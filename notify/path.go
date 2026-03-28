package notify

import "path/filepath"

// Path helpers for storing guard-related files in OpenClaw root directory.

// BindingsPath returns the path to bindings.json
func BindingsPath(root string) string {
	return filepath.Join(root, ".guard-state", "bindings.json")
}

// CredentialsPath returns the path to channel_credentials.json
func CredentialsPath(root string) string {
	return filepath.Join(root, ".guard-state", "channel_credentials.json")
}

// ManifestPath returns the path to manifest.json
func ManifestPath(root string) string {
	return filepath.Join(root, ".guard-state", "manifest.json")
}

// GuardStateDir returns the .guard-state directory path
func GuardStateDir(root string) string {
	return filepath.Join(root, ".guard-state")
}
