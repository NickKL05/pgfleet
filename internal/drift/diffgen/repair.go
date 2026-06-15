package diffgen

import (
	"fmt"
	"sort"
	"strings"

	"github.com/NickKL05/pgfleet/internal/drift/model"
	"github.com/NickKL05/pgfleet/internal/report"
)

// RepairOptions controls corrective DDL generation.
type RepairOptions struct {
	Model model.Options
	// AllowDestructive permits DROP TABLE and DROP COLUMN. Without it, such
	// drift is reported but skipped (R5.9).
	AllowDestructive bool
}

// Statement is one corrective DDL statement in a repair plan.
type Statement struct {
	SQL         string
	Object      string
	Destructive bool
}

// Skipped is a corrective action that was not emitted, either because it is
// destructive and destruction was not allowed, or because the object type is
// not supported for automatic repair.
type Skipped struct {
	Object string
	Reason string
	SQL    string // the statement a human could run, shown commented in output
}

// RepairPlan is the ordered set of statements that converge one tenant to the
// reference, plus anything deliberately left out.
type RepairPlan struct {
	Tenant     string
	Statements []Statement
	Skipped    []Skipped
}

// Empty reports whether the plan has nothing to do.
func (p *RepairPlan) Empty() bool { return len(p.Statements) == 0 && len(p.Skipped) == 0 }

// HasWork reports whether the plan has statements to apply.
func (p *RepairPlan) HasWork() bool { return len(p.Statements) > 0 }

// Ranks order statements so dependents drop before dependencies and
// dependencies are created before dependents (R5.7). Drops occupy the low
// range, creates the high range.
const (
	rankDropPolicy     = 10
	rankDropTrigger    = 11
	rankDropIndex      = 12
	rankDropForeignKey = 13
	rankDropConstraint = 14
	rankDropColumn     = 15
	rankDropView       = 16
	rankDropTable      = 17
	rankDropSequence   = 18
	rankDropType       = 19

	rankCreateType       = 30
	rankCreateSequence   = 31
	rankCreateTable      = 32
	rankAddColumn        = 33
	rankAlterColumn      = 34
	rankCreateConstraint = 35
	rankCreateForeignKey = 36
	rankCreateIndex      = 37
	rankCreateView       = 38
	rankCreateTrigger    = 39
	rankCreatePolicy     = 40
)

type action struct {
	rank        int
	sql         string
	object      string
	destructive bool
}

// GenerateRepair produces the corrective DDL that converges tenant to
// reference. Generation is offline-safe: it never executes anything.
func GenerateRepair(reference, tenant *model.Schema, tenantName string, opts RepairOptions) *RepairPlan {
	plan := &RepairPlan{Tenant: tenantName}
	diffs := Compare(reference, tenant, opts.Model)

	// Whole-table additions and removals are handled as a unit so child object
	// diffs for those tables are not emitted as separate statements.
	missingTables := map[string]bool{}
	extraTables := map[string]bool{}
	for _, d := range diffs {
		if d.Type == model.KindTable {
			switch d.Class {
			case Missing:
				missingTables[d.Name] = true
			case Extra:
				extraTables[d.Name] = true
			}
		}
	}

	var actions []action
	add := func(a action) { actions = append(actions, a) }

	for _, d := range diffs {
		switch d.Type {
		case model.KindTable:
			repairTable(reference, d, opts, &actions, &plan.Skipped)
		case model.KindColumn:
			repairColumn(reference, d, missingTables, extraTables, opts, add, &plan.Skipped)
		case model.KindConstraint:
			repairConstraint(reference, d, extraTables, add)
		case model.KindIndex:
			repairIndex(reference, tenant, d, extraTables, add)
		case model.KindView:
			repairView(reference, d, add)
		case model.KindSequence:
			repairSequence(reference, d, extraTables, add)
		case model.KindType:
			repairEnum(reference, d, add, &plan.Skipped)
		case model.KindTrigger:
			repairTrigger(reference, d, extraTables, add)
		case model.KindFunction:
			plan.Skipped = append(plan.Skipped, Skipped{
				Object: d.Type + " " + d.Name,
				Reason: "function bodies are not retained; recreate manually",
			})
		case model.KindPolicy:
			plan.Skipped = append(plan.Skipped, Skipped{
				Object: d.Type + " " + d.Name,
				Reason: "policy repair is not automated; recreate manually",
			})
		}
	}

	sort.SliceStable(actions, func(i, j int) bool { return actions[i].rank < actions[j].rank })
	for _, a := range actions {
		plan.Statements = append(plan.Statements, Statement{SQL: a.sql, Object: a.object, Destructive: a.destructive})
	}
	return plan
}

