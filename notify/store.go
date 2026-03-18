package notify

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	BindingStatusPending = "pending"
	BindingStatusBound   = "bound"
	BindingStatusRevoked = "revoked"

	ConnectionStatusUnknown = "unknown"
	ConnectionStatusOK      = "ok"
	ConnectionStatusFailed  = "failed"
)

type BindingRecord struct {
	ID               string    `json:"id"`
	Channel          string    `json:"channel"`
	AccountID        string    `json:"accountId"`
	SenderID         string    `json:"senderId"`
	DisplayName      string    `json:"displayName,omitempty"`
	PairingCode      string    `json:"pairingCode,omitempty"`
	Status           string    `json:"status"`
	BoundAt          time.Time `json:"boundAt,omitempty"`
	CreatedAt        time.Time `json:"createdAt"`
	UpdatedAt        time.Time `json:"updatedAt"`
	LastTestAt       time.Time `json:"lastTestAt,omitempty"`
	LastTestResult   string    `json:"lastTestResult,omitempty"`
	ConnectionStatus string    `json:"connectionStatus,omitempty"`
}

type PendingBinding struct {
	ID          string    `json:"id"`
	Channel     string    `json:"channel"`
	AccountID   string    `json:"accountId"`
	SenderID    string    `json:"senderId"`
	DisplayName string    `json:"displayName,omitempty"`
	PairingCode string    `json:"pairingCode"`
	CreatedAt   time.Time `json:"createdAt"`
	ExpiresAt   time.Time `json:"expiresAt"`
}

type StoreData struct {
	Version         int              `json:"version"`
	Bindings        []BindingRecord  `json:"bindings"`
	PendingBindings []PendingBinding `json:"pendingBindings"`
}

type Store struct {
	mu   sync.Mutex
	path string
	data StoreData
}

func NewStore(path string) (*Store, error) {
	s := &Store{
		path: path,
		data: StoreData{
			Version:         1,
			Bindings:        []BindingRecord{},
			PendingBindings: []PendingBinding{},
		},
	}

	if err := s.Load(); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return s, nil
		}
		return nil, err
	}

	return s, nil
}

func (s *Store) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	raw, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}

	var data StoreData
	if err := json.Unmarshal(raw, &data); err != nil {
		return err
	}

	if data.Version <= 0 {
		data.Version = 1
	}
	if data.Bindings == nil {
		data.Bindings = []BindingRecord{}
	}
	if data.PendingBindings == nil {
		data.PendingBindings = []PendingBinding{}
	}

	s.data = data
	s.cleanupExpiredPendingLocked()
	return nil
}

func (s *Store) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked()
}

func (s *Store) ListBindings() []BindingRecord {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]BindingRecord, len(s.data.Bindings))
	copy(out, s.data.Bindings)
	return out
}

func (s *Store) ListPendingBindings() []PendingBinding {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.cleanupExpiredPendingLocked()

	out := make([]PendingBinding, len(s.data.PendingBindings))
	copy(out, s.data.PendingBindings)
	return out
}

func (s *Store) UpsertPending(p PendingBinding) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	p.ID = strings.TrimSpace(p.ID)
	p.Channel = strings.TrimSpace(p.Channel)
	p.AccountID = strings.TrimSpace(p.AccountID)
	p.SenderID = strings.TrimSpace(p.SenderID)
	p.DisplayName = strings.TrimSpace(p.DisplayName)
	p.PairingCode = strings.ToUpper(strings.TrimSpace(p.PairingCode))

	if p.ID == "" {
		p.ID = buildBindingID(p.Channel, p.AccountID, p.SenderID)
	}
	if p.CreatedAt.IsZero() {
		p.CreatedAt = now
	}
	if p.ExpiresAt.IsZero() {
		p.ExpiresAt = now.Add(1 * time.Hour)
	}

	replaced := false
	for i := range s.data.PendingBindings {
		if s.data.PendingBindings[i].ID == p.ID {
			s.data.PendingBindings[i] = p
			replaced = true
			break
		}
	}
	if !replaced {
		s.data.PendingBindings = append(s.data.PendingBindings, p)
	}

	return s.saveLocked()
}

func (s *Store) RemovePending(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	id = strings.TrimSpace(id)
	if id == "" {
		return nil
	}

	filtered := s.data.PendingBindings[:0]
	for _, item := range s.data.PendingBindings {
		if item.ID != id {
			filtered = append(filtered, item)
		}
	}
	s.data.PendingBindings = filtered

	return s.saveLocked()
}

