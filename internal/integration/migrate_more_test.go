//go:build integration

package integration

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/NickKL05/pgfleet/internal/migrate"
	"github.com/NickKL05/pgfleet/internal/report"
)

func tableExists(t *testing.T, schema, table string) bool {
	t.Helper()
	var exists bool
	err := testPool.QueryRow(context.Background(),
		"select exists (select 1 from pg_tables where schemaname=$1 and tablename=$2)", schema, table).Scan(&exists)
	if err != nil {
		t.Fatal(err)
	}
	return exists
}

func TestMigrateDown(t *testing.T) {
	ctx := context.Background()
	schema := bulkCreateSchemas(t, "down_", 1)[0]
	set, err := migrate.Load(writeTableMigrations(t, 3))
	if err != nil {
		t.Fatal(err)
	}
	pool := newRunnerPool(t, 4)

	if _, err := migrate.NewRunner(pool, upOptions(set, 4)).Run(ctx, "u", "migrate up", []string{schema}); err != nil {
		t.Fatal(err)
	}

	down := upOptions(set, 4)
	down.Direction = migrate.Down
	down.Target = 1
	rep, err := migrate.NewRunner(pool, down).Run(ctx, "d", "migrate down", []string{schema})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Tenants[0].Status != report.StatusOK || rep.Tenants[0].To != 1 {
		t.Fatalf("down result: %s at %d, want ok at 1", rep.Tenants[0].Status, rep.Tenants[0].To)
	}
	if !tableExists(t, schema, "t1") {
		t.Error("t1 should remain after down to 1")
	}
	if tableExists(t, schema, "t2") || tableExists(t, schema, "t3") {
		t.Error("t2 and t3 should have been rolled back")
	}
}

func TestMigrateDryRun(t *testing.T) {
	ctx := context.Background()
	schema := bulkCreateSchemas(t, "dry_", 1)[0]
	set, err := migrate.Load(writeTableMigrations(t, 2))
	if err != nil {
		t.Fatal(err)
	}
	pool := newRunnerPool(t, 4)

	var buf bytes.Buffer
	opts := upOptions(set, 4)
	opts.DryRun = true
	opts.DryRunOut = &buf
	if _, err := migrate.NewRunner(pool, opts).Run(ctx, "dr", "migrate up", []string{schema}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "create table t1") {
		t.Errorf("dry-run output should contain the SQL, got:\n%s", buf.String())
	}
	// Nothing was actually applied.
	if tableExists(t, schema, "t1") {
		t.Error("dry-run must not create tables")
	}
	st, err := migrate.Status(ctx, pool, []string{schema}, set, "_pgfleet_migrations", 4)
	if err != nil {
		t.Fatal(err)
	}
	if st.Tenants[0].To != 0 {
		t.Errorf("dry-run must not advance version, got %d", st.Tenants[0].To)
	}
}

func TestMigrateNoTransaction(t *testing.T) {
	ctx := context.Background()
	schema := bulkCreateSchemas(t, "notx_", 1)[0]
	dir := t.TempDir()
	writeMigration(t, dir, 1, "nt", "-- pgfleet:no-transaction\ncreate table nt (id int);", "drop table nt;")
	set, err := migrate.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !set.All()[0].NoTransaction {
		t.Fatal("migration should be marked no-transaction")
	}
	pool := newRunnerPool(t, 4)
	rep, err := migrate.NewRunner(pool, upOptions(set, 4)).Run(ctx, "nt", "migrate up", []string{schema})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Tenants[0].Status != report.StatusOK || rep.Tenants[0].To != 1 {
		t.Fatalf("no-transaction result: %s at %d, want ok at 1", rep.Tenants[0].Status, rep.Tenants[0].To)
	}
	if !tableExists(t, schema, "nt") {
		t.Error("no-transaction migration should have created the table")
	}
}
