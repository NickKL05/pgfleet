package web

import (
	"encoding/json"
	"net/http"
	"slices"

	"github.com/NickKL05/pgfleet/internal/report"
)

// wantRefresh reports whether the caller asked to bypass the cache via
// ?refresh=1 (or the "refresh" button in the UI).
func wantRefresh(r *http.Request) bool {
	v := r.URL.Query().Get("refresh")
	return v == "1" || v == "true"
}

func (s *Server) handleSummary(w http.ResponseWriter, r *http.Request) {
	refresh := wantRefresh(r)
	mig, err := s.cache.migrationStatus(r.Context(), refresh)
	if err != nil {
		s.writeError(w, r, http.StatusBadGateway, err)
		return
	}
	drf, err := s.cache.driftStatus(r.Context(), refresh)
	if err != nil {
		s.writeError(w, r, http.StatusBadGateway, err)
		return
	}

	resp := SummaryResponse{
		Total:         len(mig.Tenants),
		LatestVersion: s.provider.LatestVersion(),
		UpToDate:      mig.Summary[migrationUpToDate],
		Behind:        mig.Summary[migrationBehind],
		Drifted:       drf.Summary["drifted"],
		Clean:         drf.Summary["clean"],
		Reference:     drf.Reference,
	}
	s.writeJSON(w, r, resp)
}

func (s *Server) handleTenants(w http.ResponseWriter, r *http.Request) {
	refresh := wantRefresh(r)
	mig, err := s.cache.migrationStatus(r.Context(), refresh)
	if err != nil {
		s.writeError(w, r, http.StatusBadGateway, err)
		return
	}
	drf, err := s.cache.driftStatus(r.Context(), refresh)
	if err != nil {
		s.writeError(w, r, http.StatusBadGateway, err)
		return
	}

	drifted := make(map[string]bool, len(drf.Tenants))
	for _, t := range drf.Tenants {
		drifted[t.Schema] = t.Drifted
	}

	rows := make([]TenantRow, 0, len(mig.Tenants))
	for _, t := range mig.Tenants {
		rows = append(rows, TenantRow{
			Schema:          t.Schema,
			Version:         tenantVersion(t),
			MigrationStatus: t.Status,
			Drifted:         drifted[t.Schema],
		})
	}

	s.writeJSON(w, r, TenantsResponse{
		LatestVersion: s.provider.LatestVersion(),
		Reference:     drf.Reference,
		Tenants:       rows,
	})
}

func (s *Server) handleDrift(w http.ResponseWriter, r *http.Request) {
	drf, err := s.cache.driftStatus(r.Context(), wantRefresh(r))
	if err != nil {
		s.writeError(w, r, http.StatusBadGateway, err)
		return
	}
	s.writeJSON(w, r, drf)
}

func (s *Server) handleTenantDrift(w http.ResponseWriter, r *http.Request) {
	schema := r.PathValue("tenant")
	// Only diff a tenant we actually discovered; this both returns a clean 404
	// for typos and prevents the endpoint from probing arbitrary schema names.
	if !slices.Contains(s.provider.Tenants(), schema) {
		s.writeError(w, r, http.StatusNotFound, errUnknownTenant(schema))
		return
	}
	rep, err := s.provider.TenantDiff(r.Context(), schema)
	if err != nil {
		s.writeError(w, r, http.StatusBadGateway, err)
		return
	}
	s.writeJSON(w, r, rep)
}

func (s *Server) handleVersions(w http.ResponseWriter, r *http.Request) {
	mig, err := s.cache.migrationStatus(r.Context(), wantRefresh(r))
	if err != nil {
		s.writeError(w, r, http.StatusBadGateway, err)
		return
	}
	s.writeJSON(w, r, VersionsResponse{
		LatestVersion: s.provider.LatestVersion(),
		Versions:      versionHistogram(mig, s.provider.LatestVersion()),
	})
}

// versionHistogram counts tenants at each version from 0 to the latest, so the
// chart shows empty buckets (a version nobody is on yet) rather than skipping
// them. A tenant reporting a version above latest, which is possible
// mid-rollout when a migration was just added, gets its own trailing bucket.
func versionHistogram(mig *report.RunReport, latest int) []VersionBucket {
	counts := map[int]int{}
	top := latest
	for _, t := range mig.Tenants {
		v := tenantVersion(t)
		counts[v]++
		if v > top {
			top = v
		}
	}
	buckets := make([]VersionBucket, 0, top+1)
	for v := 0; v <= top; v++ {
		buckets = append(buckets, VersionBucket{Version: v, Count: counts[v]})
	}
	return buckets
}

// writeJSON encodes v as indented JSON. Indentation keeps the API pleasant to
// curl, matching the CLI's JSON output.
func (s *Server) writeJSON(w http.ResponseWriter, r *http.Request, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		s.logger.Error("encode response", "path", r.URL.Path, "error", err)
	}
}

// writeError logs the cause and returns a small JSON error body. The database
// error text is not leaked to the client; it is logged server-side.
func (s *Server) writeError(w http.ResponseWriter, r *http.Request, status int, err error) {
	s.logger.Error("request failed", "path", r.URL.Path, "status", status, "error", err)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	body := map[string]string{"error": http.StatusText(status)}
	// A 404 names the missing resource; it is safe and useful to echo.
	if status == http.StatusNotFound {
		body["error"] = err.Error()
	}
	_ = json.NewEncoder(w).Encode(body)
}
