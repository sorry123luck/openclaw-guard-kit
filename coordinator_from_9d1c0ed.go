package coord

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"openclaw-guard-kit/backup"
	"openclaw-guard-kit/internal/protocol"
	"openclaw-guard-kit/logging"
)

type Coordinator struct {
	logger       *logging.Logger
	mu           sync.Mutex
	targets      map[string]*targetState
	defaultLease time.Duration

	backupSvc    *backup.Service
	manifestPath string
}

type targetState struct {
	active *lease
	queue  []*waiter
}

type lease struct {
	LeaseID    string
	RequestID  string
	ClientID   string
	AgentID    string
	Target     string
	TargetKey  string
	Kind       string
	Path       string
	AcquiredAt time.Time
	ExpiresAt  time.Time
}

type waiter struct {
	msg protocol.Message
	ch  chan protocol.Message
}

type dispatchGrant struct {
	response protocol.Message
	ch       chan protocol.Message
}

type grantResult struct {
	response protocol.Message
}

func NewCoordinator(logger *logging.Logger) *Coordinator {
	return &Coordinator{
		logger:       logger,
		targets:      make(map[string]*targetState),
		defaultLease: 30 * time.Second,
	}
}

func (c *Coordinator) ConfigureBaselineRefresh(backupSvc *backup.Service, manifestPath string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.backupSvc = backupSvc
	c.manifestPath = strings.TrimSpace(manifestPath)
}

func (c *Coordinator) HasActiveLease(target, path, agentID string) bool {
	targetKey, err := resolveTargetKey(protocol.Message{
		Target:  target,
		Path:    path,
		AgentID: agentID,
	})
	if err != nil {
		return false
	}

	var pending *dispatchGrant

	c.mu.Lock()
	pending = c.expireIfNeededLocked(targetKey)

	state, ok := c.targets[targetKey]
	hasLease := ok && state.active != nil
	c.mu.Unlock()

	c.dispatchIfNeeded(pending)
	return hasLease
}

func (c *Coordinator) HandleMessage(ctx context.Context, msg protocol.Message) (protocol.Message, error) {
	msg = normalizeMessage(msg)

	switch msg.Type {
	case protocol.MessageWriteRequest:
		return c.handleWriteRequest(ctx, msg)
	case protocol.MessageWriteCompleted:
		return c.handleWriteCompleted(msg)
	case protocol.MessageWriteFailed:
		return c.handleWriteRelease(msg, false)
	default:
		return protocol.Message{}, fmt.Errorf("unsupported message type: %s", msg.Type)
	}
}

