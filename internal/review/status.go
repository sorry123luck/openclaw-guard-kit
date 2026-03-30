//go:build windows

package review

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// ReviewStatusFile represents the status information stored in review-status.json
type ReviewStatusFile struct {
	CandidateBatchKey string    `json:"candidateBatchKey,omitempty"`
	CandidateStatus   string    `json:"candidateStatus,omitempty"`
	CandidateTargets  []string  `json:"candidateTargets,omitempty"`
	CandidateSince    time.Time `json:"candidateSince,omitempty"`
	HealthStatus      string    `json:"healthStatus,omitempty"`
	HealthMessage     string    `json:"healthMessage,omitempty"`
	LastHealthCheckAt time.Time `json:"lastHealthCheckAt,omitempty"`
	DiagnosisStatus   string    `json:"diagnosisStatus,omitempty"`
	DiagnosisSummary  string    `json:"diagnosisSummary,omitempty"`
	DiagnosisAt       time.Time `json:"diagnosisAt,omitempty"`
	DoctorLogPath     string    `json:"doctorLogPath,omitempty"`
	RollbackStatus    string    `json:"rollbackStatus,omitempty"`
	RollbackMessage   string    `json:"rollbackMessage,omitempty"`
	RollbackAt        time.Time `json:"rollbackAt,omitempty"`
}

// reviewStatusFilePath returns the path to the review status file.
func reviewStatusFilePath(rootDir string) string {
	if rootDir == "" {
		return ""
	}
	return filepath.Join(rootDir, ".guard-state", "review-status.json")
}

// WriteReviewStatusFile writes the given status to the review status file.
func WriteReviewStatusFile(rootDir string, status *ReviewStatusFile) error {
	if rootDir == "" {
		return nil
	}
	path := reviewStatusFilePath(rootDir)
	if path == "" {
		return nil
	}
	// Ensure the directory exists.
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	// Marshal the status to JSON.
	data, err := json.MarshalIndent(status, "", "  ")
	if err != nil {
		return err
	}
	// Write to a temporary file and then rename to avoid partial writes.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ReadReviewStatusFile reads the review status file and returns the status.
// If the file does not exist, it returns a zero value status and no error.
func ReadReviewStatusFile(rootDir string) (*ReviewStatusFile, error) {
	if rootDir == "" {
		return &ReviewStatusFile{}, nil
	}
	path := reviewStatusFilePath(rootDir)
	if path == "" {
		return &ReviewStatusFile{}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// Return zero value if file doesn't exist.
			return &ReviewStatusFile{}, nil
		}
		return nil, err
	}
	var status ReviewStatusFile
	if err := json.Unmarshal(data, &status); err != nil {
		return nil, err
	}
	return &status, nil
}
