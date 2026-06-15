package drift

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/NickKL05/pgfleet/internal/drift/catalog"
	"github.com/NickKL05/pgfleet/internal/drift/diffgen"
	"github.com/NickKL05/pgfleet/internal/pgutil"
)

// Repair generates a corrective DDL plan per tenant against a live reference
// schema. It only reads the catalog; nothing is executed (R5.7). Repair needs
// the full object definitions, so it requires a schema-mode reference rather
// than a snapshot.
func Repair(ctx context.Context, db catalog.Querier, tenants []string, refSchema string, opts diffgen.RepairOptions) ([]*diffgen.RepairPlan, error) {
	refModels, err := Models(ctx, db, []string{refSchema})
	if err != nil {
		return nil, err
	}
	ref := refModels[refSchema]
	if ref == nil {
		return nil, fmt.Errorf("reference schema %q not found", refSchema)
	}

	tenantModels, err := Models(ctx, db, tenants)
	if err != nil {
		return nil, err
	}

	plans := make([]*diffgen.RepairPlan, 0, len(tenants))
	for _, t := range tenants {
		tm := tenantModels[t]
		if tm == nil {
			tm = modelOrEmpty(nil, t)
		}
		plans = append(plans, diffgen.GenerateRepair(ref, tm, t, opts))
	}
	return plans, nil
}

// RenderRepairSQL renders a tenant's repair plan as a runnable SQL file. The
// search_path header makes the schema-relative statements resolve to the tenant
// when run with psql. Skipped actions are listed as comments so nothing is lost.
func RenderRepairSQL(plan *diffgen.RepairPlan) string {
	var b strings.Builder
	fmt.Fprintf(&b, "-- pgfleet repair for %s\n", plan.Tenant)
	fmt.Fprintf(&b, "-- %d statement(s), %d skipped\n", len(plan.Statements), len(plan.Skipped))
	fmt.Fprintf(&b, "SET search_path TO %s, public;\n\n", pgutil.QuoteIdent(plan.Tenant))

	for _, s := range plan.Statements {
		if s.Destructive {
			fmt.Fprintf(&b, "-- destructive: %s\n", s.Object)
		}
		fmt.Fprintf(&b, "%s\n", s.SQL)
	}

	if len(plan.Skipped) > 0 {
		b.WriteString("\n-- skipped (manual action or --allow-destructive required):\n")
		for _, sk := range plan.Skipped {
			fmt.Fprintf(&b, "--   %s: %s\n", sk.Object, sk.Reason)
			if sk.SQL != "" {
				fmt.Fprintf(&b, "--     %s\n", sk.SQL)
			}
		}
	}
	return b.String()
}

// WriteRepairFiles writes one file per tenant with work into dir (e.g.
// repair/tenant_42.sql) and returns the written paths (R5.7).
func WriteRepairFiles(dir string, plans []*diffgen.RepairPlan) ([]string, error) {
	needDir := false
	for _, p := range plans {
		if !p.Empty() {
			needDir = true
			break
		}
	}
	if needDir {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create repair dir %s: %w", dir, err)
		}
	}

	var written []string
	for _, p := range plans {
		if p.Empty() {
			continue
		}
		path := filepath.Join(dir, p.Tenant+".sql")
		if err := os.WriteFile(path, []byte(RenderRepairSQL(p)), 0o644); err != nil {
			return written, fmt.Errorf("write %s: %w", path, err)
		}
		written = append(written, path)
	}
	return written, nil
}

// ApplyOptions carries the lock and timeout rules for applying repairs. They
// mirror the migration runner so a repair behaves like a migration (R5.8).
type ApplyOptions struct {
	LockID           int64
	StatementTimeout time.Duration
	LockTimeout      time.Duration
}

// ApplyRepair runs a tenant's repair statements in one transaction, guarded by
// the same per-tenant advisory lock and timeouts as migrations (R5.8).
func ApplyRepair(ctx context.Context, pool *pgxpool.Pool, plan *diffgen.RepairPlan, opts ApplyOptions) error {
	if !plan.HasWork() {
		return nil
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire connection: %w", err)
	}
	defer conn.Release()

	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback after commit is a no-op

	var locked bool
	if err := tx.QueryRow(ctx,
		"select pg_try_advisory_xact_lock($1, hashtext($2))", opts.LockID, plan.Tenant).Scan(&locked); err != nil {
		return fmt.Errorf("advisory lock: %w", err)
	}
	if !locked {
		return fmt.Errorf("tenant %s is locked by another runner", plan.Tenant)
	}

	if err := pgutil.SetSearchPath(ctx, tx, plan.Tenant); err != nil {
		return err
	}
	if err := setLocalTimeouts(ctx, tx, opts); err != nil {
		return err
	}

	for _, s := range plan.Statements {
		if _, err := tx.Exec(ctx, s.SQL); err != nil {
			return fmt.Errorf("apply %s: %w", s.Object, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

func setLocalTimeouts(ctx context.Context, tx pgx.Tx, opts ApplyOptions) error {
	if opts.StatementTimeout > 0 {
		if _, err := tx.Exec(ctx, fmt.Sprintf("set local statement_timeout = %d", opts.StatementTimeout.Milliseconds())); err != nil {
			return err
		}
	}
	if opts.LockTimeout > 0 {
		if _, err := tx.Exec(ctx, fmt.Sprintf("set local lock_timeout = %d", opts.LockTimeout.Milliseconds())); err != nil {
			return err
		}
	}
	return nil
}