func (c *Coordinator) handleWriteRequest(ctx context.Context, msg protocol.Message) (protocol.Message, error) {
	targetKey, err := resolveTargetKey(msg)
	if err != nil {
		return protocol.Message{}, err
	}

	msg.TargetKey = targetKey
	if msg.Mode == "" {
		msg.Mode = protocol.WriteModeReject
	}

	var pending *dispatchGrant

	c.mu.Lock()
	pending = c.expireIfNeededLocked(targetKey)

	state := c.getOrCreateTargetStateLocked(targetKey)

	if state.active == nil && len(state.queue) == 0 {
		granted := c.activateLocked(state, msg)
		c.mu.Unlock()

		c.dispatchIfNeeded(pending)

		if c.logger != nil {
			c.logger.Info(
				"write granted",
				"agent", msg.AgentID,
				"target", msg.Target,
				"targetKey", msg.TargetKey,
				"kind", msg.Kind,
				"path", msg.Path,
				"requestId", msg.RequestID,
				"clientId", msg.ClientID,
				"leaseId", granted.response.LeaseID,
			)
		}
		return granted.response, nil
	}

	if msg.Mode == protocol.WriteModeReject {
		queuePos := len(state.queue) + 1
		resp := protocol.Message{
			Type:          protocol.MessageWriteWait,
			RequestID:     msg.RequestID,
			ClientID:      msg.ClientID,
			AgentID:       msg.AgentID,
			Target:        msg.Target,
			TargetKey:     msg.TargetKey,
			Kind:          msg.Kind,
			Path:          msg.Path,
			QueuePosition: queuePos,
			Status:        protocol.StatusBusy,
			Message:       "target busy",
			At:            time.Now().UTC(),
		}
		c.mu.Unlock()

		c.dispatchIfNeeded(pending)

		if c.logger != nil {
			c.logger.Info(
				"write busy",
				"agent", msg.AgentID,
				"target", msg.Target,
				"targetKey", msg.TargetKey,
				"kind", msg.Kind,
				"path", msg.Path,
				"requestId", msg.RequestID,
				"clientId", msg.ClientID,
				"queuePosition", queuePos,
			)
		}
		return resp, nil
	}

	wait := &waiter{
		msg: msg,
		ch:  make(chan protocol.Message, 1),
	}
	state.queue = append(state.queue, wait)
	queuePos := len(state.queue)
	c.mu.Unlock()

	c.dispatchIfNeeded(pending)

	if c.logger != nil {
		c.logger.Info(
			"write queued",
			"agent", msg.AgentID,
			"target", msg.Target,
			"targetKey", msg.TargetKey,
			"kind", msg.Kind,
			"path", msg.Path,
			"requestId", msg.RequestID,
			"clientId", msg.ClientID,
			"queuePosition", queuePos,
		)
	}

	if msg.WaitSeconds > 0 {
		timer := time.NewTimer(time.Duration(msg.WaitSeconds) * time.Second)
		defer timer.Stop()

		select {
		case granted := <-wait.ch:
			if c.logger != nil {
				c.logger.Info(
					"queued write granted",
					"agent", granted.AgentID,
					"target", granted.Target,
					"targetKey", granted.TargetKey,
					"kind", granted.Kind,
					"path", granted.Path,
					"requestId", granted.RequestID,
					"clientId", granted.ClientID,
					"leaseId", granted.LeaseID,
				)
			}
			return granted, nil

		case <-timer.C:
			c.removeWaiter(targetKey, msg.RequestID, msg.ClientID, msg.LeaseID)
			return protocol.Message{
				Type:          protocol.MessageWriteWait,
				RequestID:     msg.RequestID,
				ClientID:      msg.ClientID,
				AgentID:       msg.AgentID,
				Target:        msg.Target,
				TargetKey:     msg.TargetKey,
				Kind:          msg.Kind,
				Path:          msg.Path,
				QueuePosition: queuePos,
				Status:        protocol.StatusTimeout,
				Message:       "timed out waiting for lease",
				At:            time.Now().UTC(),
			}, nil

		case <-ctx.Done():
			c.removeWaiter(targetKey, msg.RequestID, msg.ClientID, msg.LeaseID)
			return protocol.Message{}, ctx.Err()
		}
	}

	select {
	case granted := <-wait.ch:
		if c.logger != nil {
			c.logger.Info(
				"queued write granted",
				"agent", granted.AgentID,
				"target", granted.Target,
				"targetKey", granted.TargetKey,
				"kind", granted.Kind,
				"path", granted.Path,
				"requestId", granted.RequestID,
				"clientId", granted.ClientID,
				"leaseId", granted.LeaseID,
			)
		}
		return granted, nil

	case <-ctx.Done():
		c.removeWaiter(targetKey, msg.RequestID, msg.ClientID, msg.LeaseID)
		return protocol.Message{}, ctx.Err()
	}
}

