// Package config loads, validates, and redacts pgfleet configuration.
//
// Precedence is file < env < flag (R3.3). The database DSN is never read from
// or written to the config file; it is sourced from the environment variable
// named by database.dsn_env and must never appear in logs or output (R3.2).
package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// DefaultPath is the config file consulted when --config is not supplied.
const DefaultPath = "pgfleet.yaml"

// DiscoveryMode selects how tenant schemas are enumerated.
type DiscoveryMode string

// Supported tenant discovery modes.
const (
	DiscoveryQuery   DiscoveryMode = "query"
	DiscoveryPattern DiscoveryMode = "pattern"
	DiscoveryStatic  DiscoveryMode = "static"
)

// ReferenceMode selects the canonical reference for drift detection.
type ReferenceMode string

// Supported drift reference modes.
const (
	ReferenceSchema   ReferenceMode = "schema"
	ReferenceSnapshot ReferenceMode = "snapshot"
)

// Config is the fully validated configuration for a single invocation.
type Config struct {
	Database   Database   `yaml:"database"`
	Tenants    Tenants    `yaml:"tenants"`
	Migrations Migrations `yaml:"migrations"`
	Drift      Drift      `yaml:"drift"`
	Run        Run        `yaml:"run"`
}

// Database holds connection settings; the DSN itself lives only in the env.
type Database struct {
	DSNEnv string `yaml:"dsn_env"`
}

// Tenants configures tenant discovery and exclusion.
type Tenants struct {
	Discovery Discovery `yaml:"discovery"`
	Exclude   []string  `yaml:"exclude"`
}

// Discovery selects how tenant schemas are enumerated.
type Discovery struct {
	Mode    DiscoveryMode `yaml:"mode"`
	Query   string        `yaml:"query"`
	Pattern string        `yaml:"pattern"`
	Static  []string      `yaml:"static"`
}

// Migrations configures the migration directory, state table, and lock.
type Migrations struct {
	Dir    string `yaml:"dir"`
	Table  string `yaml:"table"`
	LockID int64  `yaml:"lock_id"`
}

// Drift configures the canonical reference and the ignore list.
type Drift struct {
	Reference Reference `yaml:"reference"`
	Ignore    []string  `yaml:"ignore"`
}

// Reference names the canonical source drift is compared against.
type Reference struct {
	Mode     ReferenceMode `yaml:"mode"`
	Schema   string        `yaml:"schema"`
	Snapshot string        `yaml:"snapshot"`
}

// Run configures concurrency, timeouts, and failure behavior.
type Run struct {
	StatementTimeout Duration `yaml:"statement_timeout"`
	LockTimeout      Duration `yaml:"lock_timeout"`
	Concurrency      int      `yaml:"concurrency"`
	FailFast         bool     `yaml:"fail_fast"`
}

// Duration is a time.Duration that unmarshals from a Go duration string
// such as "60s" or "5s".
type Duration time.Duration

// UnmarshalYAML parses a duration string such as "60s" into a Duration.
func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	var s string
	if err := value.Decode(&s); err != nil {
		return err
	}
	parsed, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}
	*d = Duration(parsed)
	return nil
}

// Std returns the standard library duration value.
func (d Duration) Std() time.Duration { return time.Duration(d) }

func (d Duration) String() string { return time.Duration(d).String() }

// defaults applies the values documented in the example pgfleet.yaml for any
// field the user left unset.
func defaults() Config {
	return Config{
		Database:   Database{DSNEnv: "PGFLEET_DSN"},
		Migrations: Migrations{Dir: "./migrations", Table: "_pgfleet_migrations", LockID: 743201},
		Drift:      Drift{Reference: Reference{Mode: ReferenceSchema}},
		Run: Run{
			Concurrency:      16,
			StatementTimeout: Duration(60 * time.Second),
			LockTimeout:      Duration(5 * time.Second),
		},
	}
}

// Load reads, parses, and validates the config at path. A missing required key
// yields an error that names the key; callers map this to exit code 2 (R3.1).
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("config file not found: %s", path)
		}
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}

	cfg := defaults()
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// Validate reports the first missing or invalid required key by name (R3.1).
func (c *Config) Validate() error {
	if c.Database.DSNEnv == "" {
		return missing("database.dsn_env")
	}

	switch c.Tenants.Discovery.Mode {
	case DiscoveryQuery:
		if c.Tenants.Discovery.Query == "" {
			return missing("tenants.discovery.query")
		}
	case DiscoveryPattern:
		if c.Tenants.Discovery.Pattern == "" {
			return missing("tenants.discovery.pattern")
		}
	case DiscoveryStatic:
		if len(c.Tenants.Discovery.Static) == 0 {
			return missing("tenants.discovery.static")
		}
	case "":
		return missing("tenants.discovery.mode")
	default:
		return fmt.Errorf("config: tenants.discovery.mode must be one of query|pattern|static, got %q", c.Tenants.Discovery.Mode)
	}

	if c.Migrations.Dir == "" {
		return missing("migrations.dir")
	}
	if c.Migrations.Table == "" {
		return missing("migrations.table")
	}

	switch c.Drift.Reference.Mode {
	case ReferenceSchema:
		if c.Drift.Reference.Schema == "" {
			return missing("drift.reference.schema")
		}
	case ReferenceSnapshot:
		if c.Drift.Reference.Snapshot == "" {
			return missing("drift.reference.snapshot")
		}
	case "":
		return missing("drift.reference.mode")
	default:
		return fmt.Errorf("config: drift.reference.mode must be one of schema|snapshot, got %q", c.Drift.Reference.Mode)
	}

	if c.Run.Concurrency < 1 {
		return fmt.Errorf("config: run.concurrency must be at least 1, got %d", c.Run.Concurrency)
	}
	return nil
}

func missing(key string) error {
	return fmt.Errorf("config: required key %q is missing or empty", key)
}
