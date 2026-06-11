//go:build integration

package integration

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/NickKL05/pgfleet/internal/migrate"
	"github.com/NickKL05/pgfleet/internal/report"
)

const testLockID = int64(743201)

func upOptions(set *migrate.Set, concurrency int) migrate.Options {
	return migrate.Options{
		Set:              set,
		Table:            "_pgfleet_migrations",
		LockID:           testLockID,
		Concurrency:      concurrency,
		StatementTimeout: 60 * time.Second,
		LockTimeout:      5 * time.Second,
		Direction:        migrate.Up,
	}
}

// T1: 50 schemas, apply 5 migrations, verify all at version 5.
func TestT1_FiftySchemasFiveMigrations(t *testing.T) {
	ctx := context.Background()
	schemas := bulkCreateSchemas(t, "t1_", 50)
	set, err := migrate.Load(writeTableMigrations(t, 5))
	if err != nil {
		t.Fatal(err)
	}
	pool := newRunnerPool(t, 16)

	rep, err := migrate.NewRunner(pool, upOptions(set, 16)).Run(ctx, "t1", "migrate up", schemas)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Failed() {
		t.Fatalf("run reported failures: %v", rep.Summary)
	}
	for _, r := range rep.Tenants {
		if r.Status != report.StatusOK || r.To != 5 {
			t.Fatalf("tenant %s: status=%s to=%d, want ok at 5", r.Schema, r.Status, r.To)
		}
	}

	// Confirm via status that every tenant is up to date at version 5.
	st, err := migrate.Status(ctx, pool, schemas, set, "_pgfleet_migrations", 16)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range st.Tenants {
		if r.Status != migrate.StatusUpToDate || r.To != 5 {
			t.Fatalf("status %s: %s at %d, want up-to-date at 5", r.Schema, r.Status, r.To)
		}
	}
}

// T2: inject a failure in one tenant, assert isolation, then resume succeeds.
func TestT2_FailureIsolationAndResume(t *testing.T) {
	ctx := context.Background()
	schemas := bulkCreateSchemas(t, "t2_", 5)
	bad := schemas[2]

	dir := t.TempDir()
	writeMigration(t, dir, 1, "a", "create table a (id int);", "drop table a;")
	writeMigration(t, dir, 2, "b", "create table b (id int);", "drop table b;")
	// Pre-create table b in the bad tenant so migration 0002 fails there only.
	mustExec(t, fmt.Sprintf("create table %s.b (id int)", bad))

	set, err := migrate.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	pool := newRunnerPool(t, 8)

	rep, err := migrate.NewRunner(pool, upOptions(set, 8)).Run(ctx, "t2", "migrate up", schemas)
	if err != nil {
		t.Fatal(err)
	}
	ok, failed := 0, 0
	for _, r := range rep.Tenants {
		switch {
		case r.Schema == bad:
			if r.Status != report.StatusFailed {
				t.Fatalf("bad tenant should have failed, got %s", r.Status)
			}
			failed++
		default:
			if r.Status != report.StatusOK || r.To != 2 {
				t.Fatalf("healthy tenant %s: %s at %d, want ok at 2", r.Schema, r.Status, r.To)
			}
			ok++
		}
	}
	if ok != 4 || failed != 1 {
		t.Fatalf("isolation broken: ok=%d failed=%d, want 4/1", ok, failed)
	}

	// Resolve the conflict and resume; the failed tenant should converge.
	mustExec(t, fmt.Sprintf("drop table %s.b", bad))
	if _, err := migrate.NewRunner(pool, upOptions(set, 8)).Run(ctx, "t2b", "migrate up", schemas); err != nil {
		t.Fatal(err)
	}
	st, err := migrate.Status(ctx, pool, schemas, set, "_pgfleet_migrations", 8)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range st.Tenants {
		if r.To != 2 || r.Status != migrate.StatusUpToDate {
			t.Fatalf("after resume %s: %s at %d, want up-to-date at 2", r.Schema, r.Status, r.To)
		}
	}
}

// T4: a migration file changed after apply is detected as checksum-mismatch.
func TestT4_ChecksumMismatch(t *testing.T) {
	ctx := context.Background()
	schemas := bulkCreateSchemas(t, "t4_", 1)

	dir := t.TempDir()
	writeMigration(t, dir, 1, "a", "create table a (id int);", "drop table a;")
	set1, err := migrate.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	pool := newRunnerPool(t, 4)
	if _, err := migrate.NewRunner(pool, upOptions(set1, 4)).Run(ctx, "t4", "migrate up", schemas); err != nil {
		t.Fatal(err)
	}

	// Change the file content so its checksum no longer matches what was applied.
	writeMigration(t, dir, 1, "a", "create table a (id int); -- edited", "drop table a;")
	set2, err := migrate.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	rep, err := migrate.NewRunner(pool, upOptions(set2, 4)).Run(ctx, "t4b", "migrate up", schemas)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Tenants[0].Status != report.StatusChecksumMismatch {
		t.Fatalf("expected checksum-mismatch, got %s", rep.Tenants[0].Status)
	}

	// --allow-dirty bypasses the guard.
	opts := upOptions(set2, 4)
	opts.AllowDirty = true
	rep2, err := migrate.NewRunner(pool, opts).Run(ctx, "t4c", "migrate up", schemas)
	if err != nil {
		t.Fatal(err)
	}
	if rep2.Tenants[0].Status == report.StatusChecksumMismatch {
		t.Fatalf("--allow-dirty should bypass the checksum guard, got %s", rep2.Tenants[0].Status)
	}
}

// T5: a tenant whose advisory lock is held elsewhere reports "locked".
func TestT5_AdvisoryLockContention(t *testing.T) {
	ctx := context.Background()
	schema := bulkCreateSchemas(t, "t5_", 1)[0]
	set, err := migrate.Load(writeTableMigrations(t, 1))
	if err != nil {
		t.Fatal(err)
	}

	// Hold the per-tenant advisory lock on a separate session for the duration.
	holder, err := testPool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := holder.Exec(ctx, "select pg_advisory_lock($1, hashtext($2))", testLockID, schema); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = holder.Exec(context.Background(), "select pg_advisory_unlock($1, hashtext($2))", testLockID, schema)
		holder.Release()
	}()

	pool := newRunnerPool(t, 4)
	rep, err := migrate.NewRunner(pool, upOptions(set, 4)).Run(ctx, "t5", "migrate up", []string{schema})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Tenants[0].Status != report.StatusLocked {
		t.Fatalf("expected locked, got %s", rep.Tenants[0].Status)
	}
}