func (c *Coordinator) handleWriteCompleted(msg protocol.Message) (protocol.Message, error) {
	targetKey, err := resolveTargetKey(msg)
	if err != nil {
		return protocol.Message{}, err
	}

	var (
		pendingBefore *dispatchGrant
		targetName    string
		activeRequest string
		activeClient  string
		activeLeaseID string
	)

	c.mu.Lock()
	pendingBefore = c.expireIfNeededLocked(targetKey)

	state, ok := c.targets[targetKey]
	if !ok || state.active == nil {
		c.mu.Unlock()
		c.dispatchIfNeeded(pendingBefore)
		return protocol.Message{}, errors.New("no active lease for target")
	}

	if !activeLeaseMatches(state.active, msg) {
		c.mu.Unlock()
		c.dispatchIfNeeded(pendingBefore)
		return protocol.Message{}, errors.New("release does not match active lease owner")
	}

	targetName = state.active.TargetKey
	if targetName == "" {
		targetName = state.active.Target
	}
	if targetName == "" {
		targetName = msg.TargetKey
	}
	if targetName == "" {
		targetName = msg.Target
	}

	activeRequest = state.active.RequestID
	activeClient = state.active.ClientID
	activeLeaseID = state.active.LeaseID
	c.mu.Unlock()

	c.dispatchIfNeeded(pendingBefore)

	if c.shouldRefreshBaseline(targetName) {
		if _, err := c.backupSvc.RefreshBaseline(c.manifestPath, targetName); err != nil {
			return protocol.Message{}, err
		}
	}

	var (
		pendingAfter *dispatchGrant
		nextGrant    *dispatchGrant
		releasedMsg  protocol.Message
	)

	c.mu.Lock()
	pendingAfter = c.expireIfNeededLocked(targetKey)

	state, ok = c.targets[targetKey]
	if !ok || state.active == nil {
		c.mu.Unlock()
		c.dispatchIfNeeded(pendingAfter)
		return protocol.Message{}, errors.New("no active lease for target after baseline refresh")
	}

	if state.active.RequestID != activeRequest || state.active.ClientID != activeClient || state.active.LeaseID != activeLeaseID {
		c.mu.Unlock()
		c.dispatchIfNeeded(pendingAfter)
		return protocol.Message{}, errors.New("active lease changed during baseline refresh")
	}

	releasedMsg, nextGrant = c.releaseActiveLocked(targetKey, state, true)
	c.mu.Unlock()

	c.dispatchIfNeeded(pendingAfter)
	c.dispatchIfNeeded(nextGrant)

	if c.logger != nil {
		c.logger.Info(
			"write completed",
			"agent", msg.AgentID,
			"target", msg.Target,
			"targetKey", targetKey,
			"kind", msg.Kind,
			"path", msg.Path,
			"requestId", msg.RequestID,
			"leaseId", msg.LeaseID,
			"clientId", msg.ClientID,
		)
	}

	return releasedMsg, nil
}

func (c *Coordinator) handleWriteRelease(msg protocol.Message, success bool) (protocol.Message, error) {
	targetKey, err := resolveTargetKey(msg)
	if err != nil {
		return protocol.Message{}, err
	}

	var (
		pending  *dispatchGrant
		next     *dispatchGrant
		released protocol.Message
	)

	c.mu.Lock()
	pending = c.expireIfNeededLocked(targetKey)

	state, ok := c.targets[targetKey]
	if !ok || state.active == nil {
		c.mu.Unlock()
		c.dispatchIfNeeded(pending)
		return protocol.Message{}, errors.New("no active lease for target")
	}

	if !activeLeaseMatches(state.active, msg) {
		c.mu.Unlock()
		c.dispatchIfNeeded(pending)
		return protocol.Message{}, errors.New("release does not match active lease owner")
	}

	released, next = c.releaseActiveLocked(targetKey, state, success)
	c.mu.Unlock()

	c.dispatchIfNeeded(pending)
	c.dispatchIfNeeded(next)

	if c.logger != nil {
		c.logger.Info(
			"write released",
			"agent", msg.AgentID,
			"target", msg.Target,
			"targetKey", targetKey,
			"kind", msg.Kind,
			"path", msg.Path,
			"requestId", msg.RequestID,
			"leaseId", msg.LeaseID,
			"clientId", msg.ClientID,
			"success", success,
		)
	}

	return released, nil
}

func (c *Coordinator) releaseActiveLocked(targetKey string, state *targetState, success bool) (protocol.Message, *dispatchGrant) {
	active := state.active

	releasedMsg := protocol.Message{
		Type:      protocol.MessageWriteReleased,
		RequestID: active.RequestID,
		LeaseID:   active.LeaseID,
		ClientID:  active.ClientID,
		AgentID:   active.AgentID,
		Target:    active.Target,
		TargetKey: active.TargetKey,
		Kind:      active.Kind,
		Path:      active.Path,
		Status:    protocol.StatusReleased,
		At:        time.Now().UTC(),
	}

	if success {
		if c.shouldRefreshBaseline(active.TargetKey) || c.shouldRefreshBaseline(active.Target) {
			releasedMsg.Message = "write completed, baseline refreshed, and lease released"
		} else {
			releasedMsg.Message = "write completed and lease released"
		}
	} else {
		releasedMsg.Message = "write failed and lease released"
	}

	state.active = nil

	var nextGrant *dispatchGrant
	if len(state.queue) > 0 {
		next := state.queue[0]
		state.queue = state.queue[1:]
		granted := c.activateLocked(state, next.msg)
		nextGrant = &dispatchGrant{
			response: granted.response,
			ch:       next.ch,
		}
	}

	if state.active == nil && len(state.queue) == 0 {
		delete(c.targets, targetKey)
	}

	return releasedMsg, nextGrant
}

