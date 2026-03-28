package coord

import (
	"context"
	"openclaw-guard-kit/internal/protocol"
	"testing"
)

// Verify that handleWriteRequest correctly rejects when validateWriteTarget fails
func TestHandleWriteRequest_AgentMismatch(t *testing.T) {
	c := &Coordinator{}
	msg := protocol.Message{
		Kind:      protocol.KindAuthProfile,
		TargetKey: "auth:alice",
		AgentID:   "bob",
		Path:      "/agents/alice/auth-profiles.json",
	}
	if _, err := c.handleWriteRequest(context.Background(), msg); err == nil {
		t.Fatalf("expected error from handleWriteRequest due to agent mismatch, got nil")
	}
}