func repairTable(ref *model.Schema, d report.ObjectDiff, opts RepairOptions, actions *[]action, skipped *[]Skipped) {
	switch d.Class {
	case Missing:
		t := ref.Tables[d.Name]
		if t == nil {
			return
		}
		*actions = append(*actions, action{rank: rankCreateTable, sql: createTableDDL(t), object: "table " + d.Name})
	case Extra:
		sql := fmt.Sprintf("DROP TABLE %s;", quoteIdent(d.Name))
		if opts.AllowDestructive {
			*actions = append(*actions, action{rank: rankDropTable, sql: sql, object: "table " + d.Name, destructive: true})
		} else {
			*skipped = append(*skipped, Skipped{Object: "table " + d.Name, Reason: "destructive (DROP TABLE); pass --allow-destructive", SQL: sql})
		}
	}
}

func repairColumn(ref *model.Schema, d report.ObjectDiff, missingTables, extraTables map[string]bool, opts RepairOptions, add func(action), skipped *[]Skipped) {
	table, col := splitName(d.Name)
	if missingTables[table] || extraTables[table] {
		return // handled by the table-level create/drop
	}
	switch d.Class {
	case Missing:
		c := ref.Column(table, col)
		if c == nil {
			return
		}
		add(action{rank: rankAddColumn, sql: fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s;", quoteIdent(table), columnDDL(c)), object: "column " + d.Name})
	case Extra:
		sql := fmt.Sprintf("ALTER TABLE %s DROP COLUMN %s;", quoteIdent(table), quoteIdent(col))
		if opts.AllowDestructive {
			add(action{rank: rankDropColumn, sql: sql, object: "column " + d.Name, destructive: true})
		} else {
			*skipped = append(*skipped, Skipped{Object: "column " + d.Name, Reason: "destructive (DROP COLUMN); pass --allow-destructive", SQL: sql})
		}
	case Modified:
		c := ref.Column(table, col)
		if c == nil {
			return
		}
		for i, sql := range alterColumnDDL(table, col, c, d.Changes, skipped) {
			add(action{rank: rankAlterColumn, sql: sql, object: fmt.Sprintf("column %s (%d)", d.Name, i)})
		}
	}
}

func repairConstraint(ref *model.Schema, d report.ObjectDiff, extraTables map[string]bool, add func(action)) {
	table, name := splitName(d.Name)
	if extraTables[table] {
		return
	}
	dropSQL := fmt.Sprintf("ALTER TABLE %s DROP CONSTRAINT %s;", quoteIdent(table), quoteIdent(name))
	switch d.Class {
	case Missing:
		if c := ref.Constraint(table, name); c != nil {
			add(action{rank: constraintCreateRank(c.Type), sql: addConstraintDDL(table, c), object: "constraint " + d.Name})
		}
	case Extra:
		add(action{rank: rankDropConstraint, sql: dropSQL, object: "constraint " + d.Name})
	case Modified:
		add(action{rank: rankDropConstraint, sql: dropSQL, object: "constraint " + d.Name})
		if c := ref.Constraint(table, name); c != nil {
			add(action{rank: constraintCreateRank(c.Type), sql: addConstraintDDL(table, c), object: "constraint " + d.Name})
		}
	}
}

func repairIndex(ref, tenant *model.Schema, d report.ObjectDiff, extraTables map[string]bool, add func(action)) {
	// The diff names an index without its table; find the owner in whichever
	// model has it so an index on an extra table is left to the table drop.
	table, ok := tableOfIndex(ref, d.Name)
	if !ok {
		table, _ = tableOfIndex(tenant, d.Name)
	}
	if extraTables[table] {
		return
	}
	dropSQL := fmt.Sprintf("DROP INDEX %s;", quoteIdent(d.Name))
	switch d.Class {
	case Missing:
		if def := indexDef(ref, d.Name); def != "" {
			add(action{rank: rankCreateIndex, sql: ensureSemicolon(def), object: "index " + d.Name})
		}
	case Extra:
		add(action{rank: rankDropIndex, sql: dropSQL, object: "index " + d.Name})
	case Modified:
		add(action{rank: rankDropIndex, sql: dropSQL, object: "index " + d.Name})
		if def := indexDef(ref, d.Name); def != "" {
			add(action{rank: rankCreateIndex, sql: ensureSemicolon(def), object: "index " + d.Name})
		}
	}
}

func repairView(ref *model.Schema, d report.ObjectDiff, add func(action)) {
	v := ref.Views[d.Name]
	switch d.Class {
	case Missing:
		if v != nil {
			add(action{rank: rankCreateView, sql: fmt.Sprintf("CREATE VIEW %s AS %s;", quoteIdent(d.Name), v.Definition), object: "view " + d.Name})
		}
	case Extra:
		add(action{rank: rankDropView, sql: fmt.Sprintf("DROP VIEW %s;", quoteIdent(d.Name)), object: "view " + d.Name})
	case Modified:
		if v != nil {
			add(action{rank: rankCreateView, sql: fmt.Sprintf("CREATE OR REPLACE VIEW %s AS %s;", quoteIdent(d.Name), v.Definition), object: "view " + d.Name})
		}
	}
}

func repairSequence(ref *model.Schema, d report.ObjectDiff, extraTables map[string]bool, add func(action)) {
	// A sequence owned by an extra table is dropped with that table.
	for t := range extraTables {
		if strings.HasPrefix(d.Name, t+"_") {
			return
		}
	}
	s := ref.Sequences[d.Name]
	switch d.Class {
	case Missing:
		if s != nil {
			add(action{rank: rankCreateSequence, sql: createSequenceDDL(s), object: "sequence " + d.Name})
		}
	case Extra:
		add(action{rank: rankDropSequence, sql: fmt.Sprintf("DROP SEQUENCE %s;", quoteIdent(d.Name)), object: "sequence " + d.Name})
	case Modified:
		if s != nil {
			add(action{rank: rankCreateSequence, sql: alterSequenceDDL(s), object: "sequence " + d.Name})
		}
	}
}

func repairEnum(ref *model.Schema, d report.ObjectDiff, add func(action), skipped *[]Skipped) {
	e := ref.Types[d.Name]
	switch d.Class {
	case Missing:
		if e != nil {
			add(action{rank: rankCreateType, sql: createEnumDDL(e), object: "type " + d.Name})
		}
	case Extra:
		add(action{rank: rankDropType, sql: fmt.Sprintf("DROP TYPE %s;", quoteIdent(d.Name)), object: "type " + d.Name})
	case Modified:
		*skipped = append(*skipped, Skipped{
			Object: "type " + d.Name,
			Reason: "enum label changes need manual ALTER TYPE (cannot reorder or remove values automatically)",
		})
	}
}

func repairTrigger(ref *model.Schema, d report.ObjectDiff, extraTables map[string]bool, add func(action)) {
	table, name := splitName(d.Name)
	if extraTables[table] {
		return
	}
	tg := ref.Triggers[d.Name]
	dropSQL := fmt.Sprintf("DROP TRIGGER %s ON %s;", quoteIdent(name), quoteIdent(table))
	switch d.Class {
	case Missing:
		if tg != nil {
			add(action{rank: rankCreateTrigger, sql: ensureSemicolon(tg.Definition), object: "trigger " + d.Name})
		}
	case Extra:
		add(action{rank: rankDropTrigger, sql: dropSQL, object: "trigger " + d.Name})
	case Modified:
		add(action{rank: rankDropTrigger, sql: dropSQL, object: "trigger " + d.Name})
		if tg != nil {
			add(action{rank: rankCreateTrigger, sql: ensureSemicolon(tg.Definition), object: "trigger " + d.Name})
		}
	}
}

func constraintCreateRank(contype string) int {
	if contype == "f" {
		return rankCreateForeignKey
	}
	return rankCreateConstraint
}