func (c *Coordinator) shouldRefreshBaseline(targetName string) bool {
	if c.backupSvc == nil {
		return false
	}
	if strings.TrimSpace(c.manifestPath) == "" {
		return false
	}
	if strings.TrimSpace(targetName) == "" {
		return false
	}
	return true
}

func (c *Coordinator) expireIfNeededLocked(targetKey string) *dispatchGrant {
	state, ok := c.targets[targetKey]
	if !ok || state.active == nil {
		return nil
	}

	now := time.Now().UTC()
	if state.active.ExpiresAt.After(now) {
		return nil
	}

	if c.logger != nil {
		c.logger.Info(
			"lease expired",
			"agent", state.active.AgentID,
			"target", state.active.Target,
			"targetKey", state.active.TargetKey,
			"kind", state.active.Kind,
			"path", state.active.Path,
			"requestId", state.active.RequestID,
			"leaseId", state.active.LeaseID,
			"clientId", state.active.ClientID,
		)
	}

	state.active = nil
	if len(state.queue) == 0 {
		delete(c.targets, targetKey)
		return nil
	}

	next := state.queue[0]
	state.queue = state.queue[1:]
	granted := c.activateLocked(state, next.msg)
	return &dispatchGrant{
		response: granted.response,
		ch:       next.ch,
	}
}

func (c *Coordinator) activateLocked(state *targetState, msg protocol.Message) grantResult {
	now := time.Now().UTC()
	leaseSeconds := msg.LeaseSeconds
	if leaseSeconds <= 0 {
		leaseSeconds = int(c.defaultLease / time.Second)
	}

	leaseID := msg.LeaseID
	if leaseID == "" {
		leaseID = fmt.Sprintf("lease-%d", now.UnixNano())
	}

	state.active = &lease{
		LeaseID:    leaseID,
		RequestID:  msg.RequestID,
		ClientID:   msg.ClientID,
		AgentID:    msg.AgentID,
		Target:     msg.Target,
		TargetKey:  msg.TargetKey,
		Kind:       msg.Kind,
		Path:       msg.Path,
		AcquiredAt: now,
		ExpiresAt:  now.Add(time.Duration(leaseSeconds) * time.Second),
	}

	return grantResult{
		response: protocol.Message{
			Type:         protocol.MessageWriteGranted,
			RequestID:    msg.RequestID,
			LeaseID:      leaseID,
			ClientID:     msg.ClientID,
			AgentID:      msg.AgentID,
			Target:       msg.Target,
			TargetKey:    msg.TargetKey,
			Kind:         msg.Kind,
			Path:         msg.Path,
			LeaseSeconds: leaseSeconds,
			ExpiresAt:    state.active.ExpiresAt,
			Status:       protocol.StatusGranted,
			Message:      "write granted",
			At:           now,
		},
	}
}

func (c *Coordinator) dispatchIfNeeded(d *dispatchGrant) {
	if d == nil {
		return
	}
	if d.ch != nil {
		d.ch <- d.response
		close(d.ch)
	}
}

func (c *Coordinator) removeWaiter(targetKey, requestID, clientID, leaseID string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	state, ok := c.targets[targetKey]
	if !ok {
		return
	}

	for i, w := range state.queue {
		if waiterMatches(w.msg, requestID, clientID, leaseID) {
			state.queue = append(state.queue[:i], state.queue[i+1:]...)
			break
		}
	}

	if state.active == nil && len(state.queue) == 0 {
		delete(c.targets, targetKey)
	}
}

func (c *Coordinator) getOrCreateTargetStateLocked(targetKey string) *targetState {
	state, ok := c.targets[targetKey]
	if ok {
		return state
	}

	state = &targetState{}
	c.targets[targetKey] = state
	return state
}

func resolveTargetKey(msg protocol.Message) (string, error) {
	if targetKey := strings.TrimSpace(msg.TargetKey); targetKey != "" {
		return strings.ToLower(targetKey), nil
	}

	path := strings.TrimSpace(msg.Path)
	if path != "" {
		if inferred, _, _, ok := inferTargetFromPath(path); ok {
			return inferred, nil
		}
		return "path:" + strings.ToLower(filepath.Clean(path)), nil
	}

	target := strings.TrimSpace(msg.Target)
	if target == "" {
		return "", errors.New("missing target, targetKey, and path")
	}

	if target == protocol.TargetOpenClaw || target == protocol.KindOpenClaw {
		return protocol.TargetOpenClaw, nil
	}

	agentID := strings.TrimSpace(msg.AgentID)
	if agentID == "" {
		return "", fmt.Errorf("missing agentId for target: %s", target)
	}

	switch target {
	case protocol.TargetAuthProfile:
		return "auth:" + strings.ToLower(agentID), nil
	case protocol.KindModels:
		return "models:" + strings.ToLower(agentID), nil
	default:
		return "target:" + strings.ToLower(target) + "|agent:" + strings.ToLower(agentID), nil
	}
}