func (s *Store) FindPendingByCode(channel, accountID, code string) (PendingBinding, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.cleanupExpiredPendingLocked()

	channel = strings.TrimSpace(channel)
	accountID = strings.TrimSpace(accountID)
	code = strings.ToUpper(strings.TrimSpace(code))

	for _, item := range s.data.PendingBindings {
		if item.Channel == channel && item.AccountID == accountID && item.PairingCode == code {
			return item, true
		}
	}

	return PendingBinding{}, false
}

func (s *Store) MarkBound(channel, accountID, senderID, displayName, pairingCode string) (BindingRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	channel = strings.TrimSpace(channel)
	accountID = strings.TrimSpace(accountID)
	senderID = strings.TrimSpace(senderID)
	displayName = strings.TrimSpace(displayName)
	pairingCode = strings.ToUpper(strings.TrimSpace(pairingCode))

	id := buildBindingID(channel, accountID, senderID)

	record := BindingRecord{
		ID:               id,
		Channel:          channel,
		AccountID:        accountID,
		SenderID:         senderID,
		DisplayName:      displayName,
		PairingCode:      pairingCode,
		Status:           BindingStatusBound,
		BoundAt:          now,
		CreatedAt:        now,
		UpdatedAt:        now,
		ConnectionStatus: ConnectionStatusUnknown,
	}

	updated := false
	for i := range s.data.Bindings {
		if s.data.Bindings[i].ID == id {
			record.CreatedAt = s.data.Bindings[i].CreatedAt
			record.LastTestAt = s.data.Bindings[i].LastTestAt
			record.LastTestResult = s.data.Bindings[i].LastTestResult
			record.ConnectionStatus = s.data.Bindings[i].ConnectionStatus
			s.data.Bindings[i] = record
			updated = true
			break
		}
	}
	if !updated {
		s.data.Bindings = append(s.data.Bindings, record)
	}

	filtered := s.data.PendingBindings[:0]
	for _, item := range s.data.PendingBindings {
		if item.ID != id {
			filtered = append(filtered, item)
		}
	}
	s.data.PendingBindings = filtered

	if err := s.saveLocked(); err != nil {
		return BindingRecord{}, err
	}

	return record, nil
}

func (s *Store) UpdateBindingTestResult(channel, accountID, senderID, result, connectionStatus string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	id := buildBindingID(channel, accountID, senderID)
	now := time.Now().UTC()

	for i := range s.data.Bindings {
		if s.data.Bindings[i].ID == id {
			s.data.Bindings[i].LastTestAt = now
			s.data.Bindings[i].LastTestResult = strings.TrimSpace(result)
			s.data.Bindings[i].ConnectionStatus = strings.TrimSpace(connectionStatus)
			s.data.Bindings[i].UpdatedAt = now
			return s.saveLocked()
		}
	}

	return nil
}

func (s *Store) RevokeBinding(channel, accountID, senderID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	id := buildBindingID(channel, accountID, senderID)
	now := time.Now().UTC()

	for i := range s.data.Bindings {
		if s.data.Bindings[i].ID == id {
			s.data.Bindings[i].Status = BindingStatusRevoked
			s.data.Bindings[i].UpdatedAt = now
			return s.saveLocked()
		}
	}

	return nil
}

func (s *Store) cleanupExpiredPendingLocked() {
	now := time.Now().UTC()
	filtered := s.data.PendingBindings[:0]
	for _, item := range s.data.PendingBindings {
		if item.ExpiresAt.IsZero() || item.ExpiresAt.After(now) {
			filtered = append(filtered, item)
		}
	}
	s.data.PendingBindings = filtered
}

func (s *Store) saveLocked() error {
	if s.data.Version <= 0 {
		s.data.Version = 1
	}
	if s.data.Bindings == nil {
		s.data.Bindings = []BindingRecord{}
	}
	if s.data.PendingBindings == nil {
		s.data.PendingBindings = []PendingBinding{}
	}

	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}

	raw, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}

	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".notify-store-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	_ = os.Remove(s.path)
	return os.Rename(tmpName, s.path)
}

func buildBindingID(channel, accountID, senderID string) string {
	return strings.ToLower(strings.TrimSpace(channel)) + "|" +
		strings.TrimSpace(accountID) + "|" +
		strings.TrimSpace(senderID)
}
