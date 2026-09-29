package graphmatch

import (
	"fmt"
	"log/slog"
	"sort"
)

// Matcher runs pattern queries against a graph. It is stateless and safe for
// concurrent use.
type Matcher struct {
	logger *slog.Logger
}

// NewMatcher returns a Matcher logging to logger; nil uses slog.Default().
func NewMatcher(logger *slog.Logger) *Matcher {
	if logger == nil {
		logger = slog.Default()
	}
	return &Matcher{logger: logger}
}

// Match returns all matches of p in g. Isomorphic matches (variable mappings
// onto the same set of objects) are returned once; each variable is bound to
// exactly one object. A rejected query returns a *RejectError and nil
// results — never partial results.
func (m *Matcher) Match(g *Graph, p *Pattern) ([]Match, error) {
	log := m.logger
	log.Info("match start", "pattern", p.String())

	vars, err := p.validate()
	if err != nil {
		return nil, err
	}

	// Reject patterns that would degrade to a full Cartesian search: every
	// variable must carry at least one pruning constraint.
	for v := range vars {
		if !p.constrained(vars, v) {
			rej := &RejectError{
				Reason:  ReasonFullSearchDegradation,
				Detail:  fmt.Sprintf("variable %q has no type, property or edge constraint", v),
				Pattern: p.String(),
			}
			log.Warn("match rejected", "reason", string(rej.Reason), "detail", rej.Detail, "pattern", rej.Pattern)
			return nil, rej
		}
	}

	snap := g.snapshot()

	// Candidate sets per variable, filtered by node type and properties.
	cands := make(map[string][]Object, len(vars))
	for v, n := range vars {
		for _, o := range snap.objects {
			if nodeMatches(n, o) {
				cands[v] = append(cands[v], o)
			}
		}
		log.Info("candidates computed", "var", v, "type", n.Type, "props", slog.Any("props", n.Props), "candidates", len(cands[v]))
		if len(cands[v]) == 0 {
			log.Info("match done: empty candidate set", "var", v)
			return nil, nil
		}
	}

	// Bind the most constrained variable first; ties break by name so the
	// search order is deterministic.
	order := make([]string, 0, len(vars))
	for v := range vars {
		order = append(order, v)
	}
	sort.Slice(order, func(i, j int) bool {
		if len(cands[order[i]]) != len(cands[order[j]]) {
			return len(cands[order[i]]) < len(cands[order[j]])
		}
		return order[i] < order[j]
	})

	var (
		binding = make(map[string]string, len(vars))
		used    = make(map[string]string, len(vars)) // objectID -> variable (injectivity)
		matches []Match
		pruned  int
	)

	var visit func(idx int)
	visit = func(idx int) {
		if idx == len(order) {
			matches = append(matches, Match{Bindings: copyBinding(binding)})
			return
		}
		v := order[idx]
		for _, cand := range cands[v] {
			if _, taken := used[cand.ID]; taken {
				pruned++
				continue // injective binding: one object per variable
			}
			if ok, basis := edgesSatisfied(p, v, cand.ID, binding, snap); !ok {
				pruned++
				log.Debug("candidate pruned", "var", v, "object", cand.ID, "basis", basis)
				continue
			}
			binding[v] = cand.ID
			used[cand.ID] = v
			visit(idx + 1)
			delete(binding, v)
			delete(used, cand.ID)
		}
	}
	visit(0)

	// Isomorphic dedup: keep the first (deterministically ordered) match per
	// object set.
	seen := make(map[string]struct{}, len(matches))
	deduped := matches[:0]
	for _, mt := range matches {
		key := mt.isoKey()
		if _, dup := seen[key]; dup {
			log.Info("isomorphic duplicate dropped", "isoKey", key, "binding", mt.canonicalKey())
			continue
		}
		seen[key] = struct{}{}
		deduped = append(deduped, mt)
	}

	// Integrity gate: reject the whole query rather than return partial or
	// inconsistent results.
	if err := VerifyMatches(p, deduped); err != nil {
		log.Warn("match rejected", "reason", string(err.Reason), "detail", err.Detail, "pattern", err.Pattern)
		return nil, err
	}

	sort.Slice(deduped, func(i, j int) bool { return deduped[i].canonicalKey() < deduped[j].canonicalKey() })
	for _, mt := range deduped {
		log.Info("match found",
			"binding", mt.canonicalKey(),
			"isoKey", mt.isoKey(),
			"basis", matchBasis(p, mt, snap),
		)
	}
	log.Info("match done", "pattern", p.String(), "matches", len(deduped), "prunedCandidates", pruned)
	return deduped, nil
}

