package migrate

import (
	"context"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5"

	"github.com/NickKL05/pgfleet/internal/pgutil"
)

// AppliedRecord is one row of a tenant's state table.
type AppliedRecord struct {
	Version  int
	Name     string
	Checksum string
}

// stateTable returns the schema-qualified, quoted state table name.
func stateTable(schema, table string) string {
	return pgutil.QuoteIdent(schema) + "." + pgutil.QuoteIdent(table)
}

// EnsureStateTable creates the per-tenant migration state table if it does not
// already exist (spec 4.2). The connection must be able to write to schema.
func EnsureStateTable(ctx context.Context, conn pgutil.Execer, schema, table string) error {
	sql := fmt.Sprintf(`create table if not exists %s (
		version int primary key,
		name text not null,
		checksum text not null,
		applied_at timestamptz not null default now(),
		duration_ms int not null default 0
	)`, stateTable(schema, table))
	if _, err := conn.Exec(ctx, sql); err != nil {
		return fmt.Errorf("ensure state table for %s: %w", schema, err)
	}
	return nil
}

// querier is the read surface used to load applied records.
type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// LoadApplied returns the applied migrations keyed by version, ordered is not
// guaranteed (callers index by version).
func LoadApplied(ctx context.Context, conn querier, schema, table string) (map[int]AppliedRecord, error) {
	sql := fmt.Sprintf(`select version, name, checksum from %s order by version`, stateTable(schema, table))
	rows, err := conn.Query(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("load applied for %s: %w", schema, err)
	}
	defer rows.Close()

	out := map[int]AppliedRecord{}
	for rows.Next() {
		var r AppliedRecord
		if err := rows.Scan(&r.Version, &r.Name, &r.Checksum); err != nil {
			return nil, fmt.Errorf("scan applied for %s: %w", schema, err)
		}
		out[r.Version] = r
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// HighestApplied returns the greatest applied version, or 0 when none.
func HighestApplied(applied map[int]AppliedRecord) int {
	highest := 0
	for v := range applied {
		if v > highest {
			highest = v
		}
	}
	return highest
}

// recordApply inserts a state row after a successful up migration.
func recordApply(ctx context.Context, conn pgutil.Execer, schema, table string, m Migration, durationMS int) error {
	sql := fmt.Sprintf(`insert into %s (version, name, checksum, applied_at, duration_ms)
		values ($1, $2, $3, now(), $4)`, stateTable(schema, table))
	if _, err := conn.Exec(ctx, sql, m.Version, m.Name, m.UpChecksum, durationMS); err != nil {
		return fmt.Errorf("record apply v%d for %s: %w", m.Version, schema, err)
	}
	return nil
}

// recordRemove deletes a state row after a successful down migration.
func recordRemove(ctx context.Context, conn pgutil.Execer, schema, table string, version int) error {
	sql := fmt.Sprintf(`delete from %s where version = $1`, stateTable(schema, table))
	if _, err := conn.Exec(ctx, sql, version); err != nil {
		return fmt.Errorf("record remove v%d for %s: %w", version, schema, err)
	}
	return nil
}

// checkChecksums verifies that every applied migration still matches the file
// on disk. It returns the first mismatching version, or 0 when all match (R4.1).
func checkChecksums(applied map[int]AppliedRecord, set *Set) int {
	byVersion := map[int]Migration{}
	for _, m := range set.All() {
		byVersion[m.Version] = m
	}
	versions := make([]int, 0, len(applied))
	for v := range applied {
		versions = append(versions, v)
	}
	sort.Ints(versions)
	for _, v := range versions {
		if m, ok := byVersion[v]; ok && m.UpChecksum != applied[v].Checksum {
			return v
		}
	}
	return 0
}
