package ontology

// tagEdges builds the traversable adjacency (object type -> object type) for
// one tag. A link type is traversable for the tag when a propagation
// declaration exists for the (tag, link) pair in the edge's direction and no
// blocking point covers the same pair. When ignoreBlocks is true, blocking
// points are disregarded; the result is used to compute the blocked set.
func (e *Engine) tagEdges(tag string, ignoreBlocks bool) map[string][]string {
	adj := map[string][]string{}
	for k, dirs := range e.propagations {
		if k.tag != tag {
			continue
		}
		if !ignoreBlocks {
			if _, blocked := e.blocks[blockKey{tag: tag, link: k.link}]; blocked {
				continue
			}
		}
		link, ok := e.linkTypes[k.link]
		if !ok {
			continue
		}
		if dirs[Downstream] {
			adj[link.From] = append(adj[link.From], link.To)
		}
		if dirs[Upstream] {
			adj[link.To] = append(adj[link.To], link.From)
		}
	}
	return adj
}

// reachable returns the set of object types reachable from seeds via adj.
// Cycles terminate naturally through the visited set; the result is a pure
// reachability set and therefore independent of enumeration order and
// stable for any propagation depth.
func reachable(seeds []string, adj map[string][]string) map[string]struct{} {
	seen := map[string]struct{}{}
	queue := make([]string, 0, len(seeds))
	for _, s := range seeds {
		if _, ok := seen[s]; !ok {
			seen[s] = struct{}{}
			queue = append(queue, s)
		}
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, next := range adj[cur] {
			if _, ok := seen[next]; !ok {
				seen[next] = struct{}{}
				queue = append(queue, next)
			}
		}
	}
	return seen
}

// recomputeTagsLocked rebuilds typeTags and typeBlocked from declarations.
// Caller must hold the write lock.
//
// For every tag the effective carrier set is the reachability fixpoint over
// the types where the tag is directly attached. A type may receive the same
// tag over many paths; the set semantics collapse all of them to a single
// conclusion. typeBlocked records, per type, the tags that would reach the
// type if blocking points were ignored but do not actually reach it — i.e.
// tags whose every inheritance path is blocked.
func (e *Engine) recomputeTagsLocked() {
	e.typeTags = map[string]map[string]struct{}{}
	e.typeBlocked = map[string]map[string]struct{}{}

	// Candidate tags: anything attached or propagated. Tags without any
	// declaration can never be inherited nor blocked.
	candidates := map[string]struct{}{}
	for _, set := range e.attachments {
		for tag := range set {
			candidates[tag] = struct{}{}
		}
	}
	for k := range e.propagations {
		candidates[k.tag] = struct{}{}
	}

	for tag := range candidates {
		var seeds []string
		for objectType, set := range e.attachments {
			if _, ok := set[tag]; ok {
				seeds = append(seeds, objectType)
			}
		}
		actual := reachable(seeds, e.tagEdges(tag, false))
		potential := reachable(seeds, e.tagEdges(tag, true))

		for objectType := range actual {
			set := e.typeTags[objectType]
			if set == nil {
				set = map[string]struct{}{}
				e.typeTags[objectType] = set
			}
			set[tag] = struct{}{}
		}
		for objectType := range potential {
			if _, ok := actual[objectType]; ok {
				continue
			}
			set := e.typeBlocked[objectType]
			if set == nil {
				set = map[string]struct{}{}
				e.typeBlocked[objectType] = set
			}
			set[tag] = struct{}{}
		}
	}
}
