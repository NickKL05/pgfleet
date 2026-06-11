//go:build integration

package integration

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/NickKL05/pgfleet/internal/drift"
	"github.com/NickKL05/pgfleet/internal/migrate"
)

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}

// T6: a 1000-schema synthetic database migrates at concurrency 32 without pool
// exhaustion, and drift verify completes within the time budget.
func TestT6_ScaleMigrateAndVerify(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping scale test in -short mode")
	}
	ctx := context.Background()

	n := envInt("PGFLEET_PERF_SCHEMAS", 1000)
	budget := envDuration("PGFLEET_VERIFY_BUDGET", 15*time.Second)
	const concurrency = 32

	tenants := bulkCreateSchemas(t, "perf_", n)
	template := bulkCreateSchemas(t, "perftpl_", 1)[0]

	set, err := migrate.Load(writeTableMigrations(t, 1))
	if err != nil {
		t.Fatal(err)
	}
	pool := newRunnerPool(t, concurrency)

	// Migrate the template alongside the tenants so it is a valid reference.
	all := append(append([]string{}, tenants...), template)
	rep, err := migrate.NewRunner(pool, upOptions(set, concurrency)).Run(ctx, "t6", "migrate up", all)
	if err != nil {
		t.Fatalf("migrate at concurrency %d failed (possible pool exhaustion): %v", concurrency, err)
	}
	if rep.Failed() {
		t.Fatalf("migrate reported failures: %v", rep.Summary)
	}

	ref, err := drift.ReferenceFromSchema(ctx, testPool, template, drift.Options{})
	if err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	vrep, err := drift.Verify(ctx, testPool, tenants, ref, drift.Options{})
	if err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(start)

	if vrep.Drifted() {
		t.Fatalf("unexpected drift across %d freshly migrated tenants: %v", n, vrep.Summary)
	}
	t.Logf("verified %d schemas in %s (budget %s)", n, elapsed.Round(time.Millisecond), budget)
	if elapsed > budget {
		t.Fatalf("verify of %d schemas took %s, over budget %s", n, elapsed, budget)
	}
}
