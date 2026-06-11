package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"

	"github.com/NickKL05/pgfleet/internal/drift/model"
)

// Assemble turns raw catalog rows into one normalized model per schema. Every
// definition has its owning schema qualifier stripped and its whitespace and
// cast noise collapsed, so two structurally identical tenants assemble to equal
// models regardless of their names (spec 5.2).
func Assemble(rows *Rows, schemas []string) map[string]*model.Schema {
	out := make(map[string]*model.Schema, len(schemas))
	for _, s := range schemas {
		out[s] = model.NewSchema(s)
	}

	table := func(schema, name string) *model.Table {
		s := out[schema]
		if s == nil {
			return nil
		}
		t := s.Tables[name]
		if t == nil {
			t = &model.Table{
				Name:        name,
				Constraints: map[string]*model.Constraint{},
				Indexes:     map[string]*model.Index{},
			}
			s.Tables[name] = t
		}
		return t
	}

	for _, c := range rows.Columns {
		t := table(c.Schema, c.Table)
		if t == nil {
			continue
		}
		def := ""
		if c.Default != nil {
			def = model.NormalizeDefault(model.StripSchema(*c.Default, c.Schema))
		}
		t.Columns = append(t.Columns, &model.Column{
			Name:      c.Name,
			Position:  c.AttNum,
			Type:      model.NormalizeType(model.StripSchema(c.Type, c.Schema)),
			NotNull:   c.NotNull,
			Default:   def,
			Generated: c.Generated == "s",
			Identity:  c.Identity,
		})
	}

	for _, con := range rows.Constraints {
		t := table(con.Schema, con.Table)
		if t == nil {
			continue
		}
		t.Constraints[con.Name] = &model.Constraint{
			Name:       con.Name,
			Type:       con.Contype,
			Definition: model.NormalizeWhitespace(model.StripSchema(con.Def, con.Schema)),
		}
	}

	for _, idx := range rows.Indexes {
		t := table(idx.Schema, idx.Table)
		if t == nil {
			continue
		}
		t.Indexes[idx.Name] = &model.Index{
			Name:       idx.Name,
			Definition: model.NormalizeWhitespace(model.StripSchema(idx.Def, idx.Schema)),
		}
	}

	for _, v := range rows.Views {
		s := out[v.Schema]
		if s == nil {
			continue
		}
		s.Views[v.Name] = &model.View{
			Name:       v.Name,
			Definition: model.NormalizeWhitespace(model.StripSchema(v.Def, v.Schema)),
		}
	}

	for _, sq := range rows.Sequences {
		s := out[sq.Schema]
		if s == nil {
			continue
		}
		s.Sequences[sq.Name] = &model.Sequence{
			Name:      sq.Name,
			DataType:  sq.DataType,
			Start:     sq.Start,
			Min:       sq.Min,
			Max:       sq.Max,
			Increment: sq.Increment,
			Cycle:     sq.Cycle,
		}
	}

	for _, fn := range rows.Functions {
		s := out[fn.Schema]
		if s == nil {
			continue
		}
		normalizedDef := model.NormalizeWhitespace(model.StripSchema(fn.Def, fn.Schema))
		sum := sha256.Sum256([]byte(normalizedDef))
		s.Functions[fn.Name+"("+fn.Args+")"] = &model.Function{
			Name:     fn.Name,
			Args:     model.StripSchema(fn.Args, fn.Schema),
			BodyHash: hex.EncodeToString(sum[:]),
		}
	}

	for _, tg := range rows.Triggers {
		s := out[tg.Schema]
		if s == nil {
			continue
		}
		s.Triggers[tg.Table+"."+tg.Name] = &model.Trigger{
			Name:       tg.Name,
			Table:      tg.Table,
			Definition: model.NormalizeWhitespace(model.StripSchema(tg.Def, tg.Schema)),
		}
	}

	for _, p := range rows.Policies {
		s := out[p.Schema]
		if s == nil {
			continue
		}
		roles := append([]string(nil), p.Roles...)
		sort.Strings(roles)
		rolesStr := "public"
		if len(roles) > 0 {
			rolesStr = strings.Join(roles, ",")
		}
		s.Policies[p.Table+"."+p.Name] = &model.Policy{
			Name:       p.Name,
			Table:      p.Table,
			Command:    p.Command,
			Permissive: p.Permissive,
			Roles:      rolesStr,
			Using:      normalizeExpr(p.Using, p.Schema),
			WithCheck:  normalizeExpr(p.WithCheck, p.Schema),
		}
	}

	for _, e := range rows.Enums {
		s := out[e.Schema]
		if s == nil {
			continue
		}
		s.Types[e.Name] = &model.EnumType{Name: e.Name, Labels: append([]string(nil), e.Labels...)}
	}

	return out
}

func normalizeExpr(expr *string, schema string) string {
	if expr == nil {
		return ""
	}
	return model.NormalizeWhitespace(model.StripSchema(*expr, schema))
}
