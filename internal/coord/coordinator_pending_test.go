package coord

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"openclaw-guard-kit/internal/protocol"
)

func TestPersistPendingBinding(t *testing.T) {
	// Create a temporary directory for our test manifest
	tmpDir := t.TempDir()
	manifestPath := filepath.Join(tmpDir, "manifest.json")

	// Create a simple manifest file so the directory exists
	manifest := []byte(`{"version":1,"targets":[]}`)
	if err := os.WriteFile(manifestPath, manifest, 0644); err != nil {
		t.Fatalf("Failed to create manifest: %v", err)
	}

	// Call PersistPendingBinding directly
	pb := PendingBinding{
		ID:             "test-bind-123",
		AgentID:        "test-agent",
		Channel:        "test-channel",
		AccountID:      "test-account",
		ConversationID: "test-conversation",
		CreatedAt:      time.Now().UTC(),
		Status:         "pending",
	}

	err := PersistPendingBinding(manifestPath, pb, nil)
	if err != nil {
		t.Fatalf("PersistPendingBinding failed: %v", err)
	}

	// Verify the file was created
	bindingsFile := filepath.Join(filepath.Dir(manifestPath), "pending_robot_bindings.json")
	if _, err := os.Stat(bindingsFile); os.IsNotExist(err) {
		t.Fatalf("Bindings file was not created")
	}

	// Verify content can be read and unmarshaled
	data, err := os.ReadFile(bindingsFile)
	if err != nil {
		t.Fatalf("Failed to read bindings file: %v", err)
	}

	var bindings []PendingBinding
	if err := json.Unmarshal(data, &bindings); err != nil {
		t.Fatalf("Failed to unmarshal bindings: %v", err)
	}

	if len(bindings) != 1 {
		t.Fatalf("Expected 1 binding, got %d", len(bindings))
	}

	if bindings[0].ID != "test-bind-123" {
		t.Fatalf("Expected binding ID 'test-bind-123', got '%s'", bindings[0].ID)
	}
}

// TestCoordinatorHandleWriteCompletedNoPendingBinding verifies that handleWriteCompleted
// does NOT emit binding.pending event for normal complete-write operations.
// Binding pending events are only emitted for explicit binding operations, not for regular writes.
func TestCoordinatorHandleWriteCompletedNoPendingBinding(t *testing.T) {
	// Setup a mock dispatcher to capture all events
	var dispatchedEvents []protocol.Event
	dispatcher := EventDispatcherFunc(func(ctx context.Context, e protocol.Event) error {
		dispatchedEvents = append(dispatchedEvents, e)
		return nil
	})

	c := &Coordinator{
		dispatcher: dispatcher,
		logger:     nil, // Use nil logger to avoid output
		targets:    make(map[string]*targetState),
	}
	c.ConfigureBaselineRefresh(nil, "")

	// First, establish an active lease by sending a write request
	writeReq := protocol.Message{
		Type:         protocol.MessageWriteRequest,
		Target:       "test",
		TargetKey:    "test:key",
		AgentID:      "test-agent",
		Path:         "/agents/test-agent/test-kind", // Path under agent directory
		Kind:         "test-kind",
		RequestID:    "req-123",
		ClientID:     "client-456",
		LeaseID:      "lease-789",
		LeaseSeconds: 10,
	}

	// This should grant the lease and create an active lease
	_, err := c.handleWriteRequest(context.Background(), writeReq)
	if err != nil {
		t.Fatalf("handleWriteRequest failed: %v", err)
	}

	// Now create a write completed message for the same lease
	writeCompletedMsg := protocol.Message{
		Type:      protocol.MessageWriteCompleted,
		Target:    "test",
		TargetKey: "test:key",
		AgentID:   "test-agent",
		Path:      "/agents/test-agent/test-kind", // Same path
		Kind:      "test-kind",
		RequestID: "req-123",
		ClientID:  "client-456",
		LeaseID:   "lease-789",
	}

	// Call handleWriteCompleted - normal complete-write should NOT emit binding.pending
	_, err = c.handleWriteCompleted(context.Background(), writeCompletedMsg)
	if err != nil {
		t.Fatalf("handleWriteCompleted failed: %v", err)
	}

	// Check that we dispatched events
	if len(dispatchedEvents) == 0 {
		t.Fatalf("Expected events to be dispatched, but got none")
	}

	// Verify that NO binding.pending event was dispatched for normal write complete
	for _, e := range dispatchedEvents {
		if e.Type == "binding.pending" {
			t.Fatalf("Did NOT expect binding.pending event for normal complete-write, but got: %+v", e)
		}
	}

	// Verify we got write.completed event
	var gotWriteCompleted bool
	for _, e := range dispatchedEvents {
		if e.Type == protocol.MessageWriteCompleted {
			gotWriteCompleted = true
			break
		}
	}
	if !gotWriteCompleted {
		t.Fatalf("Expected write.completed event, got: %v", dispatchedEvents)
	}
}

// EventDispatcherFunc is an adapter to allow using a function as an EventDispatcher
type EventDispatcherFunc func(context.Context, protocol.Event) error

func (f EventDispatcherFunc) Dispatch(ctx context.Context, e protocol.Event) error {
	return f(ctx, e)
}
