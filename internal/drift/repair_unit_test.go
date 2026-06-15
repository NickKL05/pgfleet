package drift

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NickKL05/pgfleet/internal/drift/diffgen"
)

func samplePlan(tenant string) *diffgen.RepairPlan {
	return &diffgen.RepairPlan{
		Tenant: tenant,
		Statements: []diffgen.Statement{
			{SQL: "CREATE INDEX users_idx ON users (a);", Object: "index users_idx"},
			{SQL: `DROP TABLE "rogue";`, Object: "table rogue", Destructive: true},
		},
		Skipped: []diffgen.Skipped{
			{Object: "function f()", Reason: "function bodies are not retained"},
		},
	}
}

func TestRenderRepairSQL(t *testing.T) {
	out := RenderRepairSQL(samplePlan("tenant_7"))
	for _, want := range []string{
		"repair for tenant_7",
		`SET search_path TO "tenant_7", public;`,
		"CREATE INDEX users_idx ON users (a);",
		"-- destructive: table rogue",
		`DROP TABLE "rogue";`,
		"-- skipped",
		"function f()",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered SQL missing %q:\n%s", want, out)
		}
	}
}

func TestWriteRepairFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "repair")
	plans := []*diffgen.RepairPlan{
		samplePlan("tenant_a"),
		{Tenant: "tenant_clean"}, // empty: no file written
	}
	written, err := WriteRepairFiles(dir, plans)
	if err != nil {
		t.Fatal(err)
	}
	if len(written) != 1 {
		t.Fatalf("expected 1 file written, got %d: %v", len(written), written)
	}
	body, err := os.ReadFile(filepath.Join(dir, "tenant_a.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "CREATE INDEX") {
		t.Errorf("written file missing content:\n%s", body)
	}
	if _, err := os.Stat(filepath.Join(dir, "tenant_clean.sql")); !os.IsNotExist(err) {
		t.Error("clean tenant should not produce a file")
	}
}
