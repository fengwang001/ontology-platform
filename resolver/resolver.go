package resolver

import (
	"fmt"
	"sync"
)

// Category is the outcome category of a Resolve call.
type Category int

const (
	// Success: a derivation tree was found.
	Success Category = iota
	// NoInstance: no instance head matched the failing goal.
	NoInstance
	// Ambiguous: candidates existed but none was more specialized than
	// every other candidate.
	Ambiguous
	// DepthExceeded: the failing goal sits at a level greater than D.
	DepthExceeded
	// Cycle: the failing goal already occurs among its ancestor goals.
	Cycle
)

func (c Category) String() string {
	switch c {
	case Success:
		return "success"
	case NoInstance:
		return "no instance"
	case Ambiguous:
		return "ambiguous"
	case DepthExceeded:
		return "depth exceeded"
	case Cycle:
		return "cycle"
	}
	return "unknown"
}

// Constraint is a (trait, type) pair; the type may contain variables.
type Constraint struct {
	Trait string
	Type  Type
}

// Failure describes a failed resolution: the goal at which the first
// failure occurred (depth-first, context constraints in declaration order)
// and its category.
type Failure struct {
	Trait    string
	TypeText string
	Category Category
}

func (f *Failure) String() string {
	return fmt.Sprintf("%s at %s<%s>", f.Category, f.Trait, f.TypeText)
}

// Tree is a derivation tree. Height is 1 for an instance without context,
// otherwise 1 + max child height.
type Tree struct {
	Trait    string
	TypeText string
	Instance int
	Height   int
	Children []*Tree
}

// Result is the outcome of Resolve. Exactly one of Tree (on Success) or
// Failure (otherwise) is non-nil.
type Result struct {
	Category Category
	Tree     *Tree
	Failure  *Failure
}

type instance struct {
	id      int
	trait   string
	head    Type
	context []Constraint
}

// moreSpec reports whether instance a is more specialized than instance b:
// treating a's head variables as pairwise distinct, non-substitutable
// constants, b's head must match a's head.
func moreSpec(a, b instance) bool {
	return matchPattern(b.head, a.head, map[int]Type{})
}

type goalKey struct {
	trait string
	text  string
}

type goal struct {
	trait string
	typ   Type
	key   goalKey
}

func goalOf(trait string, typ Type) goal {
	return goal{trait: trait, typ: typ, key: goalKey{trait: trait, text: keyText(typ)}}
}

type cacheEntry struct {
	typ       Type
	instance  int
	height    int
	childKeys []goalKey
}

type candidate struct {
	inst  instance
	subst map[int]Type
}

// Resolver is a type class instance resolver with an incrementally
// invalidated cache. All methods are safe for concurrent use; the result of
// any interleaving equals some serial order.
type Resolver struct {
	mu          sync.Mutex
	depthLimit  int
	instances   []instance
	cache       map[goalKey]*cacheEntry
	headMatches int64 // unexported counter: head match attempts
}

// NewResolver creates a resolver with derivation depth limit d (1..64).
func NewResolver(d int) (*Resolver, error) {
	if d < minDepthLimit || d > maxDepthLimit {
		return nil, rejectf(RejectInvalidParam, "depth limit %d: must be %d..%d", d, minDepthLimit, maxDepthLimit)
	}
	return &Resolver{depthLimit: d, cache: map[goalKey]*cacheEntry{}}, nil
}

// validateInstance checks the parameters of AddInstance.
func validateInstance(trait string, head Type, ctx []Constraint) error {
	if !validName(trait) {
		return rejectf(RejectInvalidParam, "trait name %q: must be 1..32 bytes", trait)
	}
	if !head.valid() {
		return rejectf(RejectInvalidParam, "invalid head type")
	}
	if len(ctx) > maxContext {
		return rejectf(RejectInvalidParam, "context has %d constraints, at most %d allowed", len(ctx), maxContext)
	}
	headVars := map[int]bool{}
	collectVars(head, headVars)
	for _, c := range ctx {
		if !validName(c.Trait) {
			return rejectf(RejectInvalidParam, "context trait name %q: must be 1..32 bytes", c.Trait)
		}
		if !c.Type.valid() {
			return rejectf(RejectInvalidParam, "invalid context type in trait %q", c.Trait)
		}
		vars := map[int]bool{}
		collectVars(c.Type, vars)
		for v := range vars {
			if !headVars[v] {
				return rejectf(RejectInvalidParam, "context variable %d of trait %q does not occur in the head", v, c.Trait)
			}
		}
	}
	return nil
}

