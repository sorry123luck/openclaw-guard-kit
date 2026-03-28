package guard

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Client wraps the guard executable to perform guarded write operations.
type Client struct {
	GuardExePath string
	RootDir      string
	AgentID      string
}

// NewClient creates a new guard client.
func NewClient(guardExePath, rootDir, agentID string) *Client {
	return &Client{
		GuardExePath: guardExePath,
		RootDir:      rootDir,
		AgentID:      agentID,
	}
}

// targetKeyForFile returns the target-key to use for guarding the given file.
// The file path should be absolute. If the file is the openclaw.json or any agent's auth-profiles.json,
// it returns the conventional target-key. Otherwise, it returns the absolute path as the target-key.
func targetKeyForFile(rootDir, filePath string) string {
	openclawPath := filepath.Join(rootDir, "openclaw.json")
	if filePath == openclawPath {
		return "openclaw"
	}

	rel, err := filepath.Rel(rootDir, filePath)
	if err != nil {
		return filePath
	}
	rel = filepath.ToSlash(rel)
	parts := strings.Split(rel, "/")

	if len(parts) >= 4 && parts[0] == "agents" && parts[2] == "agent" {
		agent := parts[1]
		switch parts[3] {
		case "auth-profiles.json":
			return "auth:" + strings.ToLower(agent)
		case "models.json":
			return "models:" + strings.ToLower(agent)
		}
	}

	return filePath
}

// kindForFile returns the kind to use for guarding the given file.
// The file path should be absolute. If the file is the openclaw.json or any agent's auth-profiles.json,
// it returns the conventional kind. Otherwise, it returns "generic".
func kindForFile(rootDir, filePath string) string {
	openclawPath := filepath.Join(rootDir, "openclaw.json")
	if filePath == openclawPath {
		return "openclaw"
	}

	rel, err := filepath.Rel(rootDir, filePath)
	if err != nil {
		return "generic"
	}
	rel = filepath.ToSlash(rel)
	parts := strings.Split(rel, "/")

	if len(parts) >= 4 && parts[0] == "agents" && parts[2] == "agent" {
		switch parts[3] {
		case "auth-profiles.json":
			return "auth-profiles"
		case "models.json":
			return "models"
		}
	}

	return "generic"
}

// WriteFile writes data to a file atomically using the guard request-write/complete-write protocol.
// The file path should be relative to the root directory or absolute.
// The protocol is: request-write -> local write -> complete-write (on success) or fail-write (on failure).
func (c *Client) WriteFile(ctx context.Context, path string, data []byte) error {
	if c.GuardExePath == "" {
		return fmt.Errorf("guard executable path not set")
	}
	// Convert to absolute path if needed.
	if !filepath.IsAbs(path) {
		path = filepath.Join(c.RootDir, path)
	}
	// Determine the target-key and kind for this file.
	targetKey := targetKeyForFile(c.RootDir, path)
	kind := kindForFile(c.RootDir, path)

	requestID := fmt.Sprintf("guard-write-%d", time.Now().Unix())
	// Step 1: request-write to acquire a lease.
	requestOut, err := c.run(ctx, "request-write",
		"--agent", c.AgentID,
		"--target-key", targetKey,
		"--kind", kind,
		"--client", "guard-client",
		"--request", requestID,
		"--lease", "30",
		"--path", path, // Also pass the path for completeness, though not used for guarding by the server.
	)
	if err != nil {
		return err
	}
	leaseID := extractField(requestOut, "leaseId:")
	if leaseID == "" {
		return fmt.Errorf("request-write succeeded but leaseId was not found in output")
	}

	// Step 2: Perform the actual write locally (atomic write).
	// Write to a temporary file in the same directory, then rename to target.
	dir := filepath.Dir(path)
	tmpFile, err := os.CreateTemp(dir, ".guard-write-*.tmp")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	// Write data to temp file.
	if _, err := tmpFile.Write(data); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("%w", err)
	}
	if err := tmpFile.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to close temp file: %w", err)
	}
	// Rename temp file to target (atomic on same filesystem).
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to rename temp file: %w", err)
	}

	// Step 3: complete-write to release the lease on success.
	_, err = c.run(ctx, "complete-write",
		"--lease-id", leaseID,
		"--target-key", targetKey,
		"--kind", kind,
		"--client", "guard-client",
		"--request", requestID,
		"--path", path,
	)
	if err != nil {
		// If complete-write fails, we still consider the write successful locally,
		// but we have a leaked lease. We attempt to fail-write to release the lease.
		_, _ = c.run(ctx, "fail-write",
			"--lease-id", leaseID,
			"--target-key", targetKey,
			"--kind", kind,
			"--client", "guard-client",
			"--request", requestID,
			"--path", path,
			"--reason", "complete-write failed",
		)
		return fmt.Errorf("write succeeded locally but complete-write failed: %w", err)
	}

	return nil
}

// RemoveFile removes a file using the guard protocol (by writing an empty file?).
// For simplicity, we implement removal by writing an empty file.
func (c *Client) RemoveFile(ctx context.Context, path string) error {
	if c.GuardExePath == "" {
		return fmt.Errorf("guard executable path not set")
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(c.RootDir, path)
	}
	targetKey := targetKeyForFile(c.RootDir, path)
	kind := kindForFile(c.RootDir, path)

	requestID := fmt.Sprintf("guard-remove-%d", time.Now().Unix())
	// Request lease.
	requestOut, err := c.run(ctx, "request-write",
		"--agent", c.AgentID,
		"--target-key", targetKey,
		"--kind", kind,
		"--client", "guard-client",
		"--request", requestID,
		"--lease", "30",
		"--path", path,
	)
	if err != nil {
		return err
	}
	leaseID := extractField(requestOut, "leaseId:")
	if leaseID == "" {
		return fmt.Errorf("request-write succeeded but leaseId was not found in output")
	}

	// Remove the file locally.
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		// Attempt to release lease on error.
		_, _ = c.run(ctx, "fail-write",
			"--lease-id", leaseID,
			"--target-key", targetKey,
			"--kind", kind,
			"--client", "guard-client",
			"--request", requestID,
			"--path", path,
			"--reason", "remove failed",
		)
		return fmt.Errorf("failed to remove file: %w", err)
	}

	// Release lease via complete-write.
	_, err = c.run(ctx, "complete-write",
		"--lease-id", leaseID,
		"--target-key", targetKey,
		"--kind", kind,
		"--client", "guard-client",
		"--request", requestID,
		"--path", path,
	)
	if err != nil {
		return fmt.Errorf("remove succeeded locally but complete-write failed: %w", err)
	}
	return nil
}

// run executes the guard executable with the given arguments and returns the combined output.
func (c *Client) run(ctx context.Context, args ...string) (string, error) {
	timeoutCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	cmd := exec.CommandContext(timeoutCtx, c.GuardExePath, args...)
	output, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(output))
	if err != nil {
		if text != "" {
			return text, fmt.Errorf("%s", text)
		}
		return text, err
	}
	return text, nil
}

// extractField extracts a field value from the guard command output.
func extractField(text, prefix string) string {
	lines := strings.FieldsFunc(text, func(r rune) bool { return r == '\r' || r == '\n' })
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
	}
	return ""
}
