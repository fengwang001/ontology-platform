package ontology

// grantKey identifies a single explicit authorization record slot. Applying a
// grant with the same key replaces the previous effect.
type grantKey struct {
	subject string
	object  string
	action  string
}

// state is an immutable configuration snapshot. The gateway mutates by
// constructing a new snapshot under the write lock and atomically swapping it
// in; readers hold the snapshot pointer without taking the write lock, which
// gives every decision a consistent prefix of some global serial order.
type state struct {
	depthCap  int
	objects   map[string]bool
	links     map[string]LinkType
	overrides map[string]OverrideMode
	grants    map[grantKey]Effect
	index     *propIndex
}

func newState(depthCap int) *state {
	return &state{
		depthCap:  depthCap,
		objects:   map[string]bool{},
		links:     map[string]LinkType{},
		overrides: map[string]OverrideMode{},
		grants:    map[grantKey]Effect{},
	}
}

// clone returns a shallow-copied mutable copy. Map contents are copied; the
// propagation index is left nil until buildIndex runs.
func (s *state) clone() *state {
	next := &state{
		depthCap:  s.depthCap,
		objects:   make(map[string]bool, len(s.objects)),
		links:     make(map[string]LinkType, len(s.links)),
		overrides: make(map[string]OverrideMode, len(s.overrides)),
		grants:    make(map[grantKey]Effect, len(s.grants)),
	}
	for name := range s.objects {
		next.objects[name] = true
	}
	for name, link := range s.links {
		next.links[name] = link
	}
	for name, mode := range s.overrides {
		next.overrides[name] = mode
	}
	for key, effect := range s.grants {
		next.grants[key] = effect
	}
	return next
}

// validate checks all configuration-level invariants and builds the
// propagation index. It is the single commit gate: any error here means the
// candidate snapshot is discarded and the previously published snapshot stays
// observable.
func (s *state) validate() error {
	for _, link := range s.links {
		if link.PropagationDepth < 0 {
			return newError(KindInvalidDepth, "link "+link.Name+" declares negative depth")
		}
		if link.PropagationDepth > s.depthCap {
			return newError(KindInvalidDepth, "link "+link.Name+" depth exceeds platform cap")
		}
		if !s.objects[link.From] {
			return newError(KindObjectNotFound, "link "+link.Name+" source type missing: "+link.From)
		}
		if !s.objects[link.To] {
			return newError(KindObjectNotFound, "link "+link.Name+" target type missing: "+link.To)
		}
	}
	for name := range s.overrides {
		if !s.objects[name] {
			return newError(KindObjectNotFound, "override references missing type: "+name)
		}
	}
	for key := range s.grants {
		if !s.objects[key.object] {
			return newError(KindObjectNotFound, "grant references missing type: "+key.object)
		}
	}
	if cycle := findPropagationCycle(s.links); cycle != "" {
		return newError(KindPropagationCycle, cycle)
	}
	s.index = buildIndexFor(s)
	return nil
}
