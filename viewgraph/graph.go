package viewgraph

import (
	"fmt"
	"sort"
	"sync"
)

type viewReg struct {
	name    string
	base    bool
	deps    []string
	compute ComputeFn
	regIdx  int
}

// ComputeFn derives a view value from the current values of its dependencies.
// Implementations must be pure and deterministic: the same inputs must always
// produce the same output.
type ComputeFn func(deps map[string]any) (any, error)

// RecomputeReport summarises one successful recompute round.
type RecomputeReport struct {
	Evaluated []string
	Skipped   []string
}

// Graph is a materialized view DAG. The zero value is not usable; use New.
type Graph struct {
	mu     sync.RWMutex
	views  map[string]*viewReg
	values map[string]any
	dirty  map[string]bool
	rdeps  map[string]map[string]struct{}
	order  []string
}

func New() *Graph {
	return &Graph{
		views:  map[string]*viewReg{},
		values: map[string]any{},
		dirty:  map[string]bool{},
		rdeps:  map[string]map[string]struct{}{},
	}
}

// RegisterBase registers a base view whose value is supplied externally.
// A newly registered base view starts dirty and without a value.
func (g *Graph) RegisterBase(name string) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if name == "" {
		return ErrEmptyName
	}
	if _, ok := g.views[name]; ok {
		return fmt.Errorf("%w: %q", ErrDuplicateName, name)
	}

	v := &viewReg{name: name, base: true, regIdx: len(g.order)}
	g.views[name] = v
	g.order = append(g.order, name)
	g.rdeps[name] = map[string]struct{}{}
	// A newly registered base may satisfy earlier forward references.
	for _, prior := range g.order[:len(g.order)-1] {
		for _, dep := range g.views[prior].deps {
			if dep == name {
				g.rdeps[name][prior] = struct{}{}
			}
		}
	}
	g.dirty[name] = true
	return nil
}

// RegisterView registers a derived view. Dependencies may be registered later
// (forward references are allowed); a still-missing dependency or a cycle is
// rejected only when the graph is topologically used (e.g. Recompute), and
// such a failure leaves every state untouched.
func (g *Graph) RegisterView(name string, deps []string, fn ComputeFn) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if name == "" {
		return ErrEmptyName
	}
	if fn == nil {
		return fmt.Errorf("%w: %q", ErrNilCompute, name)
	}
	if _, ok := g.views[name]; ok {
		return fmt.Errorf("%w: %q", ErrDuplicateName, name)
	}

	uniq := make([]string, 0, len(deps))
	seen := map[string]bool{}
	for _, dep := range deps {
		if dep == "" {
			return fmt.Errorf("%w: dependency of %q", ErrEmptyName, name)
		}
		if !seen[dep] {
			seen[dep] = true
			uniq = append(uniq, dep)
		}
	}

	v := &viewReg{name: name, deps: uniq, compute: fn, regIdx: len(g.order)}

	g.views[name] = v
	g.rdeps[name] = map[string]struct{}{}
	for _, dep := range uniq {
		if _, ok := g.views[dep]; ok {
			g.rdeps[dep][name] = struct{}{}
		}
	}
	// A later-registered view may satisfy existing forward references; wire it.
	for _, prior := range g.order {
		for _, dep := range g.views[prior].deps {
			if dep == name {
				g.rdeps[name][prior] = struct{}{}
			}
		}
	}

	g.order = append(g.order, name)
	g.dirty[name] = true
	return nil
}

