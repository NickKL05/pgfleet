package web

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/NickKL05/pgfleet/internal/config"
	"github.com/NickKL05/pgfleet/internal/drift"
	"github.com/NickKL05/pgfleet/internal/drift/model"
	"github.com/NickKL05/pgfleet/internal/migrate"
	"github.com/NickKL05/pgfleet/internal/report"
)

// Provider is the read surface the HTTP handlers depend on. It is the same set
// of pure functions the cobra commands call, so the dashboard and the CLI can
// never disagree about the fleet's state. Splitting it behind an interface also
// lets the handlers be tested without a database.
type Provider interface {
	// MigrationStatus reports each tenant's current version and whether pending
	// migrations remain (wraps migrate.Status).
	MigrationStatus(ctx context.Context) (*report.RunReport, error)
	// DriftStatus checks every tenant against the reference (wraps drift.Verify).
	DriftStatus(ctx context.Context) (*report.DriftReport, error)
	// TenantDiff returns the object- and field-level diff for one tenant
	// (wraps drift.Diff with a single-element slice).
	TenantDiff(ctx context.Context, schema string) (*report.DriftReport, error)
	// LatestVersion is the highest migration version in the set; a tenant on it
	// is fully migrated.
	LatestVersion() int
	// Tenants is the discovered tenant list, resolved once at startup.
	Tenants() []string
}

// Fleet is the production Provider. It holds the pool, config, migration set,
// and the tenant list discovered once at server startup, mirroring how the CLI
// resolves them once per invocation.
type Fleet struct {
	pool    *pgxpool.Pool
	cfg     *config.Config
	set     *migrate.Set
	tenants []string
}

// NewFleet builds a Fleet from already-resolved dependencies. The caller
// (cmd/pgfleet) performs connect/discovery with the shared app helpers and
// hands the results here, keeping database wiring in one place.
func NewFleet(pool *pgxpool.Pool, cfg *config.Config, set *migrate.Set, tenants []string) *Fleet {
	return &Fleet{pool: pool, cfg: cfg, set: set, tenants: tenants}
}

// Tenants returns the discovered tenant list.
func (f *Fleet) Tenants() []string { return f.tenants }

// LatestVersion returns the highest version in the migration set.
func (f *Fleet) LatestVersion() int { return f.set.Highest() }

// MigrationStatus wraps migrate.Status over the whole fleet.
func (f *Fleet) MigrationStatus(ctx context.Context) (*report.RunReport, error) {
	return migrate.Status(ctx, f.pool, f.tenants, f.set, f.cfg.Migrations.Table, f.cfg.Run.Concurrency)
}

// DriftStatus wraps drift.Verify over the whole fleet.
func (f *Fleet) DriftStatus(ctx context.Context) (*report.DriftReport, error) {
	opts := f.driftOptions()
	ref, err := f.reference(ctx, opts)
	if err != nil {
		return nil, err
	}
	return drift.Verify(ctx, f.pool, f.tenants, ref, opts)
}

// TenantDiff wraps drift.Diff for a single tenant.
func (f *Fleet) TenantDiff(ctx context.Context, schema string) (*report.DriftReport, error) {
	spec, err := f.referenceSpec()
	if err != nil {
		return nil, err
	}
	return drift.Diff(ctx, f.pool, []string{schema}, spec, f.driftOptions())
}

// driftOptions builds drift options from config. The dashboard uses config
// defaults; the CLI's --strict / --ignore-column-order flags stay CLI-only, so
// verify here matches `pgfleet drift verify` with no flags.
func (f *Fleet) driftOptions() drift.Options {
	return drift.Options{Model: model.Options{Ignore: f.cfg.Drift.Ignore}}
}

// reference resolves the canonical reference the same way the CLI does: a live
// template schema, or a committed snapshot file.
func (f *Fleet) reference(ctx context.Context, opts drift.Options) (*drift.Reference, error) {
	switch f.cfg.Drift.Reference.Mode {
	case config.ReferenceSchema:
		return drift.ReferenceFromSchema(ctx, f.pool, f.cfg.Drift.Reference.Schema, opts)
	case config.ReferenceSnapshot:
		return drift.ReadSnapshot(f.cfg.Drift.Reference.Snapshot)
	default:
		return nil, errUnknownReferenceMode(f.cfg.Drift.Reference.Mode)
	}
}

// referenceSpec maps the config reference block to a drift.ReferenceSpec.
func (f *Fleet) referenceSpec() (drift.ReferenceSpec, error) {
	switch f.cfg.Drift.Reference.Mode {
	case config.ReferenceSchema:
		return drift.ReferenceSpec{Mode: drift.ModeSchema, Schema: f.cfg.Drift.Reference.Schema}, nil
	case config.ReferenceSnapshot:
		return drift.ReferenceSpec{Mode: drift.ModeSnapshot, SnapshotPath: f.cfg.Drift.Reference.Snapshot}, nil
	default:
		return drift.ReferenceSpec{}, errUnknownReferenceMode(f.cfg.Drift.Reference.Mode)
	}
}
