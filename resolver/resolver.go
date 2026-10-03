package resolver

import (
	"fmt"
	"sync"
)

// Instance is a registered type-class instance: a trait, a head type that
// may contain variables, and a context of at most MaxContext constraints
// whose variables must all occur in the head.
type Instance struct {
	ID      int
	Trait   string
	Head    *Type
	Context []Constraint
}

// Tree is a successful derivation. Children are the derivations of the
// selected instance's context constraints in declaration order.
type Tree struct {
	Trait      string
	Type       string
	InstanceID int
	Children   []*Tree
}

// Height of a derivation tree: 1 for an instance without context,
// otherwise 1 plus the maximum height of the subtrees.
func (t *Tree) Height() int {
	maxChild := 0
	for _, c := range t.Children {
		if h := c.Height(); h > maxChild {
			maxChild = h
		}
	}
	return maxChild + 1
}

func (t *Tree) String() string {
	s := fmt.Sprintf("%s<%s>#%d", t.Trait, t.Type, t.InstanceID)
	if len(t.Children) > 0 {
		s += "("
		for i, c := range t.Children {
			if i > 0 {
				s += ","
			}
			s += c.String()
		}
		s += ")"
	}
	return s
}

// goalKey identifies a resolution goal: a trait and a ground type. The
// canonical type text is unique per ground type.
type goalKey struct {
	trait string
	typ   string
}

// cacheEntry records a successfully resolved goal: the selected instance,
// the tree height, and the keys of the direct subgoals.
type cacheEntry struct {
	typ        *Type
	instanceID int
	height     int
	children   []goalKey
}

// Resolver resolves type-class constraints against a set of instances,
// caching successful goals and incrementally invalidating the cache when
// new instances are registered. All methods are safe for concurrent use;
// the result is equivalent to some serial order.
type Resolver struct {
	mu            sync.Mutex
	depthLimit    int
	instances     []*Instance
	cache         map[goalKey]*cacheEntry
	matchAttempts int
}

// NewResolver creates a resolver with derivation depth limit d
// (MinDepthLimit..MaxDepthLimit).
func NewResolver(d int) (*Resolver, error) {
	if d < MinDepthLimit || d > MaxDepthLimit {
		return nil, &Error{Kind: ErrInvalidParam, Detail: "depth limit out of range 1..64"}
	}
	return &Resolver{
		depthLimit: d,
		cache:      map[goalKey]*cacheEntry{},
	}, nil
}

// DepthLimit returns the configured derivation depth limit.
func (r *Resolver) DepthLimit() int {
	return r.depthLimit
}

// InstanceCount returns the number of registered instances.
func (r *Resolver) InstanceCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.instances)
}

// AddInstance registers an instance for trait with the given head type and
// context, returning its 1-based id. The operation is rejected (without
// changing any state) when a parameter is invalid, when the instance limit
// is reached, or when a same-trait instance with an equal head up to
// variable renaming already exists. On success the cache is invalidated
// incrementally.
func (r *Resolver) AddInstance(trait string, head *Type, context []Constraint) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if err := validateName("trait", trait); err != nil {
		return 0, err
	}
	if err := validateType(head); err != nil {
		return 0, err
	}
	if len(context) > MaxContext {
		return 0, &Error{Kind: ErrInvalidParam, Detail: "context has more than 4 constraints"}
	}
	headVars := map[int]bool{}
	collectVars(head, headVars)
	for i, c := range context {
		if err := validateName("trait", c.Trait); err != nil {
			return 0, err
		}
		if err := validateType(c.Type); err != nil {
			return 0, err
		}
		ctxVars := map[int]bool{}
		collectVars(c.Type, ctxVars)
		for v := range ctxVars {
			if !headVars[v] {
				return 0, &Error{Kind: ErrInvalidParam, Detail: fmt.Sprintf("context %d uses variable %d not present in the head", i, v)}
			}
		}
	}
	if len(r.instances) >= MaxInstances {
		return 0, &Error{Kind: ErrInstanceLimit, Detail: "more than 200 instances"}
	}
	norm := normalize(head)
	for _, inst := range r.instances {
		if inst.Trait != trait {
			continue
		}
		if typeEqual(normalize(inst.Head), norm) {
			return 0, &Error{Kind: ErrDuplicateInstance, Detail: "head equals existing instance up to variable renaming"}
		}
	}

	inst := &Instance{
		ID:      len(r.instances) + 1,
		Trait:   trait,
		Head:    cloneType(head),
		Context: make([]Constraint, len(context)),
	}
	for i, c := range context {
		inst.Context[i] = Constraint{Trait: c.Trait, Type: cloneType(c.Type)}
	}
	r.instances = append(r.instances, inst)
	r.invalidateFor(inst)
	return inst.ID, nil
}

