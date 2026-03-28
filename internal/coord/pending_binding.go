package coord

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"openclaw-guard-kit/internal/guard"
)

// PendingBinding represents a pending robot binding waiting for user approval.
type PendingBinding struct {
	ID             string    `json:"id"`
	AgentID        string    `json:"agent_id"`
	Channel        string    `json:"channel"`
	AccountID      string    `json:"account_id"`
	ConversationID string    `json:"conversation_id"`
	CreatedAt      time.Time `json:"created_at"`
	ExpiresAt      time.Time `json:"expires_at,omitempty"`
	Status         string    `json:"status"`
}

// PersistPendingBinding writes a pending binding record into a persistent bindings file
// using the guard request-write/complete-write protocol.
// If guardClient is not set, it falls back to direct write.
func PersistPendingBinding(manifestPath string, pb PendingBinding, gc *guard.Client) error {
	dir := filepath.Dir(manifestPath)
	if manifestPath == "" {
		return fmt.Errorf("empty manifest path for pending binding persistence")
	}
	file := filepath.Join(dir, "pending_robot_bindings.json")

	var existing []PendingBinding
	data, err := os.ReadFile(file)
	if err == nil && len(data) > 0 {
		if err := json.Unmarshal(data, &existing); err != nil {
			// if unmarshal fails, start fresh
			existing = []PendingBinding{}
		}
	}

	pb.CreatedAt = time.Now().UTC()
	pb.Status = "pending"
	existing = append(existing, pb)

	raw, err := json.MarshalIndent(existing, "", "  ")
	if err != nil {
		return err
	}

	if gc == nil {
		// Fallback to direct write when guard client is not available (e.g., during testing).
		return os.WriteFile(file, raw, 0o644)
	}
	return gc.WriteFile(context.Background(), file, raw)
}