// validateLocked checks structural preconditions before any topological use:
// first every dependency must be registered, then the graph must be acyclic.
func (g *Graph) validateLocked() error {
	missing := make([]string, 0)
	for _, name := range g.order {
		for _, dep := range g.views[name].deps {
			if _, ok := g.views[dep]; !ok {
				missing = append(missing, fmt.Sprintf("%s->%s", name, dep))
			}
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("%w: %v", ErrDependencyNotFound, missing)
	}
	return g.checkAcyclicLocked()
}

// checkAcyclicLocked runs Kahn's algorithm over the whole graph. Ties are
// broken by registration order so the result is deterministic. On failure it
// returns the nodes that participate in (or are blocked behind) a cycle.
func (g *Graph) checkAcyclicLocked() error {
	indeg := make(map[string]int, len(g.views))
	for name, v := range g.views {
		indeg[name] = len(v.deps)
	}
	ready := make([]string, 0)
	for name, d := range indeg {
		if d == 0 {
			ready = append(ready, name)
		}
	}
	sortByReg(ready, g.views)

	processed := 0
	for len(ready) > 0 {
		name := ready[0]
		ready = ready[1:]
		processed++
		downstream := make([]string, 0, len(g.rdeps[name]))
		for d := range g.rdeps[name] {
			downstream = append(downstream, d)
		}
		sortByReg(downstream, g.views)
		for _, d := range downstream {
			indeg[d]--
			if indeg[d] == 0 {
				ready = append(ready, d)
				sortByReg(ready, g.views)
			}
		}
	}

	if processed == len(g.views) {
		return nil
	}
	blocked := make([]string, 0)
	for name, d := range indeg {
		if d > 0 {
			blocked = append(blocked, name)
		}
	}
	sortByReg(blocked, g.views)
	return fmt.Errorf("%w: %v", ErrCycle, blocked)
}

func sortByReg(names []string, views map[string]*viewReg) {
	sort.Slice(names, func(i, j int) bool {
		return views[names[i]].regIdx < views[names[j]].regIdx
	})
}

// topoOrderLocked returns dependencies before dependents, registration order
// breaking ties.
func (g *Graph) topoOrderLocked() ([]string, error) {
	if err := g.validateLocked(); err != nil {
		return nil, err
	}
	indeg := make(map[string]int, len(g.views))
	for name, v := range g.views {
		indeg[name] = len(v.deps)
	}
	ready := make([]string, 0)
	for name, d := range indeg {
		if d == 0 {
			ready = append(ready, name)
		}
	}
	sortByReg(ready, g.views)

	result := make([]string, 0, len(g.views))
	for len(ready) > 0 {
		name := ready[0]
		ready = ready[1:]
		result = append(result, name)
		downstream := make([]string, 0, len(g.rdeps[name]))
		for d := range g.rdeps[name] {
			downstream = append(downstream, d)
		}
		sortByReg(downstream, g.views)
		for _, d := range downstream {
			indeg[d]--
			if indeg[d] == 0 {
				ready = append(ready, d)
				sortByReg(ready, g.views)
			}
		}
	}

	return result, nil
}

// markDownstreamDirtyLocked marks every transitive dependent of name dirty.
func (g *Graph) markDownstreamDirtyLocked(name string) {
	queue := []string{name}
	seen := map[string]bool{name: true}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for d := range g.rdeps[cur] {
			if !seen[d] {
				seen[d] = true
				queue = append(queue, d)
			}
			g.dirty[d] = true
		}
	}
}

// Set assigns a new external value to a base view and marks the base together
// with its whole downstream transitive closure dirty. The change is published
// atomically; Recompute must be called before derived views become consistent.
func (g *Graph) Set(name string, value any) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	v, ok := g.views[name]
	if !ok {
		return fmt.Errorf("%w: %q", ErrViewNotFound, name)
	}
	if !v.base {
		return fmt.Errorf("%w: %q", ErrNotBaseView, name)
	}

	g.values[name] = value
	g.dirty[name] = true
	g.markDownstreamDirtyLocked(name)
	return nil
}

// Recompute re-evaluates every dirty derived view in topological order
// (dependencies before dependents). A view invalidated multiple times in the
// same round is evaluated at most once, and clean views are never evaluated.
// On any failure no state changes: values, dirty flags and counts are
// untouched, so callers keep observing the previous complete round.
func (g *Graph) Recompute() (RecomputeReport, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	topo, err := g.topoOrderLocked()
	if err != nil {
		return RecomputeReport{}, err
	}

	targets := make([]string, 0)
	skipped := make([]string, 0)
	for _, name := range topo {
		if g.views[name].base {
			continue
		}
		if g.dirty[name] {
			targets = append(targets, name)
		} else {
			skipped = append(skipped, name)
		}
	}

	for name, v := range g.views {
		if v.base && g.dirty[name] {
			if _, ok := g.values[name]; ok {
				g.dirty[name] = false
			}
		}
	}
	if len(targets) == 0 {
		return RecomputeReport{Skipped: skipped}, nil
	}

	// Stage everything first; commit only after every evaluation succeeds,
	// which also guarantees atomicity of the whole round.
	staged := make(map[string]any, len(targets))
	for _, name := range targets {
		v := g.views[name]
		deps := make(map[string]any, len(v.deps))
		for _, dep := range v.deps {
			if g.views[dep].base || !g.dirty[dep] {
				val, ok := g.values[dep]
				if !ok {
					return RecomputeReport{}, fmt.Errorf("%w: %q (required by %q)", ErrDependencyNotSet, dep, name)
				}
				deps[dep] = val
			} else if val, ok := staged[dep]; ok {
				deps[dep] = val
			} else {
				return RecomputeReport{}, fmt.Errorf("%w: %q (required by %q)", ErrDependencyDirty, dep, name)
			}
		}
		out, evalErr := v.compute(deps)
		if evalErr != nil {
			return RecomputeReport{}, fmt.Errorf("viewgraph: compute %q failed: %w", name, evalErr)
		}
		staged[name] = out
	}

	for name, val := range staged {
		g.values[name] = val
		g.dirty[name] = false
	}
	return RecomputeReport{Evaluated: targets, Skipped: skipped}, nil
}

