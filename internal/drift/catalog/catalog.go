// Package catalog reads PostgreSQL system catalogs with a fixed, small number
// of set-based queries (8 total, each covering every tenant schema at once,
// never one query per table) and turns the rows into normalized schema models.
// Keeping the read database-wide is what lets drift verify scale to thousands of
// schemas (R5.5).
package catalog

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// Querier is the read surface catalog needs; *pgxpool.Pool satisfies it.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// The row types below are raw rows scanned from the catalog, before assembly
// into models.
type (
	// ColumnRow is one row of the columns query.
	ColumnRow struct {
		Schema, Table, Name string
		AttNum              int
		Type                string
		NotNull             bool
		Default             *string
		Generated           string // "" or "s"
		Identity            string // "", "a", "d"
	}
	// ConstraintRow is one constraint of the constraints/indexes query.
	ConstraintRow struct {
		Schema, Table, Name string
		Contype             string // p, f, u, c
		Def                 string
	}
	// IndexRow is one non-constraint index of the constraints/indexes query.
	IndexRow struct {
		Schema, Table, Name, Def string
	}
	// ViewRow is one row of the views query.
	ViewRow struct {
		Schema, Name, Def string
	}
	// SequenceRow is one row of the sequences query.
	SequenceRow struct {
		Schema, Name, DataType     string
		Start, Min, Max, Increment int64
		Cycle                      bool
	}
	// FunctionRow is one row of the functions query.
	FunctionRow struct {
		Schema, Name, Args, Def string
	}
	// TriggerRow is one row of the triggers query.
	TriggerRow struct {
		Schema, Table, Name, Def string
	}
	// PolicyRow is one row of the policies query.
	PolicyRow struct {
		Schema, Table, Name string
		Command             string
		Permissive          bool
		Roles               []string
		Using, WithCheck    *string
	}
	// EnumRow is one row of the enum-types query.
	EnumRow struct {
		Schema, Name string
		Labels       []string
	}
)

// Rows aggregates every catalog row read in one invocation.
type Rows struct {
	Columns     []ColumnRow
	Constraints []ConstraintRow
	Indexes     []IndexRow
	Views       []ViewRow
	Sequences   []SequenceRow
	Functions   []FunctionRow
	Triggers    []TriggerRow
	Policies    []PolicyRow
	Enums       []EnumRow
}

// Read runs the catalog queries for the given schemas and returns the raw rows.
func Read(ctx context.Context, db Querier, schemas []string) (*Rows, error) {
	r := &Rows{}
	if len(schemas) == 0 {
		return r, nil
	}
	if err := r.readColumns(ctx, db, schemas); err != nil {
		return nil, err
	}
	if err := r.readConstraintsAndIndexes(ctx, db, schemas); err != nil {
		return nil, err
	}
	if err := r.readViews(ctx, db, schemas); err != nil {
		return nil, err
	}
	if err := r.readSequences(ctx, db, schemas); err != nil {
		return nil, err
	}
	if err := r.readFunctions(ctx, db, schemas); err != nil {
		return nil, err
	}
	if err := r.readTriggers(ctx, db, schemas); err != nil {
		return nil, err
	}
	if err := r.readPolicies(ctx, db, schemas); err != nil {
		return nil, err
	}
	if err := r.readEnums(ctx, db, schemas); err != nil {
		return nil, err
	}
	return r, nil
}

const qColumns = `
select n.nspname, c.relname, a.attname, a.attnum,
       format_type(a.atttypid, a.atttypmod),
       a.attnotnull,
       pg_get_expr(ad.adbin, ad.adrelid),
       a.attgenerated,
       a.attidentity
from pg_attribute a
join pg_class c on c.oid = a.attrelid
join pg_namespace n on n.oid = c.relnamespace
left join pg_attrdef ad on ad.adrelid = a.attrelid and ad.adnum = a.attnum
where n.nspname = any($1)
  and c.relkind in ('r','p')
  and a.attnum > 0
  and not a.attisdropped
order by n.nspname, c.relname, a.attnum`

