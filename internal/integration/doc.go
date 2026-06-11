// Package integration holds end-to-end tests that run against a real PostgreSQL
// started via testcontainers (Postgres 15, 16, and 17 in CI). The tests live in
// files behind the "integration" build tag; this file exists only so the
// package is buildable, and `go test ./...` stays green, without that tag.
package integration
