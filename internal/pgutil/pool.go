// Package pgutil holds the shared database plumbing: the connection pool,
// per-connection search_path handling, and statement/lock timeout helpers.
package pgutil

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PoolConfig parameterizes pool construction.
type PoolConfig struct {
	DSN string
	// MaxConns caps open connections. The runner sets this to concurrency+2 so
	// the live connection count never exceeds that bound (R4.11).
	MaxConns int32
	// StatementTimeout and LockTimeout are applied per connection as session
	// defaults; per-transaction SET LOCAL still overrides them where needed.
	StatementTimeout time.Duration
	LockTimeout      time.Duration
}

// NewPool builds a pgxpool with the given caps and timeouts.
func NewPool(ctx context.Context, cfg PoolConfig) (*pgxpool.Pool, error) {
	pcfg, err := pgxpool.ParseConfig(cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("parse dsn: %w", err)
	}
	if cfg.MaxConns > 0 {
		pcfg.MaxConns = cfg.MaxConns
	}

	stmt := int64(cfg.StatementTimeout / time.Millisecond)
	lock := int64(cfg.LockTimeout / time.Millisecond)
	pcfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		if stmt > 0 {
			if _, err := conn.Exec(ctx, fmt.Sprintf("set statement_timeout = %d", stmt)); err != nil {
				return err
			}
		}
		if lock > 0 {
			if _, err := conn.Exec(ctx, fmt.Sprintf("set lock_timeout = %d", lock)); err != nil {
				return err
			}
		}
		return nil
	}

	pool, err := pgxpool.NewWithConfig(ctx, pcfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}
	return pool, nil
}
