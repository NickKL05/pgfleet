package report

import (
	"bytes"
	"strings"
	"testing"
)

func sampleDriftReport() *DriftReport {
	r := NewDriftReport("schema tenant_template")
	r.Add(TenantDrift{Schema: "tenant_001", Drifted: false})
	r.Add(TenantDrift{
		Schema:  "tenant_142",
		Drifted: true,
		Differences: []ObjectDiff{
			{Type: "column", Name: "users.display_name", Class: "modified", Changes: []FieldChange{
				{Field: "type", From: "text", To: "character varying(100)"},
			}},
			{Type: "index", Name: "users_created_at_idx", Class: "missing"},
		},
	})
	return r
}

func TestDriftReportAddAndDrifted(t *testing.T) {
	r := sampleDriftReport()
	if !r.Drifted() {
		t.Fatal("report with a drifted tenant should report Drifted")
	}
	if r.Summary["drifted"] != 1 || r.Summary["clean"] != 1 {
		t.Fatalf("unexpected summary: %v", r.Summary)
	}
}

func TestRenderDriftHuman(t *testing.T) {
	var buf bytes.Buffer
	if err := RenderDriftHuman(&buf, sampleDriftReport()); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "tenant_142") || !strings.Contains(out, "missing") {
		t.Fatalf("missing expected content:\n%s", out)
	}
	if strings.Contains(out, "tenant_001") {
		t.Fatalf("clean tenant should not be listed in the human summary:\n%s", out)
	}

	// A fully clean report states no drift.
	clean := NewDriftReport("schema t")
	clean.Add(TenantDrift{Schema: "a", Drifted: false})
	buf.Reset()
	if err := RenderDriftHuman(&buf, clean); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "No drift") {
		t.Fatalf("expected a no-drift line, got:\n%s", buf.String())
	}
}

func TestRenderDiffHuman(t *testing.T) {
	var buf bytes.Buffer
	if err := RenderDiffHuman(&buf, sampleDriftReport()); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	// The diff renderer expands field-level changes.
	if !strings.Contains(out, "type: text -> character varying(100)") {
		t.Fatalf("expected field-level change, got:\n%s", out)
	}
	if !strings.Contains(out, "no differences") {
		t.Fatalf("expected the clean tenant to be reported, got:\n%s", out)
	}
}

func TestRenderDriftJSON(t *testing.T) {
	var buf bytes.Buffer
	if err := RenderDriftJSON(&buf, sampleDriftReport()); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"reference"`, `"tenant_142"`, `"changes"`, `"class": "missing"`} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("JSON missing %s", want)
		}
	}
}
