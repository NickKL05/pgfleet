package web

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/NickKL05/pgfleet/internal/report"
)

// Options configures a Server.
type Options struct {
	Addr     string        // listen address, e.g. ":8080"
	CacheTTL time.Duration // fleet-query cache window; 0 disables caching
	Logger   *slog.Logger  // request/lifecycle logging; nil discards
}

// Server serves the read-only dashboard: a JSON API backed by a Provider and
// the embedded single-page app. It is safe for concurrent requests.
type Server struct {
	provider Provider
	opts     Options
	logger   *slog.Logger
	mux      *http.ServeMux

	// cache memoizes the two fleet-wide queries (migration status, drift verify)
	// for CacheTTL, so a page load that fires several component mounts against
	// 250 tenants hits the database once rather than once per endpoint.
	cache *fleetCache
}

// NewServer builds a Server. It returns an error only if the embedded assets
// cannot be mounted, which indicates a broken build.
func NewServer(provider Provider, opts Options) (*Server, error) {
	logger := opts.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	if opts.Addr == "" {
		opts.Addr = ":8080"
	}

	spa, err := assets()
	if err != nil {
		return nil, fmt.Errorf("mount embedded assets: %w", err)
	}

	s := &Server{
		provider: provider,
		opts:     opts,
		logger:   logger,
		mux:      http.NewServeMux(),
		cache:    newFleetCache(provider, opts.CacheTTL),
	}
	s.routes(spa)
	return s, nil
}

// Handler exposes the router for tests and for embedding in another mux.
func (s *Server) Handler() http.Handler { return s.mux }

func (s *Server) routes(spa fs.FS) {
	s.mux.HandleFunc("GET /api/summary", s.handleSummary)
	s.mux.HandleFunc("GET /api/tenants", s.handleTenants)
	s.mux.HandleFunc("GET /api/drift", s.handleDrift)
	s.mux.HandleFunc("GET /api/drift/{tenant}", s.handleTenantDrift)
	s.mux.HandleFunc("GET /api/versions", s.handleVersions)
	// Everything else is the SPA (index.html with a client-side-routing
	// fallback for deep links like /tenant/tenant_042).
	s.mux.Handle("/", spaHandler(spa))
}

// ListenAndServe runs the HTTP server until ctx is canceled, then shuts down
// gracefully. Canceling ctx (e.g. on SIGINT) returns nil, not an error.
func (s *Server) ListenAndServe(ctx context.Context) error {
	srv := &http.Server{
		Addr:              s.opts.Addr,
		Handler:           s.mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		s.logger.Info("dashboard listening",
			"addr", s.opts.Addr,
			"tenants", len(s.provider.Tenants()),
			"cache_ttl", s.opts.CacheTTL)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		s.logger.Info("shutting down")
		return srv.Shutdown(shutdownCtx)
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// fleetCache memoizes the two expensive fleet-wide reports behind a short TTL.
// A per-report singleflight-style guard is unnecessary here: the demo scale and
// TTL make an occasional duplicate query harmless, and the mutex keeps reads
// consistent.
type fleetCache struct {
	provider Provider
	ttl      time.Duration

	mu        sync.Mutex
	migration cachedReport[*report.RunReport]
	drift     cachedReport[*report.DriftReport]
}

type cachedReport[T any] struct {
	value     T
	fetchedAt time.Time
	ok        bool
}

func newFleetCache(provider Provider, ttl time.Duration) *fleetCache {
	return &fleetCache{provider: provider, ttl: ttl}
}

// fresh reports whether a cached entry is still within the TTL.
func (c *fleetCache) fresh(at time.Time, ok bool) bool {
	return ok && c.ttl > 0 && time.Since(at) < c.ttl
}

func (c *fleetCache) migrationStatus(ctx context.Context, refresh bool) (*report.RunReport, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !refresh && c.fresh(c.migration.fetchedAt, c.migration.ok) {
		return c.migration.value, nil
	}
	rep, err := c.provider.MigrationStatus(ctx)
	if err != nil {
		return nil, err
	}
	c.migration = cachedReport[*report.RunReport]{value: rep, fetchedAt: time.Now(), ok: true}
	return rep, nil
}

func (c *fleetCache) driftStatus(ctx context.Context, refresh bool) (*report.DriftReport, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !refresh && c.fresh(c.drift.fetchedAt, c.drift.ok) {
		return c.drift.value, nil
	}
	rep, err := c.provider.DriftStatus(ctx)
	if err != nil {
		return nil, err
	}
	c.drift = cachedReport[*report.DriftReport]{value: rep, fetchedAt: time.Now(), ok: true}
	return rep, nil
}
