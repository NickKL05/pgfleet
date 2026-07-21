// Package web serves the read-only fleet dashboard: a JSON API over the same
// migration and drift functions the CLI commands call, plus the single-page app
// embedded in the binary. It has no write paths, so nothing it exposes can
// change a database (R: dashboard is observability only).
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

// Defaults applied when the corresponding Option is left zero. Each may be
// switched off explicitly with a negative value.
const (
	defaultMinRefreshInterval = time.Second
	defaultRateLimit          = 10 // requests per second, per client
	defaultRateBurst          = 30
)

// Options configures a Server.
type Options struct {
	Addr     string        // listen address, e.g. ":8080"
	CacheTTL time.Duration // fleet-query cache window; 0 disables caching
	Logger   *slog.Logger  // request/lifecycle logging; nil discards

	// MinRefreshInterval floors how often ?refresh=1 may force a fresh fleet
	// query. Without it the cache is trivially bypassed by an unauthenticated
	// caller, turning every request into a full scan of every tenant schema.
	// Zero applies defaultMinRefreshInterval; negative removes the floor.
	MinRefreshInterval time.Duration

	// RateLimit is the sustained per-client request rate for /api/*, and
	// RateBurst the tolerated burst. Zero applies the defaults; a negative
	// RateLimit disables rate limiting.
	RateLimit float64
	RateBurst float64
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

	// limiter throttles /api/* per client; nil when rate limiting is disabled.
	limiter *rateLimiter
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

	minRefresh := opts.MinRefreshInterval
	switch {
	case minRefresh == 0:
		minRefresh = defaultMinRefreshInterval
	case minRefresh < 0:
		minRefresh = 0 // explicitly disabled
	}

	rate, burst := opts.RateLimit, opts.RateBurst
	if rate == 0 {
		rate = defaultRateLimit
	}
	if burst <= 0 {
		burst = defaultRateBurst
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
		cache:    newFleetCache(provider, opts.CacheTTL, minRefresh),
	}
	if rate > 0 {
		s.limiter = newRateLimiter(rate, burst)
	}
	s.routes(spa)
	return s, nil
}

// Handler exposes the router for tests and for embedding in another mux.
func (s *Server) Handler() http.Handler { return s.mux }

func (s *Server) routes(spa fs.FS) {
	s.mux.Handle("GET /api/summary", s.api(s.handleSummary))
	s.mux.Handle("GET /api/tenants", s.api(s.handleTenants))
	s.mux.Handle("GET /api/drift", s.api(s.handleDrift))
	s.mux.Handle("GET /api/drift/{tenant}", s.api(s.handleTenantDrift))
	s.mux.Handle("GET /api/versions", s.api(s.handleVersions))
	// Everything else is the SPA (index.html with a client-side-routing
	// fallback for deep links like /tenant/tenant_042). Static assets are served
	// from memory and are not rate limited.
	s.mux.Handle("/", spaHandler(spa))
}

// api wraps a database-backed endpoint with per-client rate limiting. Rejections
// are intentionally not logged: under the flood this exists to absorb, a log
// line per rejected request is its own denial of service.
func (s *Server) api(h http.HandlerFunc) http.Handler {
	if s.limiter == nil {
		return h
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.limiter.allow(clientIP(r)) {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":"Too Many Requests"}` + "\n"))
			return
		}
		h(w, r)
	})
}

// ListenAndServe runs the HTTP server until ctx is canceled, then shuts down
// gracefully. Canceling ctx (e.g. on SIGINT) returns nil, not an error.
func (s *Server) ListenAndServe(ctx context.Context) error {
	srv := &http.Server{
		Addr:              s.opts.Addr,
		Handler:           s.mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       15 * time.Second,
		// WriteTimeout spans handler execution, so it has to exceed the slowest
		// legitimate response. That is a fleet-wide query, itself bounded by the
		// database statement_timeout (60s by default), so this leaves headroom
		// without letting a stalled response hold a connection indefinitely.
		WriteTimeout: 75 * time.Second,
		IdleTimeout:  120 * time.Second,
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
	// minRefresh floors how often a forced refresh may reach the database.
	// Zero removes the floor.
	minRefresh time.Duration

	mu        sync.Mutex
	migration cachedReport[*report.RunReport]
	drift     cachedReport[*report.DriftReport]
}

type cachedReport[T any] struct {
	value     T
	fetchedAt time.Time
	ok        bool
}

func newFleetCache(provider Provider, ttl, minRefresh time.Duration) *fleetCache {
	return &fleetCache{provider: provider, ttl: ttl, minRefresh: minRefresh}
}

// serveCached reports whether a cached entry satisfies this request. An ordinary
// request is satisfied for as long as the entry is within the TTL. A forced
// refresh is satisfied only while the entry is newer than minRefresh, which is
// what stops an unauthenticated ?refresh=1 from making every request a full
// scan of every tenant schema.
func (c *fleetCache) serveCached(at time.Time, ok, refresh bool) bool {
	if !ok {
		return false
	}
	if refresh {
		return c.minRefresh > 0 && time.Since(at) < c.minRefresh
	}
	return c.ttl > 0 && time.Since(at) < c.ttl
}

func (c *fleetCache) migrationStatus(ctx context.Context, refresh bool) (*report.RunReport, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.serveCached(c.migration.fetchedAt, c.migration.ok, refresh) {
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
	if c.serveCached(c.drift.fetchedAt, c.drift.ok, refresh) {
		return c.drift.value, nil
	}
	rep, err := c.provider.DriftStatus(ctx)
	if err != nil {
		return nil, err
	}
	c.drift = cachedReport[*report.DriftReport]{value: rep, fetchedAt: time.Now(), ok: true}
	return rep, nil
}