func (r *Rows) readColumns(ctx context.Context, db Querier, schemas []string) error {
	rows, err := db.Query(ctx, qColumns, schemas)
	if err != nil {
		return wrap("columns", err)
	}
	defer rows.Close()
	for rows.Next() {
		var c ColumnRow
		var gen, ident string
		if err := rows.Scan(&c.Schema, &c.Table, &c.Name, &c.AttNum, &c.Type,
			&c.NotNull, &c.Default, &gen, &ident); err != nil {
			return wrap("columns scan", err)
		}
		c.Generated, c.Identity = gen, ident
		r.Columns = append(r.Columns, c)
	}
	return wrap("columns rows", rows.Err())
}

// qConstraintsIndexes merges constraints and non-constraint indexes into one
// set-based query, discriminated by the kind column, so the whole read stays
// within 8 catalog queries.
const qConstraintsIndexes = `
select 'constraint' as kind, n.nspname, c.relname, con.conname,
       con.contype::text, pg_get_constraintdef(con.oid)
from pg_constraint con
join pg_class c on c.oid = con.conrelid
join pg_namespace n on n.oid = c.relnamespace
where n.nspname = any($1) and con.contype in ('p','f','u','c')
union all
select 'index', n.nspname, t.relname, ic.relname,
       '', pg_get_indexdef(i.indexrelid)
from pg_index i
join pg_class ic on ic.oid = i.indexrelid
join pg_class t on t.oid = i.indrelid
join pg_namespace n on n.oid = t.relnamespace
where n.nspname = any($1)
  and not exists (select 1 from pg_constraint con where con.conindid = i.indexrelid)
order by 2, 3, 4`

func (r *Rows) readConstraintsAndIndexes(ctx context.Context, db Querier, schemas []string) error {
	rows, err := db.Query(ctx, qConstraintsIndexes, schemas)
	if err != nil {
		return wrap("constraints/indexes", err)
	}
	defer rows.Close()
	for rows.Next() {
		var kind, schema, table, name, contype, def string
		if err := rows.Scan(&kind, &schema, &table, &name, &contype, &def); err != nil {
			return wrap("constraints/indexes scan", err)
		}
		if kind == "constraint" {
			r.Constraints = append(r.Constraints, ConstraintRow{
				Schema: schema, Table: table, Name: name, Contype: contype, Def: def,
			})
		} else {
			r.Indexes = append(r.Indexes, IndexRow{Schema: schema, Table: table, Name: name, Def: def})
		}
	}
	return wrap("constraints/indexes rows", rows.Err())
}

const qViews = `
select n.nspname, c.relname, pg_get_viewdef(c.oid, true)
from pg_class c
join pg_namespace n on n.oid = c.relnamespace
where n.nspname = any($1) and c.relkind = 'v'
order by n.nspname, c.relname`

func (r *Rows) readViews(ctx context.Context, db Querier, schemas []string) error {
	rows, err := db.Query(ctx, qViews, schemas)
	if err != nil {
		return wrap("views", err)
	}
	defer rows.Close()
	for rows.Next() {
		var v ViewRow
		if err := rows.Scan(&v.Schema, &v.Name, &v.Def); err != nil {
			return wrap("views scan", err)
		}
		r.Views = append(r.Views, v)
	}
	return wrap("views rows", rows.Err())
}

const qSequences = `
select schemaname, sequencename, data_type::text,
       start_value, min_value, max_value, increment_by, cycle
from pg_sequences
where schemaname = any($1)
order by schemaname, sequencename`

func (r *Rows) readSequences(ctx context.Context, db Querier, schemas []string) error {
	rows, err := db.Query(ctx, qSequences, schemas)
	if err != nil {
		return wrap("sequences", err)
	}
	defer rows.Close()
	for rows.Next() {
		var s SequenceRow
		if err := rows.Scan(&s.Schema, &s.Name, &s.DataType,
			&s.Start, &s.Min, &s.Max, &s.Increment, &s.Cycle); err != nil {
			return wrap("sequences scan", err)
		}
		r.Sequences = append(r.Sequences, s)
	}
	return wrap("sequences rows", rows.Err())
}

