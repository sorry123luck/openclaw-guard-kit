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
	"openclaw-guard-kit/config"
	"openclaw-guard-kit/internal/protocol"
	"openclaw-guard-kit/logging"
)

type EventDispatcher interface {
	Dispatch(context.Context, protocol.Event) error
}

type Coordinator struct {
	logger       *logging.Logger
	dispatcher   EventDispatcher
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

// validateWriteTarget provides minimal target consistency validation before applying write leases.
func (c *Coordinator) validateWriteTarget(msg protocol.Message) error {
	// For targeted kinds that depend on agent routing, ensure targetKey encodes the agent correctly.
	if msg.Kind == protocol.KindAuthProfile || msg.Kind == protocol.KindModels {
		var expectedAgentID string
		if strings.HasPrefix(msg.TargetKey, "auth:") {
			expectedAgentID = strings.TrimPrefix(msg.TargetKey, "auth:")
		} else if strings.HasPrefix(msg.TargetKey, "models:") {
			expectedAgentID = strings.TrimPrefix(msg.TargetKey, "models:")
		} else {
			if msg.AgentID != "" {
				expectedAgentID = msg.AgentID
			} else {
				return nil
			}
		}
		if msg.AgentID != "" && expectedAgentID != "" && msg.AgentID != expectedAgentID {
			return fmt.Errorf("agent ID mismatch: message AgentID=%q, targetKey implies agentID=%q", msg.AgentID, expectedAgentID)
		}
		if msg.Path != "" && expectedAgentID != "" {
			if !strings.Contains(msg.Path, string(filepath.Separator)+expectedAgentID+string(filepath.Separator)) &&
				!strings.HasSuffix(msg.Path, string(filepath.Separator)+expectedAgentID) &&
				!strings.HasPrefix(msg.Path, expectedAgentID+string(filepath.Separator)) {
				return fmt.Errorf("path %q not under expected agent %q", msg.Path, expectedAgentID)
			}
		}
	}
	return nil
}

// (removed duplicate placeholder for anchor preservation)

func NewCoordinator(logger *logging.Logger, dispatcher EventDispatcher) *Coordinator {
	return &Coordinator{
		logger:       logger,
		dispatcher:   dispatcher,
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
	if pending != nil {
		c.emitFromMessage(context.Background(), pending.response, protocol.MessageWriteGranted, pending.response.Message, map[string]string{
			"source": "lease-expire-dispatch",
		})
	}
	return hasLease
}

func (c *Coordinator) HandleMessage(ctx context.Context, msg protocol.Message) (protocol.Message, error) {
	msg = normalizeMessage(msg)

	switch msg.Type {
	case protocol.MessageWriteRequest:
		c.emitFromMessage(ctx, msg, protocol.MessageWriteRequest, "write request received", nil)
		return c.handleWriteRequest(ctx, msg)
	case protocol.MessageWriteCompleted:
		return c.handleWriteCompleted(ctx, msg)
	case protocol.MessageWriteFailed:
		return c.handleWriteRelease(ctx, msg, false)
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
	if err := c.validateWriteTarget(msg); err != nil {
		return protocol.Message{}, err
	}
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
		if pending != nil {
			c.emitFromMessage(ctx, pending.response, protocol.MessageWriteGranted, pending.response.Message, map[string]string{
				"source": "lease-expire-dispatch",
			})
		}

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

		c.emitFromMessage(ctx, granted.response, protocol.MessageWriteGranted, "write granted", nil)
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
		if pending != nil {
			c.emitFromMessage(ctx, pending.response, protocol.MessageWriteGranted, pending.response.Message, map[string]string{
				"source": "lease-expire-dispatch",
			})
		}

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

		c.emitFromMessage(ctx, resp, protocol.MessageWriteWait, "target busy", map[string]string{
			"mode": protocol.WriteModeReject,
		})
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
	if pending != nil {
		c.emitFromMessage(ctx, pending.response, protocol.MessageWriteGranted, pending.response.Message, map[string]string{
			"source": "lease-expire-dispatch",
		})
	}

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

	c.emitFromMessage(ctx, msg, "write.queued", "write queued", map[string]string{
		"queuePosition": fmt.Sprintf("%d", queuePos),
		"mode":          protocol.WriteModeBlock,
	})

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
			c.emitFromMessage(ctx, granted, protocol.MessageWriteGranted, "queued write granted", nil)
			return granted, nil

		case <-timer.C:
			c.removeWaiter(targetKey, msg.RequestID, msg.ClientID, msg.LeaseID)
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
				Status:        protocol.StatusTimeout,
				Message:       "timed out waiting for lease",
				At:            time.Now().UTC(),
			}
			c.emitFromMessage(ctx, resp, protocol.MessageWriteWait, "timed out waiting for lease", nil)
			return resp, nil

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
		c.emitFromMessage(ctx, granted, protocol.MessageWriteGranted, "queued write granted", nil)
		return granted, nil

	case <-ctx.Done():
		c.removeWaiter(targetKey, msg.RequestID, msg.ClientID, msg.LeaseID)
		return protocol.Message{}, ctx.Err()
	}
}

func (c *Coordinator) handleWriteCompleted(ctx context.Context, msg protocol.Message) (protocol.Message, error) {
	targetKey, err := resolveTargetKey(msg)
	if err != nil {
		return protocol.Message{}, err
	}

	var (
		pendingBefore   *dispatchGrant
		targetName      string
		activeAgent     string
		activeTarget    string
		activeKind      string
		activePath      string
		activeRequest   string
		activeClient    string
		activeLeaseID   string
		refreshRequired bool
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

	activeAgent = state.active.AgentID
	activeTarget = state.active.Target
	activeKind = state.active.Kind
	activePath = state.active.Path
	activeRequest = state.active.RequestID
	activeClient = state.active.ClientID
	activeLeaseID = state.active.LeaseID
	refreshRequired = c.shouldRefreshBaseline(targetName)
	c.mu.Unlock()

	c.dispatchIfNeeded(pendingBefore)

	logAgent := activeAgent
	if logAgent == "" {
		logAgent = msg.AgentID
	}

	logTarget := activeTarget
	if logTarget == "" {
		logTarget = msg.Target
	}
	if logTarget == "" {
		logTarget = targetName
	}

	logKind := activeKind
	if logKind == "" {
		logKind = msg.Kind
	}

	logPath := activePath
	if logPath == "" {
		logPath = msg.Path
	}

	if refreshRequired {
		if _, err := c.backupSvc.RefreshBaseline(c.manifestPath, targetName); err != nil {
			if c.logger != nil {
				c.logger.Error(
					"baseline refresh failed after write complete",
					"agent", logAgent,
					"target", logTarget,
					"targetKey", targetKey,
					"kind", logKind,
					"path", logPath,
					"requestId", activeRequest,
					"leaseId", activeLeaseID,
					"clientId", activeClient,
					"manifestPath", c.manifestPath,
					"refreshTarget", targetName,
					"error", err,
				)
			}
			return protocol.Message{}, err
		}

		// Stage 3: create a candidate snapshot and append to manifest for future health validation
		if c.backupSvc != nil && c.manifestPath != "" {
			candTarget := config.FileTarget{Name: targetName, Path: activePath}
			cand, err := c.backupSvc.CreateCandidateSnapshot(ctx, candTarget, "./backup")
			if err == nil {
				if manifest, err2 := backup.LoadManifest(c.manifestPath); err2 == nil {
					manifest.Targets = append(manifest.Targets, cand)
					_ = backup.SaveManifest(c.manifestPath, manifest)
					if c.logger != nil {
						c.logger.Info("candidate snapshot appended to manifest", "target", cand.TargetKey, "path", cand.SourcePath)
					}
				}
			}
		}

		if c.logger != nil {
			c.logger.Info(
				"baseline refreshed after write complete",
				"agent", logAgent,
				"target", logTarget,
				"targetKey", targetKey,
				"kind", logKind,
				"path", logPath,
				"requestId", activeRequest,
				"leaseId", activeLeaseID,
				"clientId", activeClient,
				"manifestPath", c.manifestPath,
				"refreshTarget", targetName,
			)
		}

		c.emit(protocol.Event{
			Type:      "baseline.refreshed",
			AgentID:   logAgent,
			Target:    logTarget,
			TargetKey: targetKey,
			Kind:      logKind,
			Path:      logPath,
			Message:   "baseline refreshed after write complete",
			At:        time.Now().UTC(),
			Data: map[string]string{
				"requestId":     activeRequest,
				"leaseId":       activeLeaseID,
				"clientId":      activeClient,
				"manifestPath":  c.manifestPath,
				"refreshTarget": targetName,
			},
		})
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
			"agent", logAgent,
			"target", logTarget,
			"targetKey", targetKey,
			"kind", logKind,
			"path", logPath,
			"requestId", activeRequest,
			"leaseId", activeLeaseID,
			"clientId", activeClient,
			"baselineRefreshed", refreshRequired,
		)
	}

	c.emit(protocol.Event{
		Type:      protocol.MessageWriteCompleted,
		AgentID:   logAgent,
		Target:    logTarget,
		TargetKey: targetKey,
		Kind:      logKind,
		Path:      logPath,
		Message:   "write completed",
		At:        time.Now().UTC(),
		Data: map[string]string{
			"requestId": activeRequest,
			"leaseId":   activeLeaseID,
			"clientId":  activeClient,
			"status":    protocol.StatusCompleted,
		},
	})

	c.emitFromMessage(ctx, releasedMsg, protocol.MessageWriteReleased, releasedMsg.Message, nil)

	if nextGrant != nil {
		c.emitFromMessage(ctx, nextGrant.response, protocol.MessageWriteGranted, "queued write granted", nil)
	}

	return releasedMsg, nil
}

func (c *Coordinator) handleWriteRelease(ctx context.Context, msg protocol.Message, success bool) (protocol.Message, error) {
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

	if success {
		c.emit(protocol.Event{
			Type:      protocol.MessageWriteCompleted,
			AgentID:   msg.AgentID,
			Target:    msg.Target,
			TargetKey: targetKey,
			Kind:      msg.Kind,
			Path:      msg.Path,
			Message:   "write completed",
			At:        time.Now().UTC(),
			Data: map[string]string{
				"requestId": msg.RequestID,
				"leaseId":   msg.LeaseID,
				"clientId":  msg.ClientID,
				"status":    protocol.StatusCompleted,
			},
		})
	} else {
		c.emit(protocol.Event{
			Type:      protocol.MessageWriteFailed,
			AgentID:   msg.AgentID,
			Target:    msg.Target,
			TargetKey: targetKey,
			Kind:      msg.Kind,
			Path:      msg.Path,
			Message:   "write failed",
			At:        time.Now().UTC(),
			Data: map[string]string{
				"requestId": msg.RequestID,
				"leaseId":   msg.LeaseID,
				"clientId":  msg.ClientID,
				"reason":    msg.Reason,
				"status":    protocol.StatusFailed,
			},
		})
	}

	c.emitFromMessage(ctx, released, protocol.MessageWriteReleased, released.Message, nil)

	if next != nil {
		c.emitFromMessage(ctx, next.response, protocol.MessageWriteGranted, "queued write granted", nil)
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

	c.emit(protocol.Event{
		Type:      "lease.expired",
		AgentID:   state.active.AgentID,
		Target:    state.active.Target,
		TargetKey: state.active.TargetKey,
		Kind:      state.active.Kind,
		Path:      state.active.Path,
		Message:   "lease expired",
		At:        now,
		Data: map[string]string{
			"requestId": state.active.RequestID,
			"leaseId":   state.active.LeaseID,
			"clientId":  state.active.ClientID,
		},
	})

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

func (c *Coordinator) emit(event protocol.Event) {
	if c.dispatcher == nil {
		return
	}
	if event.At.IsZero() {
		event.At = time.Now().UTC()
	}
	ctx := context.Background()
	if err := c.dispatcher.Dispatch(ctx, event); err != nil && c.logger != nil {
		c.logger.Error(
			"coord dispatch failed",
			"type", event.Type,
			"target", event.Target,
			"targetKey", event.TargetKey,
			"error", err,
		)
	}
}

func (c *Coordinator) emitFromMessage(ctx context.Context, msg protocol.Message, eventType, eventMessage string, extra map[string]string) {
	if c.dispatcher == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}

	data := map[string]string{
		"requestId": msg.RequestID,
		"leaseId":   msg.LeaseID,
		"clientId":  msg.ClientID,
	}
	if msg.Status != "" {
		data["status"] = msg.Status
	}
	if msg.QueuePosition > 0 {
		data["queuePosition"] = fmt.Sprintf("%d", msg.QueuePosition)
	}
	if !msg.ExpiresAt.IsZero() {
		data["expiresAt"] = msg.ExpiresAt.UTC().Format(time.RFC3339)
	}
	if msg.Mode != "" {
		data["mode"] = msg.Mode
	}
	if msg.Reason != "" {
		data["reason"] = msg.Reason
	}
	for k, v := range extra {
		data[k] = v
	}

	event := protocol.Event{
		Type:      eventType,
		AgentID:   msg.AgentID,
		Target:    msg.Target,
		TargetKey: msg.TargetKey,
		Kind:      msg.Kind,
		Path:      msg.Path,
		Message:   eventMessage,
		At:        time.Now().UTC(),
		Data:      data,
	}

	if err := c.dispatcher.Dispatch(ctx, event); err != nil && c.logger != nil {
		c.logger.Error(
			"coord dispatch failed",
			"type", event.Type,
			"target", event.Target,
			"targetKey", event.TargetKey,
			"error", err,
		)
	}
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
