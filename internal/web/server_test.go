package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/NickKL05/pgfleet/internal/report"
)

// timeZero is a fixed start time for report fixtures; its value is irrelevant
// to the handlers under test.
func timeZero() time.Time { return time.Unix(0, 0).UTC() }

// fakeProvider is a Provider backed by canned reports, so the handlers can be
// exercised with no database. Call counts let a test assert the cache elides
// repeat queries.
type fakeProvider struct {
	migration *report.RunReport
	drift     *report.DriftReport
	diffs     map[string]*report.DriftReport
	latest    int
	tenants   []string

	err error // when set, every fleet query fails

	migCalls   int
	driftCalls int
}

func (f *fakeProvider) MigrationStatus(context.Context) (*report.RunReport, error) {
	f.migCalls++
	if f.err != nil {
		return nil, f.err
	}
	return f.migration, nil
}

func (f *fakeProvider) DriftStatus(context.Context) (*report.DriftReport, error) {
	f.driftCalls++
	if f.err != nil {
		return nil, f.err
	}
	return f.drift, nil
}

func (f *fakeProvider) TenantDiff(_ context.Context, schema string) (*report.DriftReport, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.diffs[schema], nil
}

func (f *fakeProvider) LatestVersion() int { return f.latest }
func (f *fakeProvider) Tenants() []string  { return f.tenants }

// sampleProvider builds a small three-tenant fleet: two up-to-date (one of them
// drifted), one behind.
func sampleProvider() *fakeProvider {
	mig := report.NewRunReport("t", "migrate status", timeZero())
	mig.Add(report.TenantResult{Schema: "tenant_001", From: 2, To: 2, Status: migrationUpToDate})
	mig.Add(report.TenantResult{Schema: "tenant_002", From: 2, To: 2, Status: migrationUpToDate})
	mig.Add(report.TenantResult{Schema: "tenant_003", From: 1, To: 1, Status: migrationBehind})

	drf := report.NewDriftReport("schema tenant_template")
	drf.Add(report.TenantDrift{Schema: "tenant_001", Drifted: false})
	drf.Add(report.TenantDrift{Schema: "tenant_002", Drifted: true, Differences: []report.ObjectDiff{
		{Type: "index", Name: "idx_users_email", Class: "missing"},
	}})
	drf.Add(report.TenantDrift{Schema: "tenant_003", Drifted: false})

	diff002 := report.NewDriftReport("schema tenant_template")
	diff002.Add(report.TenantDrift{Schema: "tenant_002", Drifted: true, Differences: []report.ObjectDiff{
		{Type: "column", Name: "users.email", Class: "modified", Changes: []report.FieldChange{
			{Field: "type", From: "text", To: "character varying(100)"},
		}},
	}})

	return &fakeProvider{
		migration: mig,
		drift:     drf,
		diffs:     map[string]*report.DriftReport{"tenant_002": diff002},
		latest:    2,
		tenants:   []string{"tenant_001", "tenant_002", "tenant_003"},
	}
}

func newTestServer(t *testing.T, p Provider) *Server {
	t.Helper()
	// CacheTTL 0 disables caching so per-test call counts are deterministic
	// unless a test opts into caching explicitly.
	s, err := NewServer(p, Options{})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	return s
}

func doGet(t *testing.T, s *Server, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %T: %v\nbody: %s", v, err, rec.Body.String())
	}
	return v
}

func TestSummary(t *testing.T) {
	s := newTestServer(t, sampleProvider())
	rec := doGet(t, s, "/api/summary")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	got := decode[SummaryResponse](t, rec)
	want := SummaryResponse{
		Total: 3, LatestVersion: 2, UpToDate: 2, Behind: 1,
		Drifted: 1, Clean: 2, Reference: "schema tenant_template",
	}
	if got != want {
		t.Errorf("summary = %+v, want %+v", got, want)
	}
}

func TestTenantsJoinsMigrationAndDrift(t *testing.T) {
	s := newTestServer(t, sampleProvider())
	rec := doGet(t, s, "/api/tenants")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	got := decode[TenantsResponse](t, rec)
	if got.LatestVersion != 2 || len(got.Tenants) != 3 {
		t.Fatalf("unexpected response: %+v", got)
	}

	byName := map[string]TenantRow{}
	for _, row := range got.Tenants {
		byName[row.Schema] = row
	}
	if row := byName["tenant_002"]; !row.Drifted || row.Version != 2 || row.MigrationStatus != migrationUpToDate {
		t.Errorf("tenant_002 = %+v, want drifted up-to-date v2", row)
	}
	if row := byName["tenant_003"]; row.Drifted || row.MigrationStatus != migrationBehind || row.Version != 1 {
		t.Errorf("tenant_003 = %+v, want clean behind v1", row)
	}
}

