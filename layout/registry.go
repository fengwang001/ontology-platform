package layout

import (
	"fmt"
	"sort"
	"sync"
)

// entry is the mutable per-type record guarded by Registry.mu.
type entry struct {
	spec      CompositeSpec
	layout    *Layout
	basic     bool
	size      int             // basic types only
	align     int             // basic types only
	direct    map[string]bool // types this type directly embeds
	deps      map[string]bool // composite types directly embedded by this one
	depend    map[string]bool // reverse edge: types directly embedding this one
	version   int
	recompute int
}

// ModifyResult reports the outcome of a Modify call. Recomputed lists, in
// topological order, every dependent whose layout was recomputed through
// propagation. Root is the verdict for the modified type itself; Verdicts holds
// the verdict for every recomputed dependent.
type ModifyResult struct {
	Root       CompatKind
	RootReason string
	Recomputed []string
	Verdicts   map[string]CompatKind
}

// Registry is the concurrency-safe type registry. All operations are
// serialized on one RWMutex, so concurrent clients observe an effect
// equivalent to some total serial order; a snapshot read under RLock returns
// fields, size and version from the same instant.
type Registry struct {
	mu     sync.RWMutex
	cfg    Config
	types  map[string]*entry
	logger Logger
}

// NewRegistry constructs an empty registry with the given configuration and
// optional logger.
func NewRegistry(cfg Config, logger Logger) (*Registry, error) {
	if !cfg.valid() {
		return nil, fmt.Errorf("layout: invalid configuration")
	}
	return &Registry{cfg: cfg, types: make(map[string]*entry), logger: logger}, nil
}

func (r *Registry) logf(format string, args ...any) {
	if r.logger != nil {
		r.logger.Logf(format, args...)
	}
}

// RegisterBasic registers a primitive type with fixed size and alignment.
// The alignment must be allowed. A rejected registration leaves no record.
func (r *Registry) RegisterBasic(name string, size, align int) (err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if name == "" {
		err = ErrEmptyName
		r.logf("RegisterBasic(%q size=%d align=%d) -> error: %v", name, size, align, err)
		return err
	}
	if _, exists := r.types[name]; exists {
		err = ErrDuplicateType
		r.logf("RegisterBasic(%q size=%d align=%d) -> error: %v", name, size, align, err)
		return err
	}
	if size <= 0 {
		err = ErrSizeExceeded
		r.logf("RegisterBasic(%q size=%d align=%d) -> error: non-positive size", name, size, align)
		return err
	}
	if !r.cfg.allowed(align) {
		err = ErrInvalidAlignment
		r.logf("RegisterBasic(%q size=%d align=%d) -> error: %v", name, size, align, err)
		return err
	}
	if size > r.cfg.MaxSize {
		err = ErrSizeExceeded
		r.logf("RegisterBasic(%q size=%d align=%d) -> error: %v", name, size, align, err)
		return err
	}
	r.types[name] = &entry{
		basic:   true,
		size:    size,
		align:   align,
		direct:  map[string]bool{},
		deps:    map[string]bool{},
		depend:  map[string]bool{},
		version: 1,
	}
	r.logf("RegisterBasic(%q size=%d align=%d) -> ok version=1", name, size, align)
	return nil
}

// Register registers a new composite type. Validation follows the fixed
// rejection order: undefined target > illegal alignment > duplicate field >
// embedding cycle > size limit. A rejected registration leaves no record.
func (r *Registry) Register(spec CompositeSpec) (err error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	specs := make(map[string]CompositeSpec, len(r.types)+1)
	for n, e := range r.types {
		if !e.basic {
			specs[n] = e.spec
		}
	}
	specs[spec.Name] = spec
	if err = r.validate(spec, specs, true); err != nil {
		r.logf("Register(%q fields=%d) -> error: %v", spec.Name, len(spec.Fields), err)
		return err
	}

	ly, err := computeLayout(r.cfg, spec, func(name string) *Layout {
		if e, ok := r.types[name]; ok {
			if e.basic {
				return &Layout{Name: name, Size: e.size, Align: e.align, Version: e.version}
			}
			return cloneLayout(e.layout)
		}
		return nil
	})
	if err != nil {
		r.logf("Register(%q fields=%d) -> error: %v", spec.Name, len(spec.Fields), err)
		return err
	}

	ent := &entry{
		spec:    spec,
		layout:  ly,
		direct:  embeddedNames(spec),
		deps:    map[string]bool{},
		depend:  map[string]bool{},
		version: 1,
	}
	for d := range ent.direct {
		ent.deps[d] = true
		r.types[d].depend[spec.Name] = true
	}
	r.types[spec.Name] = ent
	r.logf("Register(%q fields=%d) -> ok size=%d align=%d version=1", spec.Name, len(spec.Fields), ly.Size, ly.Align)
	return nil
}

