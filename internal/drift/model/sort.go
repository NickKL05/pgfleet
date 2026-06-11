package model

import "sort"

// The helpers below return map values in a stable, name-sorted order so that
// flattening and therefore fingerprinting is deterministic (R5.11).

func sortedTables(m map[string]*Table) []*Table {
	out := make([]*Table, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func sortedConstraints(m map[string]*Constraint) []*Constraint {
	out := make([]*Constraint, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func sortedIndexes(m map[string]*Index) []*Index {
	out := make([]*Index, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func sortedViews(m map[string]*View) []*View {
	out := make([]*View, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func sortedSequences(m map[string]*Sequence) []*Sequence {
	out := make([]*Sequence, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func sortedFunctions(m map[string]*Function) []*Function {
	out := make([]*Function, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Args < out[j].Args
	})
	return out
}

func sortedTriggers(m map[string]*Trigger) []*Trigger {
	out := make([]*Trigger, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Table != out[j].Table {
			return out[i].Table < out[j].Table
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func sortedPolicies(m map[string]*Policy) []*Policy {
	out := make([]*Policy, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Table != out[j].Table {
			return out[i].Table < out[j].Table
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func sortedTypes(m map[string]*EnumType) []*EnumType {
	out := make([]*EnumType, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
