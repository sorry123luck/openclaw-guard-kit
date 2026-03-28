package coord

import (
	"openclaw-guard-kit/internal/protocol"
	"testing"
)

// a) TargetKey has agent, but AgentID is empty -> should pass (no mismatch since AgentID not provided)
func TestValidateWriteTarget_AgentKeyWithNoAgentID(t *testing.T) {
	c := &Coordinator{}
	msg := protocol.Message{
		Kind:      protocol.KindAuthProfile,
		TargetKey: "auth:alice",
		Path:      "/agents/alice/auth-profiles.json",
	}
	if err := c.validateWriteTarget(msg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// b) AgentID provided but path points to different agent -> should error
func TestValidateWriteTarget_AgentPathMismatch(t *testing.T) {
	c := &Coordinator{}
	msg := protocol.Message{
		Kind:      protocol.KindAuthProfile,
		TargetKey: "auth:alice",
		AgentID:   "alice",
		Path:      "/agents/bob/auth-profiles.json",
	}
	if err := c.validateWriteTarget(msg); err == nil {
		t.Fatalf("expected path mismatch error, got nil")
	}
}
