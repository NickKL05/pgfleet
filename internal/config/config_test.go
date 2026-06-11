package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRedactDSN(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "url with password",
			in:   "postgres://app:s3cret@db.internal:5432/fleet?sslmode=require",
			want: "postgres://app:xxxxx@db.internal:5432/fleet",
		},
		{
			name: "url without password",
			in:   "postgres://app@db.internal:5432/fleet",
			want: "postgres://app@db.internal:5432/fleet",
		},
		{
			name: "keyword form",
			in:   "host=db.internal user=app password=s3cret dbname=fleet",
			want: "host=db.internal user=app password=xxxxx dbname=fleet",
		},
		{
			name: "empty",
			in:   "",
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := RedactDSN(tc.in)
			if got != tc.want {
				t.Fatalf("RedactDSN(%q) = %q, want %q", tc.in, got, tc.want)
			}
			if strings.Contains(got, "s3cret") {
				t.Fatalf("redacted output still contains the password: %q", got)
			}
		})
	}
}

func TestValidateNamesMissingKey(t *testing.T) {
	cfg := defaults()
	cfg.Tenants.Discovery.Mode = DiscoveryQuery
	// query left empty on purpose
	cfg.Drift.Reference.Schema = "tenant_template"

	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error for missing tenants.discovery.query")
	}
	if !strings.Contains(err.Error(), "tenants.discovery.query") {
		t.Fatalf("error should name the missing key, got: %v", err)
	}
}

func TestLoadAppliesDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pgfleet.yaml")
	body := `
tenants:
  discovery:
    mode: static
    static: ["tenant_a", "tenant_b"]
drift:
  reference:
    mode: schema
    schema: tenant_template
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Database.DSNEnv != "PGFLEET_DSN" {
		t.Errorf("default dsn_env not applied: %q", cfg.Database.DSNEnv)
	}
	if cfg.Run.Concurrency != 16 {
		t.Errorf("default concurrency not applied: %d", cfg.Run.Concurrency)
	}
	if cfg.Run.StatementTimeout.Std() != 60*time.Second {
		t.Errorf("default statement_timeout not applied: %s", cfg.Run.StatementTimeout)
	}
	if cfg.Migrations.Table != "_pgfleet_migrations" {
		t.Errorf("default migrations.table not applied: %q", cfg.Migrations.Table)
	}
}

func TestLoadParsesDurations(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pgfleet.yaml")
	body := `
tenants:
  discovery:
    mode: pattern
    pattern: "tenant_%"
drift:
  reference:
    mode: snapshot
    snapshot: ./schema.lock.json
run:
  concurrency: 8
  statement_timeout: 90s
  lock_timeout: 2s
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Run.StatementTimeout.Std() != 90*time.Second {
		t.Errorf("statement_timeout = %s, want 90s", cfg.Run.StatementTimeout)
	}
	if cfg.Run.LockTimeout.Std() != 2*time.Second {
		t.Errorf("lock_timeout = %s, want 2s", cfg.Run.LockTimeout)
	}
	if cfg.Run.StatementTimeout.String() != "1m30s" {
		t.Errorf("String() = %s, want 1m30s", cfg.Run.StatementTimeout.String())
	}
}

func TestValidateBranches(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*Config)
		wantKey string
	}{
		{"pattern missing", func(c *Config) { c.Tenants.Discovery.Mode = DiscoveryPattern }, "tenants.discovery.pattern"},
		{"static missing", func(c *Config) { c.Tenants.Discovery.Mode = DiscoveryStatic }, "tenants.discovery.static"},
		{"bad discovery mode", func(c *Config) { c.Tenants.Discovery.Mode = "bogus" }, "tenants.discovery.mode"},
		{"snapshot missing", func(c *Config) {
			c.Tenants.Discovery.Mode = DiscoveryStatic
			c.Tenants.Discovery.Static = []string{"a"}
			c.Drift.Reference = Reference{Mode: ReferenceSnapshot}
		}, "drift.reference.snapshot"},
		{"bad concurrency", func(c *Config) {
			c.Tenants.Discovery.Mode = DiscoveryStatic
			c.Tenants.Discovery.Static = []string{"a"}
			c.Drift.Reference = Reference{Mode: ReferenceSchema, Schema: "tpl"}
			c.Run.Concurrency = 0
		}, "run.concurrency"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := defaults()
			cfg.Drift.Reference = Reference{Mode: ReferenceSchema, Schema: "tpl"}
			tc.mutate(&cfg)
			err := cfg.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.wantKey) {
				t.Fatalf("got %v, want error naming %q", err, tc.wantKey)
			}
		})
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "nope.yaml")); err == nil {
		t.Fatal("expected error for missing config file")
	}
}

func TestResolveDSNFromEnv(t *testing.T) {
	cfg := defaults()
	cfg.Database.DSNEnv = "PGFLEET_TEST_DSN"
	t.Setenv("PGFLEET_TEST_DSN", "postgres://app@localhost/fleet")

	dsn, err := cfg.ResolveDSN()
	if err != nil {
		t.Fatalf("ResolveDSN: %v", err)
	}
	if dsn != "postgres://app@localhost/fleet" {
		t.Fatalf("unexpected DSN: %q", dsn)
	}
}
