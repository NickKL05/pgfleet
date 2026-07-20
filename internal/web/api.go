package web

import (
	"fmt"

	"github.com/NickKL05/pgfleet/internal/config"
	"github.com/NickKL05/pgfleet/internal/migrate"
	"github.com/NickKL05/pgfleet/internal/report"
)

// Migration status values surfaced to the UI, re-exported from the migrate
// package so the frontend has one place to key colors off.
const (
	migrationUpToDate = migrate.StatusUpToDate // "up-to-date"
	migrationBehind   = migrate.StatusBehind   // "behind"
)

// SummaryResponse backs the overview cards: the four headline numbers plus the
// reference label so the UI can name what drift is measured against.
type SummaryResponse struct {
	Total         int    `json:"total"`
	LatestVersion int    `json:"latest_version"`
	UpToDate      int    `json:"up_to_date"`
	Behind        int    `json:"behind"`
	Drifted       int    `json:"drifted"`
	Clean         int    `json:"clean"`
	Reference     string `json:"reference"`
}

// TenantRow is one row of the fleet table: migration state and drift state
// joined per tenant so the table renders both badges without a second lookup.
type TenantRow struct {
	Schema          string `json:"schema"`
	Version         int    `json:"version"`
	MigrationStatus string `json:"migration_status"` // up-to-date | behind
	Drifted         bool   `json:"drifted"`
}

// TenantsResponse is the fleet table payload.
type TenantsResponse struct {
	LatestVersion int         `json:"latest_version"`
	Reference     string      `json:"reference"`
	Tenants       []TenantRow `json:"tenants"`
}

// VersionBucket is one bar of the tenants-per-version histogram.
type VersionBucket struct {
	Version int `json:"version"`
	Count   int `json:"count"`
}

// VersionsResponse feeds the overview chart.
type VersionsResponse struct {
	LatestVersion int             `json:"latest_version"`
	Versions      []VersionBucket `json:"versions"`
}

// errUnknownReferenceMode mirrors the CLI's error for an unrecognized reference
// mode, so misconfiguration reads the same from the dashboard.
func errUnknownReferenceMode(mode config.ReferenceMode) error {
	return fmt.Errorf("unknown drift.reference.mode %q", mode)
}

// errUnknownTenant is returned for a diff request naming a schema outside the
// discovered fleet.
func errUnknownTenant(schema string) error {
	return fmt.Errorf("unknown tenant %q", schema)
}

// tenantVersion reads a tenant's current version from a migrate status result.
// migrate.Status reports From == To (no work is applied), so either is correct;
// To is used to read as "where the tenant is now".
func tenantVersion(r report.TenantResult) int { return r.To }
