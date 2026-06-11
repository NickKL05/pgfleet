package model

// Column returns the named column of a table, or nil if absent. Used by the
// diff generator to drill into a modified column for a field-level explanation.
func (s *Schema) Column(table, name string) *Column {
	t := s.Tables[table]
	if t == nil {
		return nil
	}
	for _, c := range t.Columns {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// Constraint returns the named constraint of a table, or nil if absent.
func (s *Schema) Constraint(table, name string) *Constraint {
	t := s.Tables[table]
	if t == nil {
		return nil
	}
	return t.Constraints[name]
}
