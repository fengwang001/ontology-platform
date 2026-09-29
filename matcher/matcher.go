package matcher

import (
	"context"
	"errors"
	"log"
	"strings"
)

// Match is one variable-to-object binding of a pattern onto the graph.
type Match map[string]string

// Matcher executes backtracking subgraph pattern matching.
type Matcher struct {
	logger *log.Logger
}

// NewMatcher builds a Matcher.
func NewMatcher(logger *log.Logger) *Matcher { return &Matcher{logger: logger} }

// MatchAll validates the pattern, then returns every distinct match.
// A rejected pattern returns a non-nil error and no partial results.
func (m *Matcher) MatchAll(ctx context.Context, g *Graph, p Pattern) ([]Match, error) {
	cp, err := p.validate()
	if err != nil {
		m.logf("REJECT pattern=%s reason=%q", patternString(p), err.Error())
		// Rejected queries never return partial results.
		return nil, err
	}

	m.logf("MATCH-START pattern=%s vars=%v", patternString(p), cp.vars)

	// Initial candidate domains: object IDs of the variable's node type,
	// pre-filtered by attribute constraints. Type/attribute pruning happens
	// before any edge search, so a pattern that matches nothing never expands
	// into a full scan.
	domains := make(map[string][]string, len(cp.vars))
	for _, variable := range cp.vars {
		var candidates []string
		for _, id := range g.ObjectsOfType(cp.varType[variable]) {
			if ok, why := checkConstraints(g.object(id), cp.varConstr[variable]); ok {
				candidates = append(candidates, id)
			} else {
				m.logf("PRUNE var=%s candidate=%s basis=%s", variable, id, why)
			}
		}
		domains[variable] = candidates
		m.logf("DOMAIN var=%s type=%s candidates=%v", variable, cp.varType[variable], candidates)
		if len(candidates) == 0 {
			m.logf("MATCH-DONE matches=0 basis=empty domain for var %s", variable)
			return []Match{}, nil
		}
	}

	st := &searchState{
		graph:   g,
		pattern: cp,
		domains: domains,
		binding: map[string]string{},
		used:    map[string]string{},
		ordered: orderVariables(cp, domains),
		seen:    map[string]struct{}{},
		matches: []Match{},
		matcher: m,
	}
	if err := st.backtrack(ctx, 0); err != nil {
		return nil, err
	}

	m.logf("MATCH-DONE matches=%d basis=backtracking exhausted with injectivity + dedup keys", len(st.matches))
	return st.matches, nil
}

type searchState struct {
	graph   *Graph
	pattern *compiledPattern
	domains map[string][]string

	binding map[string]string // variable -> object ID
	used    map[string]string // object ID -> variable (injective binding)

	ordered []string // variables in MRV search order
	seen    map[string]struct{}
	matches []Match
	matcher *Matcher
}

func (s *searchState) backtrack(ctx context.Context, depth int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if depth == len(s.ordered) {
		return s.recordMatch()
	}
	variable := s.ordered[depth]
	for _, candidate := range s.candidates(variable) {
		if _, taken := s.used[candidate]; taken {
			// Distinct variables must map to distinct objects; this is what makes
			// each isomorphic mapping yieldable at most once.
			s.matcher.logf("PRUNE var=%s candidate=%s basis=object already bound to another variable", variable, candidate)
			continue
		}
		if ok, why := s.edgesConsistent(variable, candidate); !ok {
			s.matcher.logf("PRUNE var=%s candidate=%s basis=%s", variable, candidate, why)
			continue
		}
		s.binding[variable] = candidate
		s.used[candidate] = variable
		s.matcher.logf("BIND depth=%d var=%s candidate=%s basis=type+constraints+edges consistent", depth, variable, candidate)
		if err := s.backtrack(ctx, depth+1); err != nil {
			return err
		}
		delete(s.used, candidate)
		delete(s.binding, variable)
	}
	return nil
}

// candidates restricts a variable to adjacency pruning against already-bound
// variables whenever possible, falling back to its type/constraint domain.
func (s *searchState) candidates(variable string) []string {
	domain := s.domains[variable]
	var restricted []string
	for _, edge := range s.pattern.edges {
		if edge.ToVar == variable {
			if fromID, bound := s.binding[edge.FromVar]; bound {
				restricted = intersectSorted(restricted, s.graph.neighborsOut(fromID, edge.Type))
			}
		}
		if edge.FromVar == variable {
			if toID, bound := s.binding[edge.ToVar]; bound {
				restricted = intersectSorted(restricted, s.graph.neighborsIn(toID, edge.Type))
			}
		}
	}
	if restricted == nil {
		return domain
	}
	return intersectSorted(domain, restricted)
}

