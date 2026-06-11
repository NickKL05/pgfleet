package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"

	"github.com/NickKL05/pgfleet/internal/config"
	"github.com/NickKL05/pgfleet/internal/discovery"
	"github.com/NickKL05/pgfleet/internal/pgutil"
)

// version is overridden at build time by goreleaser via -ldflags.
var version = "dev"

// globalFlags hold options shared by every command.
type globalFlags struct {
	configPath string
	tenants    string
	json       bool
	logFormat  string
}

func newRootCmd() *cobra.Command {
	gf := &globalFlags{}

	root := &cobra.Command{
		Use:           "pgfleet",
		Short:         "Multi-tenant PostgreSQL migration and drift toolkit",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	pf := root.PersistentFlags()
	pf.StringVar(&gf.configPath, "config", config.DefaultPath, "path to pgfleet.yaml")
	pf.StringVar(&gf.tenants, "tenants", "", "glob to limit tenants (e.g. 'tenant_1*')")
	pf.BoolVar(&gf.json, "json", false, "emit machine-readable JSON")
	pf.StringVar(&gf.logFormat, "log-format", "text", "log handler: text or json")

	root.AddCommand(newMigrateCmd(gf))
	root.AddCommand(newDriftCmd(gf))
	return root
}

// app bundles the resolved config, logger, and a lazily-built pool. It is the
// shared entry point every command uses to reach the database and the tenant
// list, so discovery happens exactly once per invocation (R6.1).
type app struct {
	cfg        *config.Config
	logger     *slog.Logger
	gf         *globalFlags
	pool       *pgxpool.Pool
	discoverer *discovery.Discoverer
}

// loadApp reads and validates config and builds the logger. It does not connect.
func loadApp(gf *globalFlags) (*app, error) {
	cfg, err := config.Load(gf.configPath)
	if err != nil {
		return nil, usageErr(err)
	}
	return &app{
		cfg:        cfg,
		logger:     newLogger(gf.logFormat),
		gf:         gf,
		discoverer: discovery.New(cfg.Tenants),
	}, nil
}

func newLogger(format string) *slog.Logger {
	var h slog.Handler
	opts := &slog.HandlerOptions{Level: slog.LevelInfo}
	if format == "json" {
		h = slog.NewJSONHandler(os.Stderr, opts)
	} else {
		h = slog.NewTextHandler(os.Stderr, opts)
	}
	return slog.New(h)
}

// connect builds the connection pool, capping connections at concurrency+2 so
// the live connection count never exceeds that bound (R4.11).
func (a *app) connect(ctx context.Context) error {
	dsn, err := a.cfg.ResolveDSN()
	if err != nil {
		return connErr(err)
	}
	pool, err := pgutil.NewPool(ctx, pgutil.PoolConfig{
		DSN:              dsn,
		MaxConns:         int32(a.cfg.Run.Concurrency + 2),
		StatementTimeout: a.cfg.Run.StatementTimeout.Std(),
		LockTimeout:      a.cfg.Run.LockTimeout.Std(),
	})
	if err != nil {
		return connErr(err)
	}
	a.pool = pool
	return nil
}

func (a *app) close() {
	if a.pool != nil {
		a.pool.Close()
	}
}

// tenants discovers the tenant list and applies the --tenants glob (R4.9, R6.2).
func (a *app) tenants(ctx context.Context) ([]string, error) {
	all, err := a.discoverer.Tenants(ctx, a.pool)
	if err != nil {
		return nil, connErr(err)
	}
	filtered, err := discovery.Filter(all, a.gf.tenants)
	if err != nil {
		return nil, usageErr(err)
	}
	return filtered, nil
}
