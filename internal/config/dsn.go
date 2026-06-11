package config

import (
	"fmt"
	"net/url"
	"os"
	"strings"
)

// ResolveDSN returns the connection string from the environment variable named
// by database.dsn_env. The value is never stored in the config struct, so it
// cannot leak through a config dump (R3.2).
func (c *Config) ResolveDSN() (string, error) {
	dsn := os.Getenv(c.Database.DSNEnv)
	if dsn == "" {
		return "", fmt.Errorf("database DSN not set: environment variable %s is empty", c.Database.DSNEnv)
	}
	return dsn, nil
}

// RedactDSN returns a connection string safe for logs and output: the password
// is replaced with "xxxxx" and any other form is reduced to a placeholder. It
// accepts both URL form (postgres://...) and keyword form (host=... password=...).
func RedactDSN(dsn string) string {
	if dsn == "" {
		return ""
	}

	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		u, err := url.Parse(dsn)
		if err != nil {
			return "postgres://[redacted]"
		}
		if u.User != nil {
			if _, hasPassword := u.User.Password(); hasPassword {
				u.User = url.UserPassword(u.User.Username(), "xxxxx")
			}
		}
		// Query parameters may also carry credentials; drop them entirely.
		u.RawQuery = ""
		return u.String()
	}

	// Keyword form: redact the password token only, keep the rest for context.
	fields := strings.Fields(dsn)
	for i, f := range fields {
		if k, _, ok := strings.Cut(f, "="); ok && strings.EqualFold(k, "password") {
			fields[i] = k + "=xxxxx"
		}
	}
	return strings.Join(fields, " ")
}
