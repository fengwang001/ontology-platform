package graphmatch

import (
	"sort"
	"strings"
)

// Match is one result: a consistent, injective binding of pattern variables
// to object IDs.
type Match struct {
	Bindings map[string]string
}

// isoKey identifies the set of objects a match maps to, independent of
// which variable maps where. Matches with equal isoKeys are isomorphic
// duplicates and are returned only once.
func (m Match) isoKey() string {
	ids := make([]string, 0, len(m.Bindings))
	for _, id := range m.Bindings {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return strings.Join(ids, "|")
}

// canonicalKey orders bindings by variable name for deterministic output.
func (m Match) canonicalKey() string {
	vars := make([]string, 0, len(m.Bindings))
	for v := range m.Bindings {
		vars = append(vars, v)
	}
	sort.Strings(vars)
	var b strings.Builder
	for _, v := range vars {
		b.WriteString(v)
		b.WriteByte('=')
		b.WriteString(m.Bindings[v])
		b.WriteByte(';')
	}
	return b.String()
}
