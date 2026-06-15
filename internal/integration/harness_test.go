//go:build integration

package integration

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/NickKL05/pgfleet/internal/pgutil"
)

// Package-level handles shared by every test, set up once in TestMain.
var (
	testDSN  string
	testPool *pgxpool.Pool
)

// TestMain starts one PostgreSQL container for the whole package (or reuses an
// external database when PGFLEET_TEST_DSN is set) and shares a pool. The image
// is taken from PGFLEET_PG_IMAGE so CI can run the 15/16/17 matrix.
func TestMain(m *testing.M) {
	ctx := context.Background()

	var cleanup func()
	dsn := os.Getenv("PGFLEET_TEST_DSN")
	if dsn == "" {
		img := os.Getenv("PGFLEET_PG_IMAGE")
		if img == "" {
			img = "postgres:17"
		}
		ctr, err := postgres.Run(ctx, img,
			postgres.WithDatabase("fleet"),
			postgres.WithUsername("pgfleet"),
			postgres.WithPassword("pgfleet"),
			testcontainers.WithWaitStrategy(
				wait.ForLog("database system is ready to accept connections").
					WithOccurrence(2).WithStartupTimeout(120*time.Second),
			),
		)
		if err != nil {
			fmt.Fprintf(os.Stderr, "start postgres container: %v\n", err)
			os.Exit(1)
		}
		dsn, err = ctr.ConnectionString(ctx, "sslmode=disable")
		if err != nil {
			fmt.Fprintf(os.Stderr, "connection string: %v\n", err)
			os.Exit(1)
		}
		cleanup = func() { _ = testcontainers.TerminateContainer(ctr) }
	}
	testDSN = dsn

	pool, err := connectWithRetry(ctx, dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect: %v\n", err)
		if cleanup != nil {
			cleanup()
		}
		os.Exit(1)
	}
	testPool = pool

	code := m.Run()

	pool.Close()
	if cleanup != nil {
		cleanup()
	}
	os.Exit(code)
}

func connectWithRetry(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	var lastErr error
	for i := 0; i < 30; i++ {
		pool, err := pgxpool.New(ctx, dsn)
		if err == nil {
			if pingErr := pool.Ping(ctx); pingErr == nil {
				return pool, nil
			}
			pool.Close()
		} else {
			lastErr = err
		}
		time.Sleep(time.Second)
	}
	return nil, lastErr
}

// mustExec runs a statement on the shared pool and fails the test on error.
func mustExec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

// bulkCreateSchemas creates n schemas named prefix0000..prefixNNNN in a single
// round trip and registers cleanup that drops them. DO blocks cannot take bind
// parameters, so the statements are built in Go (the names are test-controlled
// and quoted).
func bulkCreateSchemas(t *testing.T, prefix string, n int) []string {
	t.Helper()
	names := make([]string, n)
	var create strings.Builder
	for i := range names {
		names[i] = fmt.Sprintf("%s%04d", prefix, i)
		fmt.Fprintf(&create, "create schema %s;", pgutil.QuoteIdent(names[i]))
	}
	mustExec(t, create.String())

	t.Cleanup(func() {
		var drop strings.Builder
		for _, name := range names {
			fmt.Fprintf(&drop, "drop schema if exists %s cascade;", pgutil.QuoteIdent(name))
		}
		_, _ = testPool.Exec(context.Background(), drop.String())
	})

	return names
}

// applyUsersTable creates the canonical users table and its index in a schema.
func applyUsersTable(t *testing.T, schema string) {
	t.Helper()
	mustExec(t, fmt.Sprintf(`create table %s.users (
		id           bigint generated always as identity primary key,
		email        text not null unique,
		display_name varchar(255) not null,
		created_at   timestamptz not null default now()
	)`, schema))
	mustExec(t, fmt.Sprintf("create index users_created_at_idx on %s.users (created_at)", schema))
}

// writeMigration writes an up/down pair into dir.
func writeMigration(t *testing.T, dir string, version int, name, up, down string) {
	t.Helper()
	base := fmt.Sprintf("%04d_%s", version, name)
	if err := os.WriteFile(filepath.Join(dir, base+".up.sql"), []byte(up), 0o600); err != nil {
		t.Fatal(err)
	}
	if down != "" {
		if err := os.WriteFile(filepath.Join(dir, base+".down.sql"), []byte(down), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// writeTableMigrations writes n migrations, each creating a distinct table tN.
func writeTableMigrations(t *testing.T, n int) string {
	t.Helper()
	dir := t.TempDir()
	for i := 1; i <= n; i++ {
		writeMigration(t, dir, i, fmt.Sprintf("table_t%d", i),
			fmt.Sprintf("create table t%d (id int);", i),
			fmt.Sprintf("drop table t%d;", i))
	}
	return dir
}

// newRunnerPool builds a dedicated pool sized for the given concurrency, the
// way the CLI does (MaxConns = concurrency + 2).
func newRunnerPool(t *testing.T, concurrency int) *pgxpool.Pool {
	t.Helper()
	pool, err := pgutil.NewPool(context.Background(), pgutil.PoolConfig{
		DSN:              testDSN,
		MaxConns:         int32(concurrency + 2),
		StatementTimeout: 60 * time.Second,
		LockTimeout:      5 * time.Second,
	})
	if err != nil {
		t.Fatalf("build pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}
