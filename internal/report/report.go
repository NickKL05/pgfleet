// Package report renders run results in human and JSON form. The same report
// structure backs both renderers so they never drift (R4.5).
package report

import "time"

// Status values a tenant can end a run with.
const (
	StatusOK               = "ok"
	StatusNoChange         = "no-change"
	StatusFailed           = "failed"
	StatusLocked           = "locked"
	StatusChecksumMismatch = "checksum-mismatch"
	StatusSkipped          = "skipped"
)

// TenantResult is the outcome for one tenant schema.
type TenantResult struct {
	Schema string `json:"schema"`
	From   int    `json:"from"`
	To     int    `json:"to"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// RunReport is the full machine-readable report for one invocation.
type RunReport struct {
	RunID     string         `json:"run_id"`
	StartedAt time.Time      `json:"started_at"`
	Command   string         `json:"command"`
	Tenants   []TenantResult `json:"tenants"`
	Summary   map[string]int `json:"summary"`
}

// NewRunReport seeds a report with a run id and start time.
func NewRunReport(runID, command string, startedAt time.Time) *RunReport {
	return &RunReport{
		RunID:     runID,
		StartedAt: startedAt,
		Command:   command,
		Summary:   map[string]int{},
	}
}

// Add records one tenant result and updates the summary counts.
func (r *RunReport) Add(res TenantResult) {
	r.Tenants = append(r.Tenants, res)
	r.Summary[res.Status]++
}

// Failed reports whether any tenant ended in a non-success terminal state. Used
// to choose the process exit code (1 when true).
func (r *RunReport) Failed() bool {
	for status, n := range r.Summary {
		if n == 0 {
			continue
		}
		switch status {
		case StatusFailed, StatusChecksumMismatch, StatusLocked:
			return true
		}
	}
	return false
}
