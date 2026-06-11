package model

import (
	"fmt"
	"path"
	"sort"
	"strings"
)

// Object type tags used in fingerprint identity and diff output.
const (
	KindTable      = "table"
	KindColumn     = "column"
	KindConstraint = "constraint"
	KindIndex      = "index"
	KindView       = "view"
	KindSequence   = "sequence"
	KindFunction   = "function"
	KindTrigger    = "trigger"
	KindPolicy     = "policy"
	KindType       = "type"
)

// Schema is the normalized structural model of one tenant schema. All string
// fields are already normalized (schema qualifier stripped, whitespace and cast
// noise collapsed) at build time.
type Schema struct {
	Name      string
	Tables    map[string]*Table
	Views     map[string]*View
	Sequences map[string]*Sequence
	Functions map[string]*Function
	Triggers  map[string]*Trigger
	Policies  map[string]*Policy
	Types     map[string]*EnumType
}

// NewSchema returns an empty schema model.
func NewSchema(name string) *Schema {
	return &Schema{
		Name:      name,
		Tables:    map[string]*Table{},
		Views:     map[string]*View{},
		Sequences: map[string]*Sequence{},
		Functions: map[string]*Function{},
		Triggers:  map[string]*Trigger{},
		Policies:  map[string]*Policy{},
		Types:     map[string]*EnumType{},
	}
}

type Table struct {
	Name        string
	Columns     []*Column // ordered by catalog position
	Constraints map[string]*Constraint
	Indexes     map[string]*Index
}

type Column struct {
	Name      string
	Position  int
	Type      string // normalized
	NotNull   bool
	Default   string // normalized, empty when none
	Generated bool   // stored generated column
	Identity  string // "", "a" (always), "d" (by default)
}

type Constraint struct {
	Name       string
	Type       string // p, f, u, c
	Definition string // normalized pg_get_constraintdef, schema stripped
}

type Index struct {
	Name       string
	Definition string // normalized pg_get_indexdef, schema stripped
}

type View struct {
	Name       string
	Definition string // normalized pg_get_viewdef
}

type Sequence struct {
	Name      string
	DataType  string
	Start     int64
	Min       int64
	Max       int64
	Increment int64
	Cycle     bool
}

type Function struct {
	Name     string // proname
	Args     string // identity arguments, e.g. "integer, text"
	BodyHash string // sha256 of normalized pg_get_functiondef
}

type Trigger struct {
	Name       string
	Table      string
	Definition string // normalized pg_get_triggerdef, schema stripped
}

type Policy struct {
	Name       string
	Table      string
	Command    string // r, a, w, d, * (all)
	Permissive bool
	Roles      string // sorted, comma joined
	Using      string // normalized
	WithCheck  string // normalized
}

type EnumType struct {
	Name   string
	Labels []string // significant order
}

// Options controls how a schema is flattened for fingerprinting.
type Options struct {
	IgnoreColumnOrder bool
	Strict            bool
	Ignore            []string // type:name patterns with * wildcards (R5.10)
}

// FlatObject is a single fingerprintable unit: a stable identity plus a
// canonical body string that is hashed.
type FlatObject struct {
	Type string
	Name string
	Body string
}

// Flatten produces the deterministic, filtered list of fingerprintable objects
// for the schema. Objects matching the ignore list (and the children of ignored
// tables) are dropped (R5.10).
func (s *Schema) Flatten(opts Options) []FlatObject {
	rules := parseIgnore(opts.Ignore)
	ignoredTables := map[string]bool{}
	for name := range s.Tables {
		if rules.matches(KindTable, name) {
			ignoredTables[name] = true
		}
	}

	var out []FlatObject
	add := func(kind, name, body string) {
		if rules.matches(kind, name) {
			return
		}
		out = append(out, FlatObject{Type: kind, Name: name, Body: body})
	}

	for _, t := range sortedTables(s.Tables) {
		if ignoredTables[t.Name] {
			continue
		}
		add(KindTable, t.Name, "table")
		for _, c := range t.Columns {
			add(KindColumn, t.Name+"."+c.Name, c.body(opts))
		}
		for _, con := range sortedConstraints(t.Constraints) {
			add(KindConstraint, t.Name+"."+con.Name, con.body())
		}
		for _, idx := range sortedIndexes(t.Indexes) {
			add(KindIndex, idx.Name, idx.Definition)
		}
	}
	for _, v := range sortedViews(s.Views) {
		add(KindView, v.Name, v.Definition)
	}
	for _, sq := range sortedSequences(s.Sequences) {
		add(KindSequence, sq.Name, sq.body())
	}
	for _, fn := range sortedFunctions(s.Functions) {
		add(KindFunction, fmt.Sprintf("%s(%s)", fn.Name, fn.Args), "body="+fn.BodyHash)
	}
	for _, tg := range sortedTriggers(s.Triggers) {
		if ignoredTables[tg.Table] {
			continue
		}
		add(KindTrigger, tg.Table+"."+tg.Name, tg.Definition)
	}
	for _, p := range sortedPolicies(s.Policies) {
		if ignoredTables[p.Table] {
			continue
		}
		add(KindPolicy, p.Table+"."+p.Name, p.body())
	}
	for _, e := range sortedTypes(s.Types) {
		add(KindType, e.Name, "enum("+strings.Join(e.Labels, ",")+")")
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Type != out[j].Type {
			return out[i].Type < out[j].Type
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func (c *Column) body(opts Options) string {
	var b strings.Builder
	fmt.Fprintf(&b, "type=%s notnull=%t default=%s generated=%t identity=%s",
		c.Type, c.NotNull, c.Default, c.Generated, c.Identity)
	if !opts.IgnoreColumnOrder {
		fmt.Fprintf(&b, " pos=%d", c.Position)
	}
	return b.String()
}

func (c *Constraint) body() string {
	return fmt.Sprintf("type=%s def=%s", c.Type, c.Definition)
}

func (s *Sequence) body() string {
	return fmt.Sprintf("type=%s start=%d min=%d max=%d increment=%d cycle=%t",
		s.DataType, s.Start, s.Min, s.Max, s.Increment, s.Cycle)
}

func (p *Policy) body() string {
	return fmt.Sprintf("cmd=%s permissive=%t roles=%s using=%s withcheck=%s",
		p.Command, p.Permissive, p.Roles, p.Using, p.WithCheck)
}

// ignoreRule is a parsed "type:name" ignore entry.
type ignoreRule struct {
	kind string // "*" matches any kind
	name string // glob
}

type ignoreSet []ignoreRule

func parseIgnore(patterns []string) ignoreSet {
	set := make(ignoreSet, 0, len(patterns))
	for _, p := range patterns {
		kind, name, ok := strings.Cut(p, ":")
		if !ok {
			// A bare entry matches any kind with that name glob.
			set = append(set, ignoreRule{kind: "*", name: p})
			continue
		}
		set = append(set, ignoreRule{kind: strings.TrimSpace(kind), name: strings.TrimSpace(name)})
	}
	return set
}

func (s ignoreSet) matches(kind, name string) bool {
	for _, r := range s {
		if r.kind != "*" && r.kind != kind {
			continue
		}
		if ok, _ := path.Match(r.name, name); ok {
			return true
		}
	}
	return false
}
