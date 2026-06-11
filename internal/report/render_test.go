package report

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func sampleReport() *RunReport {
	r := NewRunReport("run-test", "migrate up", time.Unix(0, 0).UTC())
	for i := 0; i < 5; i++ {
		r.Add(TenantResult{Schema: "tenant_" + string(rune('a'+i)), From: 0, To: 3, Status: StatusOK})
	}
	r.Add(TenantResult{Schema: "tenant_bad", From: 1, To: 2, Status: StatusFailed, Error: "syntax error at or near \"creat\""})
	return r
}

func TestRenderHumanGroups(t *testing.T) {
	var buf bytes.Buffer
	if err := RenderHuman(&buf, sampleReport()); err != nil {
		t.Fatal(err)
	}
	out := buf.String()

	// The five healthy tenants collapse onto a single grouped line (R4.8).
	if strings.Count(out, "ok") < 1 {
		t.Errorf("expected an ok group line, got:\n%s", out)
	}
	if !strings.Contains(out, "5 total") {
		t.Errorf("expected collapsed count for 5 tenants, got:\n%s", out)
	}
	// The failure is expanded with its Postgres error.
	if !strings.Contains(out, "tenant_bad") || !strings.Contains(out, "syntax error") {
		t.Errorf("expected expanded failure, got:\n%s", out)
	}
}

func TestRenderJSONShape(t *testing.T) {
	var buf bytes.Buffer
	if err := RenderJSON(&buf, sampleReport()); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{`"run_id"`, `"started_at"`, `"tenants"`, `"summary"`, `"status": "failed"`} {
		if !strings.Contains(out, want) {
			t.Errorf("JSON missing %s in:\n%s", want, out)
		}
	}
}

func TestFailed(t *testing.T) {
	r := NewRunReport("x", "migrate up", time.Now())
	r.Add(TenantResult{Schema: "a", Status: StatusOK})
	if r.Failed() {
		t.Error("all-ok report should not be Failed")
	}
	r.Add(TenantResult{Schema: "b", Status: StatusChecksumMismatch})
	if !r.Failed() {
		t.Error("report with checksum mismatch should be Failed")
	}
}