// validate checks one spec against the rejection order. specs is the graph of
// composite declarations including the candidate (used for cycle checks).
// registering distinguishes registration from modification for the
// duplicate-name check.
func (r *Registry) validate(spec CompositeSpec, specs map[string]CompositeSpec, registering bool) error {
	if spec.Name == "" {
		return ErrEmptyName
	}
	if registering {
		if _, exists := r.types[spec.Name]; exists {
			return ErrDuplicateType
		}
	} else if _, exists := r.types[spec.Name]; !exists {
		return ErrUndefinedType
	}
	// Pass 1, declaration order: undefined embedded targets win over every
	// other error class.
	for _, f := range spec.Fields {
		if f.Name == "" {
			return ErrEmptyName
		}
		if !f.Indirect {
			if _, ok := r.types[f.TypeName]; !ok {
				if f.TypeName != spec.Name {
					return fmt.Errorf("%w: %s", ErrUndefinedType, f.TypeName)
				}
				return fmt.Errorf("%w: %s", ErrEmbeddingCycle, spec.Name)
			}
		}
	}
	// Alignment errors (including the type-wide cap) precede duplicate names.
	if spec.MaxAlign != 0 && !r.cfg.allowed(spec.MaxAlign) {
		return ErrInvalidAlignment
	}
	seen := map[string]bool{}
	for _, f := range spec.Fields {
		if !f.Indirect && !r.cfg.allowed(r.types[f.TypeName].layoutOrAlign()) {
			return ErrInvalidAlignment
		}
		if seen[f.Name] {
			return fmt.Errorf("%w: %s", ErrDuplicateField, f.Name)
		}
		seen[f.Name] = true
	}
	if hasEmbeddingCycle(spec.Name, specs) {
		return fmt.Errorf("%w: %s", ErrEmbeddingCycle, spec.Name)
	}
	return nil
}

