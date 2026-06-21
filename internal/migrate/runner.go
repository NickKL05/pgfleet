package migrate

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/sync/errgroup"

	"github.com/NickKL05/pgfleet/internal/pgutil"
	"github.com/NickKL05/pgfleet/internal/report"
)

// Direction selects whether the runner applies up or down migrations.
type Direction int

// Migration directions.
const (
	Up Direction = iota
	Down
)

// errTenantLocked signals that another runner holds the tenant's advisory lock.
// It is a status, not a failure (R4.3).
var errTenantLocked = errors.New("tenant locked by another runner")

// Options configures a single run.
type Options struct {
	Set              *Set
	Table            string
	LockID           int64
	Concurrency      int
	StatementTimeout time.Duration
	LockTimeout      time.Duration
	FailFast         bool
	AllowDirty       bool
	DryRun           bool
	Direction        Direction
	Target           int // 0 means "all" for up; required (>0) for down
	Logger           *slog.Logger
	// DryRunOut receives the printed SQL plan when DryRun is set.
	DryRunOut io.Writer
}

// Runner applies migrations across a set of tenants using a bounded worker pool.
type Runner struct {
	pool *pgxpool.Pool
	opts Options
}

// NewRunner builds a Runner.
func NewRunner(pool *pgxpool.Pool, opts Options) *Runner {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &Runner{pool: pool, opts: opts}
}

// Run applies migrations to every tenant, returning a populated report. Tenant
// failures are isolated: one tenant's failure never blocks others unless
// fail_fast is set (R4.4, R4.5). Tenants reporting "locked" are retried once at
// the end of the run (R4.3).
func (r *Runner) Run(ctx context.Context, runID, command string, tenants []string) (*report.RunReport, error) {
	rep := report.NewRunReport(runID, command, time.Now().UTC())

	results, err := r.runPass(ctx, tenants)
	if err != nil {
		return nil, err
	}

	var lockedAgain []string
	for _, res := range results {
		if res.Status == report.StatusLocked {
			lockedAgain = append(lockedAgain, res.Schema)
		}
	}

	// Retry the locked tenants exactly once.
	retried := map[string]report.TenantResult{}
	if len(lockedAgain) > 0 && !r.opts.DryRun {
		r.opts.Logger.Info("retrying locked tenants", "count", len(lockedAgain))
		retryResults, err := r.runPass(ctx, lockedAgain)
		if err != nil {
			return nil, err
		}
		for _, res := range retryResults {
			retried[res.Schema] = res
		}
	}

	for _, res := range results {
		if final, ok := retried[res.Schema]; ok {
			rep.Add(final)
			continue
		}
		rep.Add(res)
	}
	return rep, nil
}

// runPass processes every tenant once through the worker pool.
func (r *Runner) runPass(ctx context.Context, tenants []string) ([]report.TenantResult, error) {
	results := make([]report.TenantResult, len(tenants))

	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(r.opts.Concurrency)

	for i, schema := range tenants {
		i, schema := i, schema
		g.Go(func() error {
			res := r.migrateTenant(ctx, schema)
			results[i] = res
			if r.opts.FailFast && res.Status == report.StatusFailed {
				return fmt.Errorf("fail-fast: tenant %s failed: %s", schema, res.Error)
			}
			return nil
		})
	}

	// With fail_fast off, worker funcs never return an error, so Wait returns
	// nil and every result slot is populated. With fail_fast on, the first
	// failure cancels the context and surfaces here.
	if err := g.Wait(); err != nil && r.opts.FailFast {
		// Fill any unstarted tenants as skipped so the report stays complete.
		for i := range results {
			if results[i].Schema == "" {
				results[i] = report.TenantResult{Schema: tenants[i], Status: report.StatusSkipped}
			}
		}
		return results, nil
	}
	return results, nil
}

