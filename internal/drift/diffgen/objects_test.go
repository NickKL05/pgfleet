package diffgen

import (
	"strings"
	"testing"

	"github.com/NickKL05/pgfleet/internal/drift/model"
)

func emptyTable(name string, cols ...*model.Column) *model.Table {
	return &model.Table{
		Name:        name,
		Columns:     cols,
		Constraints: map[string]*model.Constraint{},
		Indexes:     map[string]*model.Index{},
	}
}

func TestRepairView(t *testing.T) {
	ref := model.NewSchema("ref")
	ref.Views["av"] = &model.View{Name: "av", Definition: "SELECT id FROM users"}

	missing := GenerateRepair(ref, model.NewSchema("t"), "t", RepairOptions{})
	if !strings.Contains(allSQL(missing), `CREATE VIEW "av" AS SELECT id FROM users;`) {
		t.Errorf("missing view: %s", allSQL(missing))
	}

	extra := GenerateRepair(model.NewSchema("t"), ref, "t", RepairOptions{})
	if !strings.Contains(allSQL(extra), `DROP VIEW "av";`) {
		t.Errorf("extra view: %s", allSQL(extra))
	}

	changed := model.NewSchema("t")
	changed.Views["av"] = &model.View{Name: "av", Definition: "SELECT id, email FROM users"}
	modified := GenerateRepair(ref, changed, "t", RepairOptions{})
	if !strings.Contains(allSQL(modified), `CREATE OR REPLACE VIEW "av"`) {
		t.Errorf("modified view: %s", allSQL(modified))
	}
}

func TestRepairSequence(t *testing.T) {
	ref := model.NewSchema("ref")
	ref.Sequences["ctr"] = &model.Sequence{Name: "ctr", DataType: "bigint", Start: 1, Min: 1, Max: 100, Increment: 2, Cycle: true}

	missing := GenerateRepair(ref, model.NewSchema("t"), "t", RepairOptions{})
	want := `CREATE SEQUENCE "ctr" AS bigint INCREMENT BY 2 MINVALUE 1 MAXVALUE 100 START WITH 1 CYCLE;`
	if !strings.Contains(allSQL(missing), want) {
		t.Errorf("missing sequence:\n got %s\nwant %s", allSQL(missing), want)
	}

	extra := GenerateRepair(model.NewSchema("t"), ref, "t", RepairOptions{})
	if !strings.Contains(allSQL(extra), `DROP SEQUENCE "ctr";`) {
		t.Errorf("extra sequence: %s", allSQL(extra))
	}

	changed := model.NewSchema("t")
	changed.Sequences["ctr"] = &model.Sequence{Name: "ctr", DataType: "bigint", Start: 1, Min: 1, Max: 100, Increment: 1, Cycle: false}
	modified := GenerateRepair(ref, changed, "t", RepairOptions{})
	if !strings.Contains(allSQL(modified), `ALTER SEQUENCE "ctr"`) {
		t.Errorf("modified sequence: %s", allSQL(modified))
	}
}

func TestRepairEnum(t *testing.T) {
	ref := model.NewSchema("ref")
	ref.Types["st"] = &model.EnumType{Name: "st", Labels: []string{"a", "b'c"}}

	missing := GenerateRepair(ref, model.NewSchema("t"), "t", RepairOptions{})
	if !strings.Contains(allSQL(missing), `CREATE TYPE "st" AS ENUM ('a', 'b''c');`) {
		t.Errorf("missing enum: %s", allSQL(missing))
	}

	extra := GenerateRepair(model.NewSchema("t"), ref, "t", RepairOptions{})
	if !strings.Contains(allSQL(extra), `DROP TYPE "st";`) {
		t.Errorf("extra enum: %s", allSQL(extra))
	}

	// A modified enum cannot be altered automatically; it is skipped.
	changed := model.NewSchema("t")
	changed.Types["st"] = &model.EnumType{Name: "st", Labels: []string{"a"}}
	modified := GenerateRepair(ref, changed, "t", RepairOptions{})
	if len(modified.Skipped) == 0 {
		t.Errorf("modified enum should be skipped, got statements: %s", allSQL(modified))
	}
}

func TestRepairTrigger(t *testing.T) {
	ref := model.NewSchema("ref")
	ref.Tables["t"] = emptyTable("t", &model.Column{Name: "id", Position: 1, Type: "integer"})
	ref.Triggers["t.tg"] = &model.Trigger{Name: "tg", Table: "t",
		Definition: "CREATE TRIGGER tg BEFORE INSERT ON t FOR EACH ROW EXECUTE FUNCTION f()"}

	tenant := model.NewSchema("x")
	tenant.Tables["t"] = emptyTable("t", &model.Column{Name: "id", Position: 1, Type: "integer"})

	missing := GenerateRepair(ref, tenant, "x", RepairOptions{})
	if !strings.Contains(allSQL(missing), "CREATE TRIGGER tg BEFORE INSERT ON t") {
		t.Errorf("missing trigger: %s", allSQL(missing))
	}

	extra := GenerateRepair(tenant, ref, "x", RepairOptions{})
	if !strings.Contains(allSQL(extra), `DROP TRIGGER "tg" ON "t";`) {
		t.Errorf("extra trigger: %s", allSQL(extra))
	}
}

func TestRepairColumnDefaultAndNullability(t *testing.T) {
	ref := model.NewSchema("ref")
	ref.Tables["t"] = emptyTable("t", &model.Column{Name: "c", Position: 1, Type: "integer", NotNull: false, Default: "0"})

	tenant := model.NewSchema("x")
	tenant.Tables["t"] = emptyTable("t", &model.Column{Name: "c", Position: 1, Type: "integer", NotNull: true, Default: ""})

	plan := GenerateRepair(ref, tenant, "x", RepairOptions{})
	sql := allSQL(plan)
	if !strings.Contains(sql, `ALTER TABLE "t" ALTER COLUMN "c" DROP NOT NULL;`) {
		t.Errorf("expected DROP NOT NULL: %s", sql)
	}
	if !strings.Contains(sql, `ALTER TABLE "t" ALTER COLUMN "c" SET DEFAULT 0;`) {
		t.Errorf("expected SET DEFAULT: %s", sql)
	}
}

func TestRepairConstraintKindChange(t *testing.T) {
	ref := model.NewSchema("ref")
	rt := emptyTable("t", &model.Column{Name: "id", Position: 1, Type: "integer"})
	rt.Constraints["c"] = &model.Constraint{Name: "c", Type: "u", Definition: "UNIQUE (id)"}
	ref.Tables["t"] = rt

	tenant := model.NewSchema("x")
	xt := emptyTable("t", &model.Column{Name: "id", Position: 1, Type: "integer"})
	xt.Constraints["c"] = &model.Constraint{Name: "c", Type: "p", Definition: "PRIMARY KEY (id)"}
	tenant.Tables["t"] = xt

	plan := GenerateRepair(ref, tenant, "x", RepairOptions{})
	sql := allSQL(plan)
	if !strings.Contains(sql, `DROP CONSTRAINT "c";`) || !strings.Contains(sql, `ADD CONSTRAINT "c" UNIQUE (id);`) {
		t.Errorf("expected drop + re-add of constraint: %s", sql)
	}
}
