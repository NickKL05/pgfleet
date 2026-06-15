package diffgen

import (
	"strings"
	"testing"

	"github.com/NickKL05/pgfleet/internal/drift/model"
)

func repairUsers(name string) *model.Schema {
	s := model.NewSchema(name)
	s.Tables["users"] = &model.Table{
		Name: "users",
		Columns: []*model.Column{
			{Name: "id", Position: 1, Type: "bigint", NotNull: true, Identity: "a"},
			{Name: "email", Position: 2, Type: "text", NotNull: true},
		},
		Constraints: map[string]*model.Constraint{
			"users_pkey": {Name: "users_pkey", Type: "p", Definition: "PRIMARY KEY (id)"},
		},
		Indexes: map[string]*model.Index{
			"users_email_idx": {Name: "users_email_idx", Definition: "CREATE INDEX users_email_idx ON users USING btree (email)"},
		},
	}
	return s
}

func allSQL(p *RepairPlan) string {
	var b strings.Builder
	for _, s := range p.Statements {
		b.WriteString(s.SQL)
		b.WriteString("\n")
	}
	return b.String()
}

func TestRepairMissingIndex(t *testing.T) {
	ref := repairUsers("tenant_template")
	ten := repairUsers("tenant_087")
	delete(ten.Tables["users"].Indexes, "users_email_idx")

	plan := GenerateRepair(ref, ten, "tenant_087", RepairOptions{})
	if len(plan.Statements) != 1 {
		t.Fatalf("expected 1 statement, got %d: %s", len(plan.Statements), allSQL(plan))
	}
	if plan.Statements[0].SQL != "CREATE INDEX users_email_idx ON users USING btree (email);" {
		t.Fatalf("unexpected statement: %q", plan.Statements[0].SQL)
	}
}

func TestRepairModifiedColumn(t *testing.T) {
	ref := repairUsers("tenant_template")
	ten := repairUsers("tenant_142")
	ten.Tables["users"].Columns[1].Type = "character varying(100)"

	plan := GenerateRepair(ref, ten, "tenant_142", RepairOptions{})
	if len(plan.Statements) != 1 {
		t.Fatalf("expected 1 statement, got %d: %s", len(plan.Statements), allSQL(plan))
	}
	want := `ALTER TABLE "users" ALTER COLUMN "email" TYPE text;`
	if plan.Statements[0].SQL != want {
		t.Fatalf("got %q, want %q", plan.Statements[0].SQL, want)
	}
}

func TestRepairExtraTableGuarded(t *testing.T) {
	ref := repairUsers("tenant_template")
	ten := repairUsers("tenant_199")
	ten.Tables["audit"] = &model.Table{
		Name:        "audit",
		Columns:     []*model.Column{{Name: "id", Position: 1, Type: "bigint"}},
		Constraints: map[string]*model.Constraint{},
		Indexes:     map[string]*model.Index{},
	}

	// Without --allow-destructive: DROP TABLE is skipped, not emitted.
	guarded := GenerateRepair(ref, ten, "tenant_199", RepairOptions{})
	if len(guarded.Statements) != 0 {
		t.Fatalf("expected no statements without allow-destructive, got: %s", allSQL(guarded))
	}
	if len(guarded.Skipped) != 1 || !strings.Contains(guarded.Skipped[0].SQL, "DROP TABLE") {
		t.Fatalf("expected a skipped DROP TABLE, got %+v", guarded.Skipped)
	}

	// With --allow-destructive: DROP TABLE emitted; the extra column is not a
	// separate statement (it goes with the table).
	allowed := GenerateRepair(ref, ten, "tenant_199", RepairOptions{AllowDestructive: true})
	if len(allowed.Statements) != 1 {
		t.Fatalf("expected exactly the DROP TABLE, got: %s", allSQL(allowed))
	}
	if !allowed.Statements[0].Destructive || allowed.Statements[0].SQL != `DROP TABLE "audit";` {
		t.Fatalf("unexpected statement: %+v", allowed.Statements[0])
	}
}

func TestRepairMissingTableOrdering(t *testing.T) {
	ref := repairUsers("tenant_template")
	ten := model.NewSchema("tenant_new") // empty: the whole table is missing

	plan := GenerateRepair(ref, ten, "tenant_new", RepairOptions{})

	// Expect CREATE TABLE, then ADD CONSTRAINT, then CREATE INDEX, in that order.
	if len(plan.Statements) != 3 {
		t.Fatalf("expected 3 statements, got %d: %s", len(plan.Statements), allSQL(plan))
	}
	order := []string{"CREATE TABLE", "ADD CONSTRAINT", "CREATE INDEX"}
	for i, want := range order {
		if !strings.Contains(plan.Statements[i].SQL, want) {
			t.Errorf("statement %d = %q, expected to contain %q", i, plan.Statements[i].SQL, want)
		}
	}
	// The columns must be inside CREATE TABLE, not separate ADD COLUMN.
	if strings.Contains(allSQL(plan), "ADD COLUMN") {
		t.Errorf("missing table should not produce ADD COLUMN statements:\n%s", allSQL(plan))
	}
	if !strings.Contains(plan.Statements[0].SQL, "GENERATED ALWAYS AS IDENTITY") {
		t.Errorf("identity column not rendered in CREATE TABLE:\n%s", plan.Statements[0].SQL)
	}
}

func TestRepairCleanIsEmpty(t *testing.T) {
	plan := GenerateRepair(repairUsers("a"), repairUsers("b"), "b", RepairOptions{})
	if !plan.Empty() {
		t.Fatalf("identical schemas should yield an empty plan, got: %s", allSQL(plan))
	}
}