// edgesConsistent verifies edges whose other endpoint is already bound,
// including self edges (var -e-> var).
func (s *searchState) edgesConsistent(variable, candidate string) (bool, string) {
	for _, edge := range s.pattern.edges {
		switch {
		case edge.FromVar == variable && edge.ToVar == variable:
			if !s.graph.HasEdge(candidate, edge.Type, candidate) {
				return false, "missing self edge " + edge.Type
			}
		case edge.FromVar == variable:
			if toID, bound := s.binding[edge.ToVar]; bound {
				if !s.graph.HasEdge(candidate, edge.Type, toID) {
					return false, "missing edge " + candidate + " -" + edge.Type + "-> " + toID
				}
			}
		case edge.ToVar == variable:
			if fromID, bound := s.binding[edge.FromVar]; bound {
				if !s.graph.HasEdge(fromID, edge.Type, candidate) {
					return false, "missing edge " + fromID + " -" + edge.Type + "-> " + candidate
				}
			}
		}
	}
	return true, "edges consistent"
}

func (s *searchState) recordMatch() error {
	key := matchKey(s.pattern.vars, s.binding)
	if _, dup := s.seen[key]; dup {
		s.matcher.logf("DEDUP drop=%s basis=same object tuple already yielded", key)
		return nil
	}
	s.seen[key] = struct{}{}
	match := Match{}
	for _, variable := range s.pattern.vars {
		match[variable] = s.binding[variable]
	}
	s.matches = append(s.matches, match)
	s.matcher.logf("MATCH accept=%s basis=all %d variables and %d edges satisfied", key, len(s.pattern.vars), len(s.pattern.edges))
	return nil
}

// orderVariables chooses the variable with the smallest domain at each step
// (minimum-remaining-values heuristic), breaking ties by number of edges so
// highly constrained variables are bound first.
func orderVariables(cp *compiledPattern, domains map[string][]string) []string {
	degree := map[string]int{}
	for _, edge := range cp.edges {
		degree[edge.FromVar]++
		if edge.ToVar != edge.FromVar {
			degree[edge.ToVar]++
		}
	}
	remaining := append([]string(nil), cp.vars...)
	ordered := make([]string, 0, len(remaining))
	for len(remaining) > 0 {
		best := 0
		for i := 1; i < len(remaining); i++ {
			ri, rbest := remaining[i], remaining[best]
			if len(domains[ri]) < len(domains[rbest]) ||
				(len(domains[ri]) == len(domains[rbest]) && degree[ri] > degree[rbest]) ||
				(len(domains[ri]) == len(domains[rbest]) && degree[ri] == degree[rbest] && ri < rbest) {
				best = i
			}
		}
		ordered = append(ordered, remaining[best])
		remaining = append(remaining[:best], remaining[best+1:]...)
	}
	return ordered
}

func intersectSorted(a, b []string) []string {
	if a == nil {
		return append([]string(nil), b...)
	}
	var out []string
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			out = append(out, a[i])
			i++
			j++
		case a[i] < b[j]:
			i++
		default:
			j++
		}
	}
	return out
}

func matchKey(vars []string, binding map[string]string) string {
	parts := make([]string, len(vars))
	for i, variable := range vars {
		parts[i] = variable + "=" + binding[variable]
	}
	return strings.Join(parts, ",")
}

func patternString(p Pattern) string {
	nodes := make([]string, len(p.Nodes))
	for i, n := range p.Nodes {
		nodes[i] = n.Var + ":" + n.Type
	}
	edges := make([]string, len(p.Edges))
	for i, e := range p.Edges {
		edges[i] = e.FromVar + "-" + e.Type + "->" + e.ToVar
	}
	return "nodes[" + strings.Join(nodes, ",") + "] edges[" + strings.Join(edges, ",") + "]"
}

func (m *Matcher) logf(format string, args ...any) {
	if m.logger != nil {
		m.logger.Printf(format, args...)
	}
}

// IsRejection reports whether err is one of the distinguishable pattern
// rejection reasons.
func IsRejection(err error) bool {
	return errors.Is(err, ErrDegenerateFullScan) ||
		errors.Is(err, ErrIsomorphicDuplicates) ||
		errors.Is(err, ErrInconsistentBinding)
}

// RejectionKind classifies a rejection error for callers/tests.
func RejectionKind(err error) string {
	switch {
	case errors.Is(err, ErrDegenerateFullScan):
		return "degenerate_full_scan"
	case errors.Is(err, ErrIsomorphicDuplicates):
		return "isomorphic_duplicates"
	case errors.Is(err, ErrInconsistentBinding):
		return "inconsistent_binding"
	default:
		return ""
	}
}
