//go:build windows

package review

// ReviewConfig holds the configuration for the candidate review process.
// It includes the fields needed by the review worker: openclaw path, root dir, agent id, and review-specific parameters.
type ReviewConfig struct {
	// OpenClawPath is the path to the openclaw executable.
	OpenClawPath string
	// RootDir is the root directory for state and logs.
	RootDir string
	// AgentID is the agent identifier.
	AgentID string
	// CandidateStableSeconds is the time a candidate must stay healthy
	// before it can be promoted.
	CandidateStableSeconds int
	// HealthCheckIntervalSec is the interval between health checks.
	HealthCheckIntervalSec int
	// HealthCommandTimeoutSec is the timeout for a single health check command.
	HealthCommandTimeoutSec int
	// DoctorCommandTimeoutSec is the timeout for the openclaw doctor command.
	DoctorCommandTimeoutSec int
	// DoctorDeep indicates whether to run doctor with --deep flag.
	DoctorDeep bool
}
