package pgutil

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

// Execer is the subset of the pgx surface this package needs. *pgx.Conn,
// *pgxpool.Conn, and pgx.Tx all satisfy it.
type Execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// QuoteIdent quotes a PostgreSQL identifier, doubling any embedded quotes. Used
// for schema names that arrive from discovery and are therefore untrusted from
// the catalog's perspective.
func QuoteIdent(ident string) string {
	return `"` + strings.ReplaceAll(ident, `"`, `""`) + `"`
}

// SetSearchPath sets search_path to "<schema>", public for the connection so
// migration SQL runs unqualified inside the tenant schema (spec 4.1).
func SetSearchPath(ctx context.Context, conn Execer, schema string) error {
	sql := fmt.Sprintf("set search_path = %s, public", QuoteIdent(schema))
	if _, err := conn.Exec(ctx, sql); err != nil {
		return fmt.Errorf("set search_path to %s: %w", schema, err)
	}
	return nil
}
