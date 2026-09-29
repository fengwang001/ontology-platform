package graphmatch

import (
	"fmt"
	"sort"
	"strings"
)

// NodePattern constrains one pattern variable: an optional object type and
// optional property equality constraints. The same variable may appear in
// multiple NodePatterns; its constraints are merged and it is still bound to
// exactly one object.
type NodePattern struct {
	Var   string
	Type  string
	Props map[string]string
}

// EdgePattern constrains a typed edge between two pattern variables.
type EdgePattern struct {
	FromVar string
	ToVar   string
	Type    string
}

// Pattern is a subgraph pattern over node variables and typed edges.
type Pattern struct {
	Nodes []NodePattern
	Edges []EdgePattern
}

// validate checks structural validity and returns the merged node
// constraints keyed by variable.
func (p *Pattern) validate() (map[string]NodePattern, error) {
	if len(p.Nodes) == 0 {
		return nil, fmt.Errorf("graphmatch: pattern has no node constraints")
	}
	merged := make(map[string]NodePattern, len(p.Nodes))
	for _, n := range p.Nodes {
		if n.Var == "" {
			return nil, fmt.Errorf("graphmatch: node pattern with empty variable")
		}
		cur, ok := merged[n.Var]
		if !ok {
			cur = NodePattern{Var: n.Var, Props: map[string]string{}}
		}
		if n.Type != "" {
			if cur.Type != "" && cur.Type != n.Type {
				return nil, fmt.Errorf("graphmatch: variable %q has conflicting type constraints %q and %q", n.Var, cur.Type, n.Type)
			}
			cur.Type = n.Type
		}
		for k, v := range n.Props {
			if prev, dup := cur.Props[k]; dup && prev != v {
				return nil, fmt.Errorf("graphmatch: variable %q has conflicting property constraints on %q", n.Var, k)
			}
			cur.Props[k] = v
		}
		merged[n.Var] = cur
	}
	for _, e := range p.Edges {
		if e.Type == "" {
			return nil, fmt.Errorf("graphmatch: edge pattern %q->%q has empty edge type", e.FromVar, e.ToVar)
		}
		if _, ok := merged[e.FromVar]; !ok {
			return nil, fmt.Errorf("graphmatch: edge pattern references undeclared variable %q", e.FromVar)
		}
		if _, ok := merged[e.ToVar]; !ok {
			return nil, fmt.Errorf("graphmatch: edge pattern references undeclared variable %q", e.ToVar)
		}
	}
	return merged, nil
}

// String renders the pattern for logs and error messages.
func (p *Pattern) String() string {
	var b strings.Builder
	b.WriteString("nodes[")
	for i, n := range p.Nodes {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(n.Var)
		if n.Type != "" {
			b.WriteString(":" + n.Type)
		}
		if len(n.Props) > 0 {
			keys := make([]string, 0, len(n.Props))
			for k := range n.Props {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			b.WriteString("{")
			for j, k := range keys {
				if j > 0 {
					b.WriteByte(',')
				}
				b.WriteString(k + "=" + n.Props[k])
			}
			b.WriteString("}")
		}
	}
	b.WriteString("] edges[")
	for i, e := range p.Edges {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(e.FromVar + "-" + e.Type + "->" + e.ToVar)
	}
	b.WriteString("]")
	return b.String()
}

// constrained reports whether a variable has at least one constraint that
// prunes the search space: a node type, a property, or an incident edge.
func (p *Pattern) constrained(vars map[string]NodePattern, v string) bool {
	n := vars[v]
	if n.Type != "" || len(n.Props) > 0 {
		return true
	}
	for _, e := range p.Edges {
		if e.FromVar == v || e.ToVar == v {
			return true
		}
	}
	return false
}