func findDuplicate(instances []instance, trait string, head Type) bool {
	for _, inst := range instances {
		if inst.trait == trait && alphaEqual(inst.head, head) {
			return true
		}
	}
	return false
}

// AddInstance registers an instance for trait with the given head type and
// context constraints, returning its 1-based instance number. On rejection
// (invalid parameters, duplicate head up to variable renaming, or more than
// 200 instances) no state changes.
func (r *Resolver) AddInstance(trait string, head Type, ctx []Constraint) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := validateInstance(trait, head, ctx); err != nil {
		return 0, err
	}
	if findDuplicate(r.instances, trait, head) {
		return 0, rejectf(RejectDuplicateInstance, "trait %q head %s duplicates an existing instance", trait, head)
	}
	if len(r.instances) >= maxInstances {
		return 0, rejectf(RejectTooManyInstances, "at most %d instances allowed", maxInstances)
	}
	inst := instance{id: len(r.instances) + 1, trait: trait, head: head, context: append([]Constraint(nil), ctx...)}
	r.instances = append(r.instances, inst)
	r.invalidate(inst)
	return inst.id, nil
}

// invalidate drops exactly the cache entries whose re-resolution must
// change after inst was added: a cached goal g with selected instance S is
// doomed iff inst has the same trait as g, inst's head matches g's type,
// and S is not more specialized than inst. Doom propagates to every entry
// whose tree contains a doomed goal (never towards subgoals).
func (r *Resolver) invalidate(inst instance) {
	doomed := map[goalKey]bool{}
	for k, e := range r.cache {
		if inst.trait != k.trait {
			continue
		}
		if !matchPattern(inst.head, e.typ, map[int]Type{}) {
			continue
		}
		sel := r.instances[e.instance-1]
		if !moreSpec(sel, inst) {
			doomed[k] = true
		}
	}
	for changed := true; changed; {
		changed = false
		for k, e := range r.cache {
			if doomed[k] {
				continue
			}
			for _, ck := range e.childKeys {
				if doomed[ck] {
					doomed[k] = true
					changed = true
					break
				}
			}
		}
	}
	for k := range doomed {
		delete(r.cache, k)
	}
}

// Resolve computes the derivation tree for the goal (trait, typ). The type
// must be ground and of depth at most 16; invalid parameters are rejected
// without changing any state.
func (r *Resolver) Resolve(trait string, typ Type) (Result, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !validName(trait) {
		return Result{}, rejectf(RejectInvalidParam, "trait name %q: must be 1..32 bytes", trait)
	}
	if !typ.valid() {
		return Result{}, rejectf(RejectInvalidParam, "invalid type")
	}
	if !typ.IsGround() {
		return Result{}, rejectf(RejectInvalidParam, "type %s contains variables", typ)
	}
	if typ.Depth() > maxTypeDepth {
		return Result{}, rejectf(RejectInvalidParam, "type %s has depth %d, at most %d allowed", typ, typ.Depth(), maxTypeDepth)
	}
	tree, failure := r.resolve(goalOf(trait, typ), 1, nil, true)
	if failure != nil {
		return Result{Category: failure.Category, Failure: failure}, nil
	}
	return Result{Category: Success, Tree: tree}, nil
}

