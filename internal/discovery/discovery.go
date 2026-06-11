// Package discovery enumerates tenant schemas. A single implementation is
// shared by the migration and drift subsystems, and its result is cached for
// the lifetime of one invocation (R6.1).
package discovery

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/NickKL05/pgfleet/internal/config"
)

// Querier is the read surface discovery needs; *pgxpool.Pool satisfies it.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// Discoverer resolves the tenant list once and caches it.
type Discoverer struct {
	cfg    config.Tenants
	cached []string
	done   bool
}

// New builds a Discoverer from the tenants config block.
func New(cfg config.Tenants) *Discoverer {
	return &Discoverer{cfg: cfg}
}

// Tenants returns the active tenant schemas, applying exclude rules. The result
// is computed once and cached (R6.1).
func (d *Discoverer) Tenants(ctx context.Context, db Querier) ([]string, error) {
	if d.done {
		return d.cached, nil
	}

	var raw []string
	var err error
	switch d.cfg.Discovery.Mode {
	case config.DiscoveryStatic:
		raw = append(raw, d.cfg.Discovery.Static...)
	case config.DiscoveryQuery:
		raw, err = queryDiscovery(ctx, db, d.cfg.Discovery.Query)
	case config.DiscoveryPattern:
		raw, err = patternDiscovery(ctx, db, d.cfg.Discovery.Pattern)
	default:
		return nil, fmt.Errorf("discovery: unsupported mode %q", d.cfg.Discovery.Mode)
	}
	if err != nil {
		return nil, err
	}

	filtered := applyExcludes(raw, d.cfg.Exclude)
	sort.Strings(filtered)
	d.cached = filtered
	d.done = true
	return d.cached, nil
}

func queryDiscovery(ctx context.Context, db Querier, sql string) ([]string, error) {
	rows, err := db.Query(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("discovery query: %w", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("discovery scan: %w", err)
		}
		out = append(out, name)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("discovery rows: %w", err)
	}
	return out, nil
}

func patternDiscovery(ctx context.Context, db Querier, pattern string) ([]string, error) {
	const sql = `select nspname from pg_namespace
	             where nspname ilike $1
	               and nspname not like 'pg\_%'
	               and nspname <> 'information_schema'`
	rows, err := db.Query(ctx, sql, pattern)
	if err != nil {
		return nil, fmt.Errorf("discovery pattern query: %w", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("discovery scan: %w", err)
		}
		out = append(out, name)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("discovery rows: %w", err)
	}
	return out, nil
}

// applyExcludes drops schemas matching any exclude entry. An entry containing
// '%' is treated as a SQL-style wildcard (translated to a glob); otherwise it
// is an exact match.
func applyExcludes(names, excludes []string) []string {
	if len(excludes) == 0 {
		return names
	}
	out := names[:0:0]
	for _, n := range names {
		if !excluded(n, excludes) {
			out = append(out, n)
		}
	}
	return out
}

func excluded(name string, excludes []string) bool {
	for _, ex := range excludes {
		if strings.Contains(ex, "%") {
			if ok, _ := path.Match(strings.ReplaceAll(ex, "%", "*"), name); ok {
				return true
			}
		} else if ex == name {
			return true
		}
	}
	return false
}

// Filter applies a --tenants glob (e.g. "tenant_1*") to an already-discovered
// list, enabling canary rollouts (R4.9). An empty glob returns the input
// unchanged. The match uses shell-style globbing.
func Filter(names []string, glob string) ([]string, error) {
	if glob == "" {
		return names, nil
	}
	// Validate the pattern once with a probe so a bad glob is reported clearly.
	if _, err := path.Match(glob, ""); err != nil {
		return nil, fmt.Errorf("invalid --tenants glob %q: %w", glob, err)
	}
	out := make([]string, 0, len(names))
	for _, n := range names {
		if ok, _ := path.Match(glob, n); ok {
			out = append(out, n)
		}
	}
	return out, nil
}

// PoolQuerier adapts a *pgxpool.Pool to the Querier interface. It exists so
// callers can pass a pool directly without a wrapper at every call site.
func PoolQuerier(p *pgxpool.Pool) Querier { return p }