// migrateTenant runs one tenant's pending migrations on a dedicated connection.
func (r *Runner) migrateTenant(ctx context.Context, schema string) report.TenantResult {
	conn, err := r.pool.Acquire(ctx)
	if err != nil {
		return failResult(schema, 0, 0, fmt.Errorf("acquire connection: %w", err))
	}
	defer conn.Release()

	if r.opts.DryRun {
		return r.dryRunTenant(ctx, conn.Conn(), schema)
	}

	if err := EnsureStateTable(ctx, conn, schema, r.opts.Table); err != nil {
		return failResult(schema, 0, 0, err)
	}
	applied, err := LoadApplied(ctx, conn, schema, r.opts.Table)
	if err != nil {
		return failResult(schema, 0, 0, err)
	}
	from := HighestApplied(applied)

	if mismatch := checkChecksums(applied, r.opts.Set); mismatch != 0 {
		if !r.opts.AllowDirty {
			return report.TenantResult{
				Schema: schema, From: from, To: from,
				Status: report.StatusChecksumMismatch,
				Error:  fmt.Sprintf("checksum mismatch at version %d (file changed since apply)", mismatch),
			}
		}
		r.opts.Logger.Warn("checksum mismatch ignored due to --allow-dirty",
			"schema", schema, "version", mismatch)
	}

	if r.opts.Direction == Down {
		return r.migrateDown(ctx, conn, schema, applied, from)
	}
	return r.migrateUp(ctx, conn, schema, from)
}

func (r *Runner) migrateUp(ctx context.Context, conn *pgxpool.Conn, schema string, from int) report.TenantResult {
	pending := r.opts.Set.Pending(from, r.opts.Target)
	if len(pending) == 0 {
		return report.TenantResult{Schema: schema, From: from, To: from, Status: report.StatusNoChange}
	}

	cur := from
	for _, m := range pending {
		err := r.applyMigration(ctx, conn, schema, m, Up)
		switch {
		case errors.Is(err, errTenantLocked):
			return report.TenantResult{Schema: schema, From: from, To: cur, Status: report.StatusLocked}
		case err != nil:
			// A failed migration rolled back; the tenant stops here and the run
			// continues (R4.5).
			return failResult(schema, from, m.Version, err)
		}
		cur = m.Version
	}
	return report.TenantResult{Schema: schema, From: from, To: cur, Status: report.StatusOK}
}

func (r *Runner) migrateDown(ctx context.Context, conn *pgxpool.Conn, schema string, applied map[int]AppliedRecord, from int) report.TenantResult {
	// Roll back every applied version greater than the target, highest first.
	versions := make([]int, 0, len(applied))
	for v := range applied {
		if v > r.opts.Target {
			versions = append(versions, v)
		}
	}
	if len(versions) == 0 {
		return report.TenantResult{Schema: schema, From: from, To: from, Status: report.StatusNoChange}
	}
	// Descending order.
	for i := 0; i < len(versions); i++ {
		for j := i + 1; j < len(versions); j++ {
			if versions[j] > versions[i] {
				versions[i], versions[j] = versions[j], versions[i]
			}
		}
	}

	byVersion := map[int]Migration{}
	for _, m := range r.opts.Set.All() {
		byVersion[m.Version] = m
	}

	cur := from
	for _, v := range versions {
		m, ok := byVersion[v]
		if !ok || !m.HasDown() {
			return failResult(schema, from, v, fmt.Errorf("no down migration available for version %d", v))
		}
		err := r.applyMigration(ctx, conn, schema, m, Down)
		switch {
		case errors.Is(err, errTenantLocked):
			return report.TenantResult{Schema: schema, From: from, To: cur, Status: report.StatusLocked}
		case err != nil:
			return failResult(schema, from, v, err)
		}
		cur = v - 1
	}
	return report.TenantResult{Schema: schema, From: from, To: r.opts.Target, Status: report.StatusOK}
}

// applyMigration runs one migration in the appropriate execution mode.
func (r *Runner) applyMigration(ctx context.Context, conn *pgxpool.Conn, schema string, m Migration, dir Direction) error {
	if m.NoTransaction && dir == Up {
		return r.applyNoTx(ctx, conn, schema, m)
	}
	return r.applyInTx(ctx, conn, schema, m, dir)
}

// applyInTx runs the migration inside a single transaction with the advisory
// lock and SET LOCAL timeouts (R4.2, R4.3).
func (r *Runner) applyInTx(ctx context.Context, conn *pgxpool.Conn, schema string, m Migration, dir Direction) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback after commit is a no-op

	locked, err := r.acquireXactLock(ctx, tx, schema)
	if err != nil {
		return err
	}
	if !locked {
		return errTenantLocked
	}

	if err := r.prepareTx(ctx, tx, schema); err != nil {
		return err
	}

	sql, version := m.UpSQL, m.Version
	if dir == Down {
		sql = m.DownSQL
	}

	start := time.Now()
	if _, err := tx.Exec(ctx, string(sql)); err != nil {
		return fmt.Errorf("apply v%d: %w", version, err)
	}
	durMS := int(time.Since(start).Milliseconds())

	if dir == Up {
		if err := recordApply(ctx, tx, schema, r.opts.Table, m, durMS); err != nil {
			return err
		}
	} else {
		if err := recordRemove(ctx, tx, schema, r.opts.Table, version); err != nil {
			return err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit v%d: %w", version, err)
	}
	return nil
}

