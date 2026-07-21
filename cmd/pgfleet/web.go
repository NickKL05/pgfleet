package main

import (
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/NickKL05/pgfleet/internal/migrate"
	"github.com/NickKL05/pgfleet/internal/web"
)

// newWebCmd serves the read-only fleet dashboard: a JSON API over the same
// migrate/drift functions the CLI uses, plus the embedded single-page UI. It is
// an optional convenience; the CLI stays the primary interface (R: read-only).
func newWebCmd(gf *globalFlags) *cobra.Command {
	var addr string
	var cacheTTL, minRefresh time.Duration
	var rateLimit, rateBurst float64
	cmd := &cobra.Command{
		Use:   "web",
		Short: "Serve the read-only fleet dashboard (HTTP API + embedded UI)",
		Long: "Serve a read-only web dashboard that visualizes migration status and\n" +
			"schema drift across the fleet. The API reuses the same functions as the\n" +
			"CLI (migrate status, drift verify/diff); it never mutates any database.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Own signal handling so Ctrl+C shuts the HTTP server down cleanly
			// rather than tearing the pool out from under in-flight requests.
			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			a, err := loadApp(gf)
			if err != nil {
				return err
			}
			set, err := migrate.Load(a.cfg.Migrations.Dir)
			if err != nil {
				return usageErr(err)
			}
			if err := a.connect(ctx); err != nil {
				return err
			}
			defer a.close()

			tenants, err := a.tenants(ctx)
			if err != nil {
				return err
			}

			fleet := web.NewFleet(a.pool, a.cfg, set, tenants)
			srv, err := web.NewServer(fleet, web.Options{
				Addr:               addr,
				CacheTTL:           cacheTTL,
				MinRefreshInterval: minRefresh,
				RateLimit:          rateLimit,
				RateBurst:          rateBurst,
				Logger:             a.logger,
			})
			if err != nil {
				return failureErr(err)
			}
			if err := srv.ListenAndServe(ctx); err != nil {
				return failureErr(err)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&addr, "addr", ":8080", "address to listen on")
	cmd.Flags().DurationVar(&cacheTTL, "cache-ttl", 3*time.Second, "cache window for fleet queries (0 disables)")
	// The dashboard has no authentication, so the endpoints that reach the
	// database are throttled by default. Both guards can be turned off with a
	// negative value when serving on a trusted network.
	cmd.Flags().DurationVar(&minRefresh, "min-refresh", time.Second,
		"minimum interval between ?refresh=1 fleet queries (negative disables)")
	cmd.Flags().Float64Var(&rateLimit, "rate-limit", 10,
		"per-client requests per second for /api/* (negative disables)")
	cmd.Flags().Float64Var(&rateBurst, "rate-burst", 30,
		"per-client burst allowance for /api/*")
	return cmd
}