// resolve solves one goal at the given level (top level is 1) with path
// holding the ancestor goal keys. Cycle is checked first, then the depth
// limit, then the cache, then instance selection.
func (r *Resolver) resolve(g goal, level int, path []goalKey, useCache bool) (*Tree, *Failure) {
	for _, anc := range path {
		if anc == g.key {
			return nil, &Failure{Trait: g.trait, TypeText: g.typ.String(), Category: Cycle}
		}
	}
	if level > r.depthLimit {
		return nil, &Failure{Trait: g.trait, TypeText: g.typ.String(), Category: DepthExceeded}
	}
	if useCache {
		if e, ok := r.cache[g.key]; ok && level+e.height-1 <= r.depthLimit {
			return r.buildTree(g, e), nil
		}
	}
	var cands []candidate
	for _, inst := range r.instances {
		if inst.trait != g.trait {
			continue
		}
		r.headMatches++
		subst := map[int]Type{}
		if matchPattern(inst.head, g.typ, subst) {
			cands = append(cands, candidate{inst: inst, subst: subst})
		}
	}
	if len(cands) == 0 {
		return nil, &Failure{Trait: g.trait, TypeText: g.typ.String(), Category: NoInstance}
	}
	sel := -1
	for i := range cands {
		most := true
		for j := range cands {
			if i != j && !moreSpec(cands[i].inst, cands[j].inst) {
				most = false
				break
			}
		}
		if most {
			sel = i
			break
		}
	}
	if sel < 0 {
		return nil, &Failure{Trait: g.trait, TypeText: g.typ.String(), Category: Ambiguous}
	}
	// The unique most specialized instance is selected; no backtracking
	// over other candidates or the context happens afterwards.
	c := cands[sel]
	childPath := make([]goalKey, len(path)+1)
	copy(childPath, path)
	childPath[len(path)] = g.key
	var children []*Tree
	var childKeys []goalKey
	maxChild := 0
	for _, con := range c.inst.context {
		childType := applySubst(con.Type, c.subst)
		child := goalOf(con.Trait, childType)
		childTree, failure := r.resolve(child, level+1, childPath, useCache)
		if failure != nil {
			return nil, failure
		}
		children = append(children, childTree)
		childKeys = append(childKeys, child.key)
		if childTree.Height > maxChild {
			maxChild = childTree.Height
		}
	}
	tree := &Tree{
		Trait:    g.trait,
		TypeText: g.typ.String(),
		Instance: c.inst.id,
		Height:   maxChild + 1,
		Children: children,
	}
	if useCache {
		r.cache[g.key] = &cacheEntry{
			typ:       g.typ,
			instance:  c.inst.id,
			height:    tree.Height,
			childKeys: childKeys,
		}
	}
	return tree, nil
}

// buildTree reconstructs a derivation tree from cache entries. All
// descendants of a surviving entry are guaranteed to be present because
// invalidation propagates towards dependents.
func (r *Resolver) buildTree(g goal, e *cacheEntry) *Tree {
	tree := &Tree{
		Trait:    g.trait,
		TypeText: g.typ.String(),
		Instance: e.instance,
		Height:   e.height,
	}
	for _, ck := range e.childKeys {
		ce := r.cache[ck]
		if ce == nil {
			// Unreachable: invalidation propagates towards dependents,
			// so every descendant of a surviving entry survives.
			continue
		}
		tree.Children = append(tree.Children, r.buildTree(goal{trait: ck.trait, typ: ce.typ, key: ck}, ce))
	}
	return tree
}

// VerifyCacheConsistency re-resolves every cached goal without using the
// cache and reports an error if any entry disagrees with the cache-free
// result. It does not mutate the cache or the head-match counter.
func (r *Resolver) VerifyCacheConsistency() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	saved := r.headMatches
	defer func() { r.headMatches = saved }()
	for k, e := range r.cache {
		g := goal{trait: k.trait, typ: e.typ, key: k}
		cached := r.buildTree(g, e)
		fresh, failure := r.resolve(g, 1, nil, false)
		if failure != nil {
			return fmt.Errorf("cache entry %s<%s> (instance %d) but cache-free resolution fails: %s",
				k.trait, e.typ, e.instance, failure)
		}
		if !treeEqual(cached, fresh) {
			return fmt.Errorf("cache entry %s<%s> disagrees with cache-free resolution", k.trait, e.typ)
		}
	}
	return nil
}

func treeEqual(a, b *Tree) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.Trait != b.Trait || a.TypeText != b.TypeText || a.Instance != b.Instance ||
		a.Height != b.Height || len(a.Children) != len(b.Children) {
		return false
	}
	for i := range a.Children {
		if !treeEqual(a.Children[i], b.Children[i]) {
			return false
		}
	}
	return true
}