const qFunctions = `
select n.nspname, p.proname,
       pg_get_function_identity_arguments(p.oid),
       pg_get_functiondef(p.oid)
from pg_proc p
join pg_namespace n on n.oid = p.pronamespace
where n.nspname = any($1) and p.prokind = 'f'
order by n.nspname, p.proname`

func (r *Rows) readFunctions(ctx context.Context, db Querier, schemas []string) error {
	rows, err := db.Query(ctx, qFunctions, schemas)
	if err != nil {
		return wrap("functions", err)
	}
	defer rows.Close()
	for rows.Next() {
		var f FunctionRow
		if err := rows.Scan(&f.Schema, &f.Name, &f.Args, &f.Def); err != nil {
			return wrap("functions scan", err)
		}
		r.Functions = append(r.Functions, f)
	}
	return wrap("functions rows", rows.Err())
}

const qTriggers = `
select n.nspname, c.relname, tg.tgname, pg_get_triggerdef(tg.oid)
from pg_trigger tg
join pg_class c on c.oid = tg.tgrelid
join pg_namespace n on n.oid = c.relnamespace
where n.nspname = any($1) and not tg.tgisinternal
order by n.nspname, c.relname, tg.tgname`

func (r *Rows) readTriggers(ctx context.Context, db Querier, schemas []string) error {
	rows, err := db.Query(ctx, qTriggers, schemas)
	if err != nil {
		return wrap("triggers", err)
	}
	defer rows.Close()
	for rows.Next() {
		var t TriggerRow
		if err := rows.Scan(&t.Schema, &t.Table, &t.Name, &t.Def); err != nil {
			return wrap("triggers scan", err)
		}
		r.Triggers = append(r.Triggers, t)
	}
	return wrap("triggers rows", rows.Err())
}

const qPolicies = `
select n.nspname, c.relname, pol.polname,
       pol.polcmd::text, pol.polpermissive,
       array(select rolname from pg_roles where oid = any(pol.polroles) order by rolname),
       pg_get_expr(pol.polqual, pol.polrelid),
       pg_get_expr(pol.polwithcheck, pol.polrelid)
from pg_policy pol
join pg_class c on c.oid = pol.polrelid
join pg_namespace n on n.oid = c.relnamespace
where n.nspname = any($1)
order by n.nspname, c.relname, pol.polname`

func (r *Rows) readPolicies(ctx context.Context, db Querier, schemas []string) error {
	rows, err := db.Query(ctx, qPolicies, schemas)
	if err != nil {
		return wrap("policies", err)
	}
	defer rows.Close()
	for rows.Next() {
		var p PolicyRow
		if err := rows.Scan(&p.Schema, &p.Table, &p.Name, &p.Command, &p.Permissive,
			&p.Roles, &p.Using, &p.WithCheck); err != nil {
			return wrap("policies scan", err)
		}
		r.Policies = append(r.Policies, p)
	}
	return wrap("policies rows", rows.Err())
}

const qEnums = `
select n.nspname, t.typname,
       array(select e.enumlabel from pg_enum e where e.enumtypid = t.oid order by e.enumsortorder)
from pg_type t
join pg_namespace n on n.oid = t.typnamespace
where n.nspname = any($1) and t.typtype = 'e'
order by n.nspname, t.typname`

func (r *Rows) readEnums(ctx context.Context, db Querier, schemas []string) error {
	rows, err := db.Query(ctx, qEnums, schemas)
	if err != nil {
		return wrap("enums", err)
	}
	defer rows.Close()
	for rows.Next() {
		var e EnumRow
		if err := rows.Scan(&e.Schema, &e.Name, &e.Labels); err != nil {
			return wrap("enums scan", err)
		}
		r.Enums = append(r.Enums, e)
	}
	return wrap("enums rows", rows.Err())
}