// Modify replaces the declaration of an existing composite type, producing a
// new version and propagating layout recomputation to direct embedders.
//
// Propagation visits only types reachable through direct-embedding edges from
// the modified type; indirect referrers are untouched. A dependent whose
// recomputed layout is fully compatible stops propagation up that branch.
// All candidate layouts are computed before any state changes: if any
// dependent would exceed MaxSize the whole modification is rejected with
// ErrDependentOversize and every type keeps its previous layout.
func (r *Registry) Modify(spec CompositeSpec) (res ModifyResult, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	specs := make(map[string]CompositeSpec, len(r.types))
	for n, e := range r.types {
		if !e.basic {
			specs[n] = e.spec
		}
	}
	specs[spec.Name] = spec
	if err = r.validate(cloneSpec(spec), specs, false); err != nil {
		r.logf("Modify(%q fields=%d) -> error: %v", spec.Name, len(spec.Fields), err)
		return ModifyResult{}, err
	}

	root, ok := r.types[spec.Name]
	if !ok {
		return ModifyResult{}, fmt.Errorf("%w: %s", ErrUndefinedType, spec.Name)
	}
	newLayout, err := computeLayout(r.cfg, spec, r.lookupForCandidate(spec.Name, nil))
	if err != nil {
		r.logf("Modify(%q) -> error: %v", spec.Name, err)
		return ModifyResult{}, err
	}
	kind, reason := classify(root.layout, newLayout)
	res.Root, res.RootReason = kind, reason

	// Fully compatible at the root: only the declaration/edges change; no
	// dependent's inputs changed numerically, so propagation stops here.
	if kind == FullyCompatible {
		r.commitRoot(spec, newLayout, false)
		r.logf("Modify(%q) -> %s (%s); propagation stops", spec.Name, kind, reason)
		return res, nil
	}

	// Build the set of types reachable from the root through embedding edges.
	affected := map[string]bool{spec.Name: true}
	frontier := []string{spec.Name}
	for len(frontier) > 0 {
		cur := frontier[len(frontier)-1]
		frontier = frontier[:len(frontier)-1]
		for d := range r.types[cur].depend {
			if !affected[d] {
				affected[d] = true
				frontier = append(frontier, d)
			}
		}
	}

	// Topologically order affected nodes using current embedding edges
	// (Kahn). Nodes outside the affected set are already-computed predecessors.
	indeg := make(map[string]int, len(affected))
	successors := make(map[string][]string, len(affected))
	for n := range affected {
		e := r.specOf(specs, n)
		for dep := range embeddedNames(e) {
			if affected[dep] {
				indeg[n]++
				successors[dep] = append(successors[dep], n)
			}
		}
	}
	// Deterministic order for logging and for Recomputed.
	ready := []string{}
	for n := range affected {
		if n != spec.Name && indeg[n] == 0 {
			ready = append(ready, n)
		}
	}
	sort.Strings(ready)
	order := []string{spec.Name}
	// Seed: the root itself is already placed, so consume its outgoing edges.
	rootSucc := append([]string(nil), successors[spec.Name]...)
	sort.Strings(rootSucc)
	for _, s := range rootSucc {
		indeg[s]--
		if indeg[s] == 0 {
			ready = append(ready, s)
		}
	}
	for len(ready) > 0 {
		n := ready[0]
		ready = ready[1:]
		order = append(order, n)
		succ := append([]string(nil), successors[n]...)
		sort.Strings(succ)
		for _, s := range succ {
			indeg[s]--
			if indeg[s] == 0 {
				ready = append(ready, s)
			}
		}
	}

	// Evaluate candidates in topological order. newed holds layouts that
	// actually differ; a node is evaluated only if it depends on one of them,
	// which makes a fully-compatible level the propagation stop.
	newed := map[string]*Layout{spec.Name: newLayout}
	res.Verdicts = map[string]CompatKind{}
	for oi := 1; oi < len(order); oi++ {
		n := order[oi]
		nspec := r.specOf(specs, n)
		// Is any of this node's dependencies carrying a live change?
		live := false
		for dep := range embeddedNames(nspec) {
			if newed[dep] != nil {
				live = true
			}
		}
		if !live {
			continue
		}
		cand, err := computeLayout(r.cfg, nspec, r.lookupForCandidate(spec.Name, newed))
		if err != nil {
			r.logf("Modify(%q) -> rejected: dependent %q %v; all layouts kept", spec.Name, n, err)
			return ModifyResult{}, fmt.Errorf("%w: %s: %v", ErrDependentOversize, n, err)
		}
		k, why := classify(r.types[n].layout, cand)
		res.Verdicts[n] = k
		if k == FullyCompatible && layoutsEqual(r.types[n].layout, cand) {
			r.logf("Modify(%q): dependent %q fully compatible (%s); propagation stops here", spec.Name, n, why)
			continue
		}
		newed[n] = cand
		res.Recomputed = append(res.Recomputed, n)
	}

	// Commit: root first, then each recomputed dependent. Only then are
	// counters and edges updated, so a failure above leaves state untouched.
	r.commitRoot(spec, newLayout, true)
	for _, n := range res.Recomputed {
		e := r.types[n]
		e.layout = newed[n]
		e.version++
		e.recompute++
	}
	r.logf("Modify(%q) -> root %s (%s); recomputed %v", spec.Name, res.Root, res.RootReason, res.Recomputed)
	return res, nil
}

// specOf returns a node's prospective spec (the candidate for root).
func (r *Registry) specOf(specs map[string]CompositeSpec, n string) CompositeSpec {
	return specs[n]
}

// lookupForCandidate resolves embedded-dependency layouts during
// recomputation: candidates override committed layouts; everything else is
// read from the registry. The lookup cost per field is O(1) map access.
func (r *Registry) lookupForCandidate(rootName string, candidates map[string]*Layout) func(string) *Layout {
	return func(name string) *Layout {
		if candidates != nil {
			if l, ok := candidates[name]; ok {
				return cloneLayout(l)
			}
		}
		if e, ok := r.types[name]; ok {
			if e.basic {
				return &Layout{Name: name, Size: e.size, Align: e.align, Version: e.version}
			}
			return cloneLayout(e.layout)
		}
		return nil
	}
}

