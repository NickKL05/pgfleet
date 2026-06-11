//go:build integration

package integration

import (
	"context"
	"reflect"
	"testing"

	"github.com/NickKL05/pgfleet/internal/config"
	"github.com/NickKL05/pgfleet/internal/discovery"
)

// TestDiscoveryModes exercises query and pattern discovery against a live
// database, including exclude handling.
func TestDiscoveryModes(t *testing.T) {
	ctx := context.Background()
	schemas := bulkCreateSchemas(t, "disc_", 4) // disc_0000..0003

	mustExec(t, "create schema if not exists disc_control")
	t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), "drop schema if exists disc_control cascade") })
	mustExec(t, "create table disc_control.tenants (schema_name text primary key, active boolean not null default true)")
	for _, s := range schemas {
		mustExec(t, "insert into disc_control.tenants (schema_name) values ($1)", s)
	}

	t.Run("query", func(t *testing.T) {
		d := discovery.New(config.Tenants{
			Discovery: config.Discovery{Mode: config.DiscoveryQuery, Query: "select schema_name from disc_control.tenants where active"},
		})
		got, err := d.Tenants(ctx, testPool)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, schemas) {
			t.Fatalf("query discovery = %v, want %v", got, schemas)
		}
	})

	t.Run("pattern with exclude", func(t *testing.T) {
		d := discovery.New(config.Tenants{
			Discovery: config.Discovery{Mode: config.DiscoveryPattern, Pattern: `disc\_00%`},
			Exclude:   []string{"disc_0003"},
		})
		got, err := d.Tenants(ctx, testPool)
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"disc_0000", "disc_0001", "disc_0002"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("pattern discovery = %v, want %v", got, want)
		}
	})
}