func TestDrift(t *testing.T) {
	s := newTestServer(t, sampleProvider())
	rec := doGet(t, s, "/api/drift")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	got := decode[report.DriftReport](t, rec)
	if got.Summary["drifted"] != 1 || got.Summary["clean"] != 2 {
		t.Errorf("summary = %v, want 1 drifted / 2 clean", got.Summary)
	}
}

func TestTenantDrift(t *testing.T) {
	s := newTestServer(t, sampleProvider())
	rec := doGet(t, s, "/api/drift/tenant_002")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	got := decode[report.DriftReport](t, rec)
	if len(got.Tenants) != 1 || !got.Tenants[0].Drifted {
		t.Fatalf("unexpected diff: %+v", got)
	}
	if len(got.Tenants[0].Differences[0].Changes) != 1 {
		t.Errorf("expected field-level change detail, got %+v", got.Tenants[0].Differences)
	}
}

func TestTenantDriftUnknownReturns404(t *testing.T) {
	s := newTestServer(t, sampleProvider())
	rec := doGet(t, s, "/api/drift/tenant_999")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	body := decode[map[string]string](t, rec)
	if !strings.Contains(body["error"], "tenant_999") {
		t.Errorf("error body = %v, want it to name the missing tenant", body)
	}
}

func TestVersionsHistogram(t *testing.T) {
	s := newTestServer(t, sampleProvider())
	rec := doGet(t, s, "/api/versions")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	got := decode[VersionsResponse](t, rec)
	// Buckets 0..2: nobody on 0, one on 1, two on 2.
	want := []VersionBucket{{Version: 0, Count: 0}, {Version: 1, Count: 1}, {Version: 2, Count: 2}}
	if len(got.Versions) != len(want) {
		t.Fatalf("versions = %+v, want %+v", got.Versions, want)
	}
	for i := range want {
		if got.Versions[i] != want[i] {
			t.Errorf("bucket %d = %+v, want %+v", i, got.Versions[i], want[i])
		}
	}
}

func TestProviderErrorReturns502(t *testing.T) {
	p := sampleProvider()
	p.err = errors.New("boom")
	s := newTestServer(t, p)
	rec := doGet(t, s, "/api/summary")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
	// The database error text must not leak to the client.
	if strings.Contains(rec.Body.String(), "boom") {
		t.Errorf("error body leaked internal error: %s", rec.Body.String())
	}
}

func TestCacheElidesRepeatQueries(t *testing.T) {
	p := sampleProvider()
	s, err := NewServer(p, Options{CacheTTL: time.Minute})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	// summary + tenants each need migration and drift; with caching the
	// underlying provider is queried once per report, not once per request.
	doGet(t, s, "/api/summary")
	doGet(t, s, "/api/tenants")
	doGet(t, s, "/api/versions")
	if p.migCalls != 1 {
		t.Errorf("MigrationStatus called %d times, want 1 (cached)", p.migCalls)
	}
	if p.driftCalls != 1 {
		t.Errorf("DriftStatus called %d times, want 1 (cached)", p.driftCalls)
	}

	// A refresh request bypasses the cache.
	doGet(t, s, "/api/summary?refresh=1")
	if p.migCalls != 2 || p.driftCalls != 2 {
		t.Errorf("refresh did not bypass cache: mig=%d drift=%d", p.migCalls, p.driftCalls)
	}
}

func TestSPAFallbackServesIndex(t *testing.T) {
	s := newTestServer(t, sampleProvider())
	// A client-side route that is not a real file must return index.html.
	rec := doGet(t, s, "/tenant/tenant_002")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("content-type = %q, want text/html", ct)
	}
	// The embedded SPA shell is served verbatim; check for a stable marker that
	// both the pre-built bundle and a Vite build carry.
	if !strings.Contains(rec.Body.String(), "pgfleet dashboard") {
		t.Errorf("fallback did not serve the SPA shell: %s", rec.Body.String())
	}
}
