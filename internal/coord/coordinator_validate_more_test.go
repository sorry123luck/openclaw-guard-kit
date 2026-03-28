package coord

import (
	"openclaw-guard-kit/internal/protocol"
	"testing"
)

// Ensure path under agent with explicit path is accepted
func TestValidateWriteTarget_AgentPathUnderAgentOK2(t *testing.T) {
	c := &Coordinator{}
	msg := protocol.Message{
		Kind:      protocol.KindModels,
		TargetKey: "models:alice",
		AgentID:   "alice",
		Path:      "/path/to/users/agents/alice/config/models.json",
	}
	if err := c.validateWriteTarget(msg); err != nil {
		t.Fatalf("expected path under agent to pass, got error: %v", err)
	}
}

// Ensure agent mismatch with path under agent triggers error (additional coverage)
func TestValidateWriteTarget_AgentMismatch_PathUnderAgent(t *testing.T) {
	c := &Coordinator{}
	msg := protocol.Message{
		Kind:      protocol.KindAuthProfile,
		TargetKey: "auth:alice",
		AgentID:   "bob",
		Path:      "/agents/alice/auth-profiles.json",
	}
	if err := c.validateWriteTarget(msg); err == nil {
		t.Fatalf("expected agent mismatch error due to AgentID mismatch")
	}
}