// invalidateFor removes exactly the cache entries whose re-resolution
// could change after the registration of n: an entry for goal g with
// selected instance S is invalidated iff n has the same trait as g, n's
// head matches g's type, and S is not more specialized than n. The
// invalidation then propagates along the depended-by direction: any entry
// whose tree contains an invalidated goal is invalidated as well.
func (r *Resolver) invalidateFor(n *Instance) {
	invalid := map[goalKey]bool{}
	for key, e := range r.cache {
		if key.trait != n.Trait {
			continue
		}
		if _, ok := match(n.Head, e.typ); !ok {
			continue
		}
		s := r.instances[e.instanceID-1]
		if !moreSpecialized(s.Head, n.Head) {
			invalid[key] = true
		}
	}
	if len(invalid) == 0 {
		return
	}
	parents := map[goalKey][]goalKey{}
	for key, e := range r.cache {
		for _, child := range e.children {
			parents[child] = append(parents[child], key)
		}
	}
	queue := make([]goalKey, 0, len(invalid))
	for key := range invalid {
		queue = append(queue, key)
	}
	for len(queue) > 0 {
		key := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		for _, p := range parents[key] {
			if !invalid[p] {
				invalid[p] = true
				queue = append(queue, p)
			}
		}
	}
	for key := range invalid {
		delete(r.cache, key)
	}
}

// Resolve computes the derivation tree for the constraint (trait, ty),
// where ty must be a ground type of depth at most MaxTypeDepth. It returns
// either a tree or a failure; an invalid parameter is reported as an
// *Error and leaves all state unchanged.
func (r *Resolver) Resolve(trait string, ty *Type) (*Tree, *Failure, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if err := validateName("trait", trait); err != nil {
		return nil, nil, err
	}
	if err := validateGround(ty); err != nil {
		return nil, nil, err
	}
	tree, failure := r.resolveGoal(trait, ty, 1, nil, true)
	return tree, failure, nil
}

