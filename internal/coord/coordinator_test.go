package coord

import (
	"openclaw-guard-kit/internal/protocol"
	"testing"
)

// Test that validateWriteTarget detects agent mismatch for auth/models kinds
func TestValidateWriteTarget_AgentMismatch(t *testing.T) {
	c := &Coordinator{}
	// KindAuthProfile with mismatched agent in TargetKey vs AgentID
	msg := protocol.Message{
		Kind:      protocol.KindAuthProfile,
		TargetKey: "auth:alice",
		AgentID:   "bob",
		Path:      "/agents/alice/auth-profiles.json",
	}
	if err := c.validateWriteTarget(msg); err == nil {
		t.Fatalf("expected agent mismatch error, got nil")
	}
}

func TestValidateWriteTarget_PathUnderAgent(t *testing.T) {
	c := &Coordinator{}
	msg := protocol.Message{
		Kind:      protocol.KindModels,
		TargetKey: "models:alice",
		AgentID:   "alice",
		Path:      "/agents/alice/models.json",
	}
	if err := c.validateWriteTarget(msg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
