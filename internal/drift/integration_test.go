//go:build integration

// These tests run the real catalog queries against a live PostgreSQL. They are
// excluded from the default build and run only with:
//
//	go test -tags integration ./internal/drift/...
//
// pointing PGFLEET_TEST_DSN at a throwaway database. They cover spec test T3:
// an identical pair of schemas shows no drift, and a single mutation is flagged
// against the exact object.
package drift

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/NickKL05/pgfleet/internal/drift/diffgen"
)

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("PGFLEET_TEST_DSN")
	if dsn == "" {
		t.Skip("set PGFLEET_TEST_DSN to run integration tests")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

const canonicalDDL = `
create table %s.users (
    id           bigint generated always as identity primary key,
    email        text not null unique,
    display_name varchar(255) not null,
    created_at   timestamptz not null default now()
);
create index users_created_at_idx on %s.users (created_at);
`

func setupSchema(t *testing.T, pool *pgxpool.Pool, name string) {
	t.Helper()
	ctx := context.Background()
	_, _ = pool.Exec(ctx, "drop schema if exists "+name+" cascade")
	if _, err := pool.Exec(ctx, "create schema "+name); err != nil {
		t.Fatalf("create schema %s: %v", name, err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), "drop schema if exists "+name+" cascade") })

	ddl := strings.ReplaceAll(canonicalDDL, "%s", name)
	if _, err := pool.Exec(ctx, ddl); err != nil {
		t.Fatalf("apply ddl to %s: %v", name, err)
	}
}

func TestIntegrationVerifyNoDrift(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	setupSchema(t, pool, "it_template")
	setupSchema(t, pool, "it_tenant_a")

	ref, err := ReferenceFromSchema(ctx, pool, "it_template", Options{})
	if err != nil {
		t.Fatal(err)
	}
	rep, err := Verify(ctx, pool, []string{"it_tenant_a"}, ref, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Drifted() {
		t.Fatalf("expected no drift, got %+v", rep.Tenants)
	}
}

func TestIntegrationVerifyDetectsMutation(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	setupSchema(t, pool, "it_template")
	setupSchema(t, pool, "it_tenant_b")

	// Drop the index in the tenant only.
	if _, err := pool.Exec(ctx, "drop index it_tenant_b.users_created_at_idx"); err != nil {
		t.Fatal(err)
	}

	ref, err := ReferenceFromSchema(ctx, pool, "it_template", Options{})
	if err != nil {
		t.Fatal(err)
	}
	rep, err := Verify(ctx, pool, []string{"it_tenant_b"}, ref, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Drifted() {
		t.Fatal("expected drift to be detected")
	}
	found := false
	for _, d := range rep.Tenants[0].Differences {
		if d.Type == "index" && d.Name == "users_created_at_idx" && d.Class == "missing" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected missing index difference, got %+v", rep.Tenants[0].Differences)
	}
}

// TestIntegrationRepairConverges is spec test T3: apply the generated repair to
// a drifted tenant and confirm it converges to zero drift. It exercises all
// three difference classes at once: a missing index, a modified column, and an
// extra table.
func TestIntegrationRepairConverges(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	setupSchema(t, pool, "it_template")
	setupSchema(t, pool, "it_tenant_c")

	mutations := []string{
		"drop index it_tenant_c.users_created_at_idx",                                                             // missing index
		"alter table it_tenant_c.users alter column display_name type varchar(100)",                               // modified column
		"create table it_tenant_c.rogue (id bigint generated always as identity primary key, note text not null)", // extra table
	}
	for _, m := range mutations {
		if _, err := pool.Exec(ctx, m); err != nil {
			t.Fatalf("mutation %q: %v", m, err)
		}
	}

	opts := diffgen.RepairOptions{AllowDestructive: true}
	plans, err := Repair(ctx, pool, []string{"it_tenant_c"}, "it_template", opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 1 || !plans[0].HasWork() {
		t.Fatalf("expected a repair plan with work, got %+v", plans)
	}

	applyOpts := ApplyOptions{LockID: 743201, StatementTimeout: 30 * time.Second, LockTimeout: 5 * time.Second}
	if err := ApplyRepair(ctx, pool, plans[0], applyOpts); err != nil {
		t.Fatalf("apply repair: %v", err)
	}

	ref, err := ReferenceFromSchema(ctx, pool, "it_template", Options{})
	if err != nil {
		t.Fatal(err)
	}
	rep, err := Verify(ctx, pool, []string{"it_tenant_c"}, ref, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Drifted() {
		t.Fatalf("repair did not converge; remaining drift: %+v", rep.Tenants[0].Differences)
	}
}