func inferTargetFromPath(path string) (targetKey, kind, agentID string, ok bool) {
	clean := filepath.Clean(strings.TrimSpace(path))
	if clean == "." || clean == "" {
		return "", "", "", false
	}

	lowerClean := strings.ToLower(clean)
	if filepath.Base(lowerClean) == "openclaw.json" {
		return protocol.TargetOpenClaw, protocol.KindOpenClaw, "", true
	}

	slash := filepath.ToSlash(clean)
	parts := strings.Split(slash, "/")
	if len(parts) < 4 {
		return "", "", "", false
	}

	n := len(parts)
	last := strings.ToLower(parts[n-1])
	prev := strings.ToLower(parts[n-2])
	agentDir := parts[n-3]
	agentsMarker := strings.ToLower(parts[n-4])

	if agentsMarker != "agents" || prev != "agent" || strings.TrimSpace(agentDir) == "" {
		return "", "", "", false
	}

	switch last {
	case "auth-profiles.json":
		return "auth:" + strings.ToLower(agentDir), protocol.KindAuthProfile, agentDir, true
	case "models.json":
		return "models:" + strings.ToLower(agentDir), protocol.KindModels, agentDir, true
	default:
		return "", "", "", false
	}
}

func activeLeaseMatches(active *lease, msg protocol.Message) bool {
	if active == nil {
		return false
	}
	if strings.TrimSpace(msg.LeaseID) != "" {
		return active.LeaseID == strings.TrimSpace(msg.LeaseID)
	}
	return active.RequestID == msg.RequestID && active.ClientID == msg.ClientID
}

func waiterMatches(msg protocol.Message, requestID, clientID, leaseID string) bool {
	if strings.TrimSpace(leaseID) != "" && strings.TrimSpace(msg.LeaseID) != "" {
		return strings.TrimSpace(msg.LeaseID) == strings.TrimSpace(leaseID)
	}
	return msg.RequestID == requestID && msg.ClientID == clientID
}

func normalizeMessage(msg protocol.Message) protocol.Message {
	msg.Type = strings.TrimSpace(msg.Type)
	msg.RequestID = strings.TrimSpace(msg.RequestID)
	msg.LeaseID = strings.TrimSpace(msg.LeaseID)
	msg.ClientID = strings.TrimSpace(msg.ClientID)
	msg.AgentID = strings.TrimSpace(msg.AgentID)
	msg.Target = strings.TrimSpace(msg.Target)
	msg.TargetKey = strings.TrimSpace(msg.TargetKey)
	msg.Kind = strings.TrimSpace(msg.Kind)
	msg.Path = strings.TrimSpace(msg.Path)
	msg.Mode = strings.TrimSpace(msg.Mode)
	msg.Reason = strings.TrimSpace(msg.Reason)

	if msg.At.IsZero() {
		msg.At = time.Now().UTC()
	}
	if msg.RequestID == "" {
		msg.RequestID = fmt.Sprintf("req-%d", time.Now().UTC().UnixNano())
	}
	if msg.ClientID == "" {
		msg.ClientID = "unknown-client"
	}
	if msg.Mode == "" {
		msg.Mode = protocol.WriteModeReject
	}

	if msg.Kind == "" {
		switch {
		case msg.Target == protocol.TargetOpenClaw || msg.TargetKey == protocol.TargetOpenClaw:
			msg.Kind = protocol.KindOpenClaw
		case strings.HasPrefix(strings.ToLower(msg.TargetKey), "auth:") || msg.Target == protocol.TargetAuthProfile:
			msg.Kind = protocol.KindAuthProfile
		case strings.HasPrefix(strings.ToLower(msg.TargetKey), "models:"):
			msg.Kind = protocol.KindModels
		}
	}

	if msg.TargetKey == "" && msg.Path != "" {
		if inferred, kind, _, ok := inferTargetFromPath(msg.Path); ok {
			msg.TargetKey = inferred
			if msg.Kind == "" {
				msg.Kind = kind
			}
		}
	}

	return msg
}
