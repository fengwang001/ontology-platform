package matcher

import "fmt"

// Constraint is an attribute predicate on a pattern node.
type Constraint struct {
	Attr string
	Op   string
	Val  any
}

// NodePattern constrains a pattern variable to a node type and attributes.
type NodePattern struct {
	Var         string
	Type        string
	Constraints []Constraint
}

// EdgePattern constrains a typed directed edge between two variables.
type EdgePattern struct {
	FromVar string
	Type    string
	ToVar   string
}

// Pattern is a subgraph pattern: typed nodes, typed edges, attribute constraints.
type Pattern struct {
	Nodes []NodePattern
	Edges []EdgePattern
}

// Supported constraint operators.
const (
	OpEq = "=="
	OpNe = "!="
	OpGt = ">"
	OpLt = "<"
)

var validOps = map[string]struct{}{
	OpEq: {}, OpNe: {}, OpGt: {}, OpLt: {},
}

// compiledPattern merges repeated declarations of the same variable so that
// one variable can only ever bind to one object with one consistent type.
type compiledPattern struct {
	vars      []string          // variables in first-declaration order
	varType   map[string]string // merged node type
	varConstr map[string][]Constraint
	edges     []EdgePattern
}

// validate checks a pattern and returns a distinguishable reject error.
func (p *Pattern) validate() (*compiledPattern, error) {
	if len(p.Nodes) == 0 {
		return nil, ErrDegenerateFullScan
	}

	cp := &compiledPattern{
		varType:   map[string]string{},
		varConstr: map[string][]Constraint{},
	}
	declared := map[string]struct{}{}
	for _, node := range p.Nodes {
		if node.Var == "" || node.Type == "" {
			return nil, fmt.Errorf("%w: every node needs a non-empty variable and type", ErrInconsistentBinding)
		}
		for _, c := range node.Constraints {
			if c.Attr == "" {
				return nil, fmt.Errorf("%w: variable %q has a constraint with an empty attribute", ErrInconsistentBinding, node.Var)
			}
			if _, ok := validOps[c.Op]; !ok {
				return nil, fmt.Errorf("%w: variable %q uses unsupported constraint operator %q", ErrInconsistentBinding, node.Var, c.Op)
			}
		}
		if prev, ok := cp.varType[node.Var]; ok {
			// Same variable must carry one consistent node type.
			if prev != node.Type {
				return nil, fmt.Errorf("%w: variable %q declared as both %q and %q", ErrInconsistentBinding, node.Var, prev, node.Type)
			}
		} else {
			cp.varType[node.Var] = node.Type
			cp.vars = append(cp.vars, node.Var)
		}
		cp.varConstr[node.Var] = append(cp.varConstr[node.Var], node.Constraints...)
		declared[node.Var] = struct{}{}
	}

	edgeSeen := map[EdgePattern]struct{}{}
	for _, edge := range p.Edges {
		if edge.FromVar == "" || edge.ToVar == "" || edge.Type == "" {
			return nil, fmt.Errorf("%w: every edge needs non-empty endpoints and type", ErrInconsistentBinding)
		}
		if _, ok := declared[edge.FromVar]; !ok {
			return nil, fmt.Errorf("%w: edge endpoint %q is not a declared node variable", ErrInconsistentBinding, edge.FromVar)
		}
		if _, ok := declared[edge.ToVar]; !ok {
			return nil, fmt.Errorf("%w: edge endpoint %q is not a declared node variable", ErrInconsistentBinding, edge.ToVar)
		}
		// Duplicated edge declarations let the same mapping be yielded through
		// multiple search paths, i.e. isomorphic matches returned more than once.
		if _, dup := edgeSeen[edge]; dup {
			return nil, fmt.Errorf("%w: edge %s -%s-> %s is declared more than once", ErrIsomorphicDuplicates, edge.FromVar, edge.Type, edge.ToVar)
		}
		edgeSeen[edge] = struct{}{}
		cp.edges = append(cp.edges, edge)
	}

	return cp, nil
}