// commitRoot installs the new root spec/layout and refreshes its dependency
// edges. bumped indicates the root layout changed (version already handled by
// the caller's verdict: append/incompatible bump here, full does not).
func (r *Registry) commitRoot(spec CompositeSpec, ly *Layout, changed bool) {
	e := r.types[spec.Name]
	newDeps := embeddedNames(spec)
	for d := range e.direct {
		if !newDeps[d] {
			delete(r.types[d].depend, spec.Name)
		}
	}
	for d := range newDeps {
		if !e.direct[d] {
			r.types[d].depend[spec.Name] = true
		}
	}
	e.spec = cloneSpec(spec)
	e.layout = ly
	e.direct = newDeps
	e.deps = copySet(newDeps)
	if changed {
		e.version++
		e.recompute++
	}
}

func copySet(m map[string]bool) map[string]bool {
	c := make(map[string]bool, len(m))
	for k := range m {
		c[k] = true
	}
	return c
}

// layoutsEqual reports whether two layouts agree on size, alignment and the
// identity and placement of every field.
func layoutsEqual(a, b *Layout) bool {
	if a.Size != b.Size || a.Align != b.Align || len(a.Fields) != len(b.Fields) {
		return false
	}
	for i := range a.Fields {
		x, y := a.Fields[i], b.Fields[i]
		if x != y {
			return false
		}
	}
	return true
}

// layoutOrAlign returns the alignment contributed by an embedded target.
func (e *entry) layoutOrAlign() int {
	if e.basic {
		return e.align
	}
	return e.layout.Align
}

func embeddedNames(spec CompositeSpec) map[string]bool {
	m := map[string]bool{}
	for _, f := range spec.Fields {
		if !f.Indirect {
			m[f.TypeName] = true
		}
	}
	return m
}

// hasEmbeddingCycle reports whether following direct-embedding edges from
// root reaches root again, treating specs as the prospective graph.
func hasEmbeddingCycle(root string, specs map[string]CompositeSpec) bool {
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := map[string]int{}
	var visit func(string) bool
	visit = func(n string) bool {
		if color[n] == black {
			return false
		}
		color[n] = gray
		s, ok := specs[n]
		if ok {
			for d := range embeddedNames(s) {
				if d == root {
					return true
				}
				if color[d] == gray {
					return true
				}
				if visit(d) {
					return true
				}
			}
		}
		color[n] = black
		return false
	}
	s := specs[root]
	for d := range embeddedNames(s) {
		if d == root {
			return true
		}
		if visit(d) {
			return true
		}
	}
	return false
}

func cloneLayout(l *Layout) *Layout {
	cp := *l
	cp.Fields = append([]FieldLayout(nil), l.Fields...)
	return &cp
}

// cloneSpec copies a spec so callers cannot mutate registry state through
// retained slices.
func cloneSpec(s CompositeSpec) CompositeSpec {
	s.Fields = append([]Field(nil), s.Fields...)
	return s
}

// Get returns an immutable copy of the type's current layout. The copy is
// taken under the read lock, so offsets, size and version share one instant.
func (r *Registry) Get(name string) (*Layout, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.types[name]
	if !ok {
		err := fmt.Errorf("%w: %s", ErrUndefinedType, name)
		r.logf("Get(%q) -> error: %v", name, err)
		return nil, err
	}
	if e.basic {
		l := &Layout{Name: name, Size: e.size, Align: e.align, Version: e.version}
		r.logf("Get(%q) -> basic size=%d align=%d version=%d", name, l.Size, l.Align, l.Version)
		return l, nil
	}
	l := cloneLayout(e.layout)
	l.Version = e.version
	r.logf("Get(%q) -> size=%d align=%d version=%d fields=%d", name, l.Size, l.Align, l.Version, len(l.Fields))
	return l, nil
}

// View returns the consistent read-only view of one type.
func (r *Registry) View(name string) (Snapshot, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.types[name]
	if !ok {
		return Snapshot{}, fmt.Errorf("%w: %s", ErrUndefinedType, name)
	}
	snap := Snapshot{Name: name, Version: e.version, Dependents: len(e.depend), RecomputeCount: e.recompute}
	if e.basic {
		snap.Size, snap.Align = e.size, e.align
	} else {
		snap.Size, snap.Align = e.layout.Size, e.layout.Align
	}
	r.logf("View(%q) -> v=%d size=%d align=%d dependents=%d recomputes=%d",
		name, snap.Version, snap.Size, snap.Align, snap.Dependents, snap.RecomputeCount)
	return snap, nil
}

// Names returns all registered type names, sorted.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.types))
	for n := range r.types {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
