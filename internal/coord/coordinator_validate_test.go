package coord

import (
	"openclaw-guard-kit/internal/protocol"
	"testing"
)

// Test: valid path under agent should pass
func TestValidateWriteTarget_AgentMatch_PathUnderAgentOK(t *testing.T) {
	c := &Coordinator{}
	msg := protocol.Message{
		Kind:      protocol.KindAuthProfile,
		TargetKey: "auth:alice",
		AgentID:   "alice",
		Path:      "/agents/alice/auth-profiles.json",
		// other fields default empty
	}
	if err := c.validateWriteTarget(msg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// Test: agent mismatch in TargetKey vs AgentID should fail even if path looks plausible
func TestValidateWriteTarget_AgentMismatch_TargetKeyVsAgent(t *testing.T) {
	c := &Coordinator{}
	msg := protocol.Message{
		Kind:      protocol.KindAuthProfile,
		TargetKey: "auth:bob",
		AgentID:   "alice",
		Path:      "/agents/bob/auth-profiles.json",
	}
	if err := c.validateWriteTarget(msg); err == nil {
		t.Fatalf("expected agent mismatch error when targetKey implies bob but AgentID alice, got nil")
	}
}

// Test: path not under agent should error
func TestValidateWriteTarget_PathNotUnderAgent(t *testing.T) {
	c := &Coordinator{}
	msg := protocol.Message{
		Kind:      protocol.KindModels,
		TargetKey: "models:alice",
		AgentID:   "alice",
		Path:      "/agents/bob/models.json",
	}
	if err := c.validateWriteTarget(msg); err == nil {
		t.Fatalf("expected path not under agent error, got nil")
	}
}
