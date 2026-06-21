package migrate

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/sync/errgroup"

	"github.com/NickKL05/pgfleet/internal/report"
)

// Status states reported by migrate status.
const (
	StatusUpToDate = "up-to-date"
	StatusBehind   = "behind"
)

// Status reports each tenant's current migration version and whether pending
// migrations remain. Output groups identical (version, status) pairs so a large
// healthy fleet collapses to a single line (R4.8).
func Status(ctx context.Context, pool *pgxpool.Pool, tenants []string, set *Set, table string, concurrency int) (*report.RunReport, error) {
	rep := report.NewRunReport(genStatusID(), "migrate status", time.Now().UTC())
	results := make([]report.TenantResult, len(tenants))

	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(concurrency)
	for i, schema := range tenants {
		i, schema := i, schema
		g.Go(func() error {
			results[i] = tenantStatus(ctx, pool, schema, set, table)
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}

	for _, res := range results {
		rep.Add(res)
	}
	return rep, nil
}

func tenantStatus(ctx context.Context, pool *pgxpool.Pool, schema string, set *Set, table string) report.TenantResult {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return failResult(schema, 0, 0, err)
	}
	defer conn.Release()

	current := 0
	applied, err := LoadApplied(ctx, conn, schema, table)
	if err != nil {
		// Treat a missing state table as version 0 rather than a hard error so
		// status works on freshly created tenants.
		if !isUndefinedTable(err) {
			return failResult(schema, 0, 0, err)
		}
	} else {
		current = HighestApplied(applied)
	}

	pending := set.Pending(current, 0)
	status := StatusUpToDate
	if len(pending) > 0 {
		status = StatusBehind
	}
	return report.TenantResult{Schema: schema, From: current, To: current, Status: status}
}

// isUndefinedTable reports whether err is the Postgres 42P01 undefined_table.
func isUndefinedTable(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "42P01"
	}
	return false
}

func genStatusID() string {
	return fmt.Sprintf("status-%d", time.Now().UnixNano())
}