// resolveGoal resolves one goal at the given 1-based level. path holds the
// ancestor goals of the current recursion. When useCache is false the
// cache is neither read nor written (used by the consistency self-check).
func (r *Resolver) resolveGoal(trait string, ty *Type, level int, path []goalKey, useCache bool) (*Tree, *Failure) {
	key := goalKey{trait: trait, typ: ty.String()}

	for _, ancestor := range path {
		if ancestor == key {
			return nil, &Failure{Kind: FailCycle, Trait: trait, Type: key.typ}
		}
	}
	if level > r.depthLimit {
		return nil, &Failure{Kind: FailDepthExceeded, Trait: trait, Type: key.typ}
	}
	if useCache {
		if e, ok := r.cache[key]; ok && level+e.height-1 <= r.depthLimit {
			if tree, ok := r.buildTree(key, e); ok {
				return tree, nil
			}
		}
	}

	type candidate struct {
		inst  *Instance
		subst map[int]*Type
	}
	var cands []candidate
	for _, inst := range r.instances {
		if inst.Trait != trait {
			continue
		}
		r.matchAttempts++
		if subst, ok := match(inst.Head, ty); ok {
			cands = append(cands, candidate{inst: inst, subst: subst})
		}
	}
	if len(cands) == 0 {
		return nil, &Failure{Kind: FailNoInstance, Trait: trait, Type: key.typ}
	}

	best := -1
	ambiguous := false
	for i := range cands {
		dominates := true
		for j := range cands {
			if i == j {
				continue
			}
			r.matchAttempts++
			if !moreSpecialized(cands[i].inst.Head, cands[j].inst.Head) {
				dominates = false
				break
			}
		}
		if dominates {
			if best != -1 {
				ambiguous = true
				break
			}
			best = i
		}
	}
	if ambiguous || best == -1 {
		return nil, &Failure{Kind: FailAmbiguous, Trait: trait, Type: key.typ}
	}

	sel := cands[best]
	tree := &Tree{Trait: trait, Type: key.typ, InstanceID: sel.inst.ID}
	childKeys := make([]goalKey, 0, len(sel.inst.Context))
	maxChildHeight := 0
	childPath := append(append([]goalKey{}, path...), key)
	for _, c := range sel.inst.Context {
		ct := applySubst(c.Type, sel.subst)
		child, failure := r.resolveGoal(c.Trait, ct, level+1, childPath, useCache)
		if failure != nil {
			return nil, failure
		}
		childKeys = append(childKeys, goalKey{trait: c.Trait, typ: ct.String()})
		tree.Children = append(tree.Children, child)
		if h := child.Height(); h > maxChildHeight {
			maxChildHeight = h
		}
	}

	if useCache {
		r.cache[key] = &cacheEntry{
			typ:        cloneType(ty),
			instanceID: sel.inst.ID,
			height:     maxChildHeight + 1,
			children:   childKeys,
		}
	}
	return tree, nil
}

// buildTree reconstructs a derivation tree from cache entries. Every
// descendant of a live entry is guaranteed to be live as well, because
// invalidation propagates along the depended-by direction; the boolean
// result is a defensive fallback.
func (r *Resolver) buildTree(key goalKey, e *cacheEntry) (*Tree, bool) {
	tree := &Tree{Trait: key.trait, Type: key.typ, InstanceID: e.instanceID}
	for _, childKey := range e.children {
		childEntry, ok := r.cache[childKey]
		if !ok {
			return nil, false
		}
		child, ok := r.buildTree(childKey, childEntry)
		if !ok {
			return nil, false
		}
		tree.Children = append(tree.Children, child)
	}
	return tree, true
}

// selfCheck verifies that every cache entry agrees with a fresh cache-less
// resolution of its goal. It is used by tests as a consistency check.
func (r *Resolver) selfCheck() error {
	for key, e := range r.cache {
		tree, failure := r.resolveGoal(key.trait, e.typ, 1, nil, false)
		if failure != nil {
			return fmt.Errorf("cache entry %v is stale: fresh resolution fails with %v", key, failure)
		}
		if tree.InstanceID != e.instanceID {
			return fmt.Errorf("cache entry %v selects instance %d, fresh resolution selects %d", key, e.instanceID, tree.InstanceID)
		}
		if h := tree.Height(); h != e.height {
			return fmt.Errorf("cache entry %v has height %d, fresh resolution has %d", key, e.height, h)
		}
		if len(tree.Children) != len(e.children) {
			return fmt.Errorf("cache entry %v has %d subgoals, fresh resolution has %d", key, len(e.children), len(tree.Children))
		}
		for i, child := range tree.Children {
			fresh := goalKey{trait: child.Trait, typ: child.Type}
			if fresh != e.children[i] {
				return fmt.Errorf("cache entry %v subgoal %d is %v, fresh resolution has %v", key, i, e.children[i], fresh)
			}
		}
	}
	return nil
}