// applyNoTx runs a non-transactional up migration directly on the connection,
// guarding it with a session-level advisory lock since there is no surrounding
// transaction to scope a transaction lock to.
func (r *Runner) applyNoTx(ctx context.Context, conn *pgxpool.Conn, schema string, m Migration) error {
	var locked bool
	if err := conn.QueryRow(ctx,
		"select pg_try_advisory_lock($1, hashtext($2))", r.opts.LockID, schema).Scan(&locked); err != nil {
		return fmt.Errorf("advisory lock: %w", err)
	}
	if !locked {
		return errTenantLocked
	}
	defer func() {
		_, _ = conn.Exec(context.Background(),
			"select pg_advisory_unlock($1, hashtext($2))", r.opts.LockID, schema)
	}()

	if err := pgutil.SetSearchPath(ctx, conn, schema); err != nil {
		return err
	}

	start := time.Now()
	if _, err := conn.Exec(ctx, string(m.UpSQL)); err != nil {
		return fmt.Errorf("apply v%d (no-transaction): %w", m.Version, err)
	}
	durMS := int(time.Since(start).Milliseconds())
	return recordApply(ctx, conn, schema, r.opts.Table, m, durMS)
}

// acquireXactLock attempts the per-tenant transaction-scoped advisory lock (R4.3).
func (r *Runner) acquireXactLock(ctx context.Context, tx pgx.Tx, schema string) (bool, error) {
	var locked bool
	if err := tx.QueryRow(ctx,
		"select pg_try_advisory_xact_lock($1, hashtext($2))", r.opts.LockID, schema).Scan(&locked); err != nil {
		return false, fmt.Errorf("advisory lock: %w", err)
	}
	return locked, nil
}

// prepareTx sets the search_path and SET LOCAL timeouts for a migration tx.
func (r *Runner) prepareTx(ctx context.Context, tx pgx.Tx, schema string) error {
	if err := pgutil.SetSearchPath(ctx, tx, schema); err != nil {
		return err
	}
	if r.opts.StatementTimeout > 0 {
		if _, err := tx.Exec(ctx, fmt.Sprintf("set local statement_timeout = %d",
			r.opts.StatementTimeout.Milliseconds())); err != nil {
			return err
		}
	}
	if r.opts.LockTimeout > 0 {
		if _, err := tx.Exec(ctx, fmt.Sprintf("set local lock_timeout = %d",
			r.opts.LockTimeout.Milliseconds())); err != nil {
			return err
		}
	}
	return nil
}

// dryRunTenant computes the pending set in a read-only transaction and prints
// the exact SQL that would run, without entering write mode (R4.6, R6.5).
func (r *Runner) dryRunTenant(ctx context.Context, conn *pgx.Conn, schema string) report.TenantResult {
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return failResult(schema, 0, 0, err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	from := 0
	applied, err := LoadApplied(ctx, tx, schema, r.opts.Table)
	if err == nil {
		from = HighestApplied(applied)
	}
	// A missing state table simply means nothing is applied yet.

	var pending []Migration
	if r.opts.Direction == Down {
		pending = r.opts.Set.PendingDown(from, r.opts.Target)
	} else {
		pending = r.opts.Set.Pending(from, r.opts.Target)
	}

	if r.opts.DryRunOut != nil && len(pending) > 0 {
		fmt.Fprintf(r.opts.DryRunOut, "\n-- tenant %s (from version %d)\n", schema, from)
		for _, m := range pending {
			body := m.UpSQL
			if r.opts.Direction == Down {
				body = m.DownSQL
			}
			fmt.Fprintf(r.opts.DryRunOut, "-- v%d %s\n%s\n", m.Version, m.Name, string(body))
		}
	}

	to := from
	if len(pending) > 0 {
		if r.opts.Direction == Down {
			to = r.opts.Target
		} else {
			to = pending[len(pending)-1].Version
		}
	}
	status := report.StatusOK
	if len(pending) == 0 {
		status = report.StatusNoChange
	}
	return report.TenantResult{Schema: schema, From: from, To: to, Status: status}
}

func failResult(schema string, from, to int, err error) report.TenantResult {
	return report.TenantResult{
		Schema: schema, From: from, To: to,
		Status: report.StatusFailed,
		Error:  err.Error(),
	}
}