// VerifyMatches checks a result set for the rejection conditions: isomorphic
// duplicates, inconsistent variable bindings, and unsatisfied constraints.
// It is used internally as a final integrity gate and is exported so callers
// can audit results from other sources.
func VerifyMatches(p *Pattern, ms []Match) *RejectError {
	vars, verr := p.validate()
	if verr != nil {
		return &RejectError{Reason: ReasonInconsistentBinding, Detail: verr.Error(), Pattern: p.String()}
	}
	seen := make(map[string]struct{}, len(ms))
	for _, mt := range ms {
		if len(mt.Bindings) != len(vars) {
			return &RejectError{
				Reason:  ReasonInconsistentBinding,
				Detail:  fmt.Sprintf("match binds %d variables, pattern declares %d", len(mt.Bindings), len(vars)),
				Pattern: p.String(),
			}
		}
		usedBy := make(map[string]string, len(mt.Bindings))
		for v := range vars {
			id, ok := mt.Bindings[v]
			if !ok {
				return &RejectError{Reason: ReasonInconsistentBinding, Detail: fmt.Sprintf("variable %q is unbound", v), Pattern: p.String()}
			}
			if prev, dup := usedBy[id]; dup {
				return &RejectError{
					Reason:  ReasonInconsistentBinding,
					Detail:  fmt.Sprintf("variables %q and %q both bind object %q", prev, v, id),
					Pattern: p.String(),
				}
			}
			usedBy[id] = v
		}
		key := mt.isoKey()
		if _, dup := seen[key]; dup {
			return &RejectError{
				Reason:  ReasonDuplicateIsomorphicMatch,
				Detail:  fmt.Sprintf("two matches map to the same object set %q", key),
				Pattern: p.String(),
			}
		}
		seen[key] = struct{}{}
	}
	return nil
}

func copyBinding(b map[string]string) map[string]string {
	cp := make(map[string]string, len(b))
	for k, v := range b {
		cp[k] = v
	}
	return cp
}

func nodeMatches(n NodePattern, o Object) bool {
	if n.Type != "" && o.Type != n.Type {
		return false
	}
	for k, v := range n.Props {
		if o.Props[k] != v {
			return false
		}
	}
	return true
}

// edgesSatisfied checks every pattern edge incident to v whose other
// endpoint is already bound. It reports the first failing edge as the
// pruning basis.
func edgesSatisfied(p *Pattern, v, objID string, binding map[string]string, snap *snapshot) (bool, string) {
	for _, e := range p.Edges {
		switch {
		case e.FromVar == v && e.ToVar == v:
			if !hasEdge(snap.out[objID], objID, e.Type) {
				return false, fmt.Sprintf("missing self-edge %s -%s-> %s", objID, e.Type, objID)
			}
		case e.FromVar == v:
			other, bound := binding[e.ToVar]
			if bound && !hasEdge(snap.out[objID], other, e.Type) {
				return false, fmt.Sprintf("missing edge %s -%s-> %s", objID, e.Type, other)
			}
		case e.ToVar == v:
			other, bound := binding[e.FromVar]
			if bound && !hasEdge(snap.in[objID], other, e.Type) {
				return false, fmt.Sprintf("missing edge %s -%s-> %s", other, e.Type, objID)
			}
		}
	}
	return true, ""
}

func hasEdge(edges []Edge, other, edgeType string) bool {
	for _, e := range edges {
		if e.Type == edgeType && (e.From == other || e.To == other) {
			return true
		}
	}
	return false
}

// matchBasis explains why a match holds: node constraints and edge evidence.
func matchBasis(p *Pattern, mt Match, snap *snapshot) string {
	vars, err := p.validate()
	if err != nil {
		return err.Error()
	}
	var parts []string
	for _, v := range sortedVars(vars) {
		o := snap.byID[mt.Bindings[v]]
		parts = append(parts, fmt.Sprintf("%s=%s(type=%s)", v, o.ID, o.Type))
	}
	for _, e := range p.Edges {
		parts = append(parts, fmt.Sprintf("edge %s -%s-> %s", mt.Bindings[e.FromVar], e.Type, mt.Bindings[e.ToVar]))
	}
	return fmt.Sprintf("%v", parts)
}

func sortedVars(vars map[string]NodePattern) []string {
	out := make([]string, 0, len(vars))
	for v := range vars {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}
