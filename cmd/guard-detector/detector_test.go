//go:build windows

package main

import "testing"

func TestShouldRollbackCandidateHardFailureOnly(t *testing.T) {
	tests := []struct {
		name          string
		summary       DoctorSummary
		verifyMessage string
		want          bool
	}{
		{
			name:    "explicit rollback category",
			summary: DoctorSummary{Category: "rollback"},
			want:    true,
		},
		{
			name:    "explicit self heal category",
			summary: DoctorSummary{Category: "self_heal"},
			want:    false,
		},
		{
			name:          "generic auth wording should not rollback",
			summary:       DoctorSummary{Category: "ignored"},
			verifyMessage: "model probe failed due to auth/api key check",
			want:          false,
		},
		{
			name:          "runtime failure should not rollback",
			summary:       DoctorSummary{Category: "ignored"},
			verifyMessage: "service not running",
			want:          false,
		},
		{
			name:          "explicit invalid config should rollback",
			summary:       DoctorSummary{Category: "unknown"},
			verifyMessage: "invalid config: unknown key providers.xxx",
			want:          true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldRollbackCandidate(tt.summary, tt.verifyMessage)
			if got != tt.want {
				t.Fatalf("shouldRollbackCandidate() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSummarizeDoctorOutputPluginHardErrorIsRollback(t *testing.T) {
	d := &Detector{}

	got := d.summarizeDoctorOutput(DoctorResult{
		Output: "plugin hard error: failed to load",
	}, "")

	if got.Category != "rollback" {
		t.Fatalf("category = %q, want rollback", got.Category)
	}
}