// Get returns the current value of a view. Dirty derived views (their value
// still belongs to a previous complete recomputation) are reported as
// ErrViewNotSet rather than serving a value inconsistent with their inputs.
func (g *Graph) Get(name string) (any, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	if _, ok := g.views[name]; !ok {
		return nil, fmt.Errorf("%w: %q", ErrViewNotFound, name)
	}
	if g.dirty[name] {
		return nil, fmt.Errorf("%w: %q", ErrViewNotSet, name)
	}
	val, ok := g.values[name]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrViewNotSet, name)
	}
	return val, nil
}

// Snapshot returns a point-in-time copy of all currently consistent values.
// Dirty views are omitted, so the returned map always corresponds to some
// complete recomputation round.
func (g *Graph) Snapshot() map[string]any {
	g.mu.RLock()
	defer g.mu.RUnlock()

	result := make(map[string]any, len(g.values))
	for name, val := range g.values {
		if !g.dirty[name] {
			result[name] = val
		}
	}
	return result
}

// IsDirty reports whether name is marked dirty.
func (g *Graph) IsDirty(name string) (bool, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	if _, ok := g.views[name]; !ok {
		return false, fmt.Errorf("%w: %q", ErrViewNotFound, name)
	}
	return g.dirty[name], nil
}

// RegistrationOrder returns view names in the order they were registered.
func (g *Graph) RegistrationOrder() []string {
	g.mu.RLock()
	defer g.mu.RUnlock()

	out := make([]string, len(g.order))
	copy(out, g.order)
	return out
}

// TopoOrder returns the deterministic topological order (dependencies first),
// breaking ties by registration order.
func (g *Graph) TopoOrder() ([]string, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	out, err := g.topoOrderLocked()
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Verify checks the self-consistency invariant without mutating anything:
// every clean derived view must equal its compute function applied to its
// dependencies' current values. It returns an error describing the first
// divergence, and is safe to call concurrently with queries.
func (g *Graph) Verify() error {
	g.mu.RLock()
	defer g.mu.RUnlock()

	topo, err := g.topoOrderLocked()
	if err != nil {
		return err
	}
	for _, name := range topo {
		v := g.views[name]
		if v.base {
			continue
		}
		if g.dirty[name] {
			continue
		}
		cur, ok := g.values[name]
		if !ok {
			return fmt.Errorf("%w: %q", ErrViewNotSet, name)
		}
		deps := make(map[string]any, len(v.deps))
		for _, dep := range v.deps {
			if g.dirty[dep] {
				return fmt.Errorf("%w: %q (required by %q)", ErrDependencyDirty, dep, name)
			}
			val, ok := g.values[dep]
			if !ok {
				return fmt.Errorf("%w: %q (required by %q)", ErrDependencyNotSet, dep, name)
			}
			deps[dep] = val
		}
		want, evalErr := v.compute(deps)
		if evalErr != nil {
			return fmt.Errorf("viewgraph: verify compute %q failed: %w", name, evalErr)
		}
		if !equalValues(want, cur) {
			return fmt.Errorf("viewgraph: verify mismatch on %q: stored %#v, expected %#v", name, cur, want)
		}
	}
	return nil
}

// FullRecompute recomputes every base-reachable derived view from scratch
// (topological order, each view evaluated exactly once) and returns the
// resulting values without touching graph state. It is intended as an oracle
// to cross-check incremental Recompute results in local verification.
func (g *Graph) FullRecompute() (map[string]any, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	topo, err := g.topoOrderLocked()
	if err != nil {
		return nil, err
	}
	values := make(map[string]any, len(g.views))
	for _, name := range topo {
		v := g.views[name]
		if v.base {
			val, ok := g.values[name]
			if !ok {
				return nil, fmt.Errorf("%w: %q", ErrViewNotSet, name)
			}
			values[name] = val
			continue
		}
		deps := make(map[string]any, len(v.deps))
		for _, dep := range v.deps {
			deps[dep] = values[dep]
		}
		out, evalErr := v.compute(deps)
		if evalErr != nil {
			return nil, fmt.Errorf("viewgraph: compute %q failed: %w", name, evalErr)
		}
		values[name] = out
	}
	return values, nil
}

func equalValues(a, b any) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a == b
}
