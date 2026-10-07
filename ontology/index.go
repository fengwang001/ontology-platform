package ontology

// Path outcome bits. For every (target, origin) pair the index keeps the set
// of outcomes observed over ALL structural paths from origin to target. A
// single scalar label would lose information whenever one route delivers while
// a parallel route goes through an override (e.g. a direct edge next to a
// detour through a block node).
const (
	bitDeliver  = 1 << 0
	bitBlocked  = 1 << 1
	bitReplaced = 1 << 2
	bitDepth    = 1 << 3
)

// originEntry summarizes all structural paths from one origin to the node
// owning the containing map.
type originEntry struct {
	bits      int
	remaining int // strongest remaining budget among delivered paths
	basis     string
}

func (e originEntry) has(bit int) bool { return e.bits&bit != 0 }

// propIndex is the precomputed structural index for one immutable snapshot.
type propIndex struct {
	inflow   map[string]map[string]originEntry
	outLinks map[string][]LinkType
}

func buildIndexFor(s *state) *propIndex {
	idx := &propIndex{
		inflow:   map[string]map[string]originEntry{},
		outLinks: map[string][]LinkType{},
	}

	links := sortedLinks(s.links)
	indeg := map[string]int{}
	for name := range s.objects {
		idx.inflow[name] = map[string]originEntry{}
		indeg[name] = 0
	}
	for _, link := range links {
		if !link.propagates() {
			continue
		}
		idx.outLinks[link.From] = append(idx.outLinks[link.From], link)
		indeg[link.To]++
	}

	// Kahn topological order. A propagation cycle is rejected before indexing,
	// so every node is processed exactly once.
	queue := make([]string, 0, len(s.objects))
	for name := range s.objects {
		if indeg[name] == 0 {
			queue = append(queue, name)
		}
	}
	order := make([]string, 0, len(s.objects))
	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]
		order = append(order, node)
		for _, link := range idx.outLinks[node] {
			indeg[link.To]--
			if indeg[link.To] == 0 {
				queue = append(queue, link.To)
			}
		}
	}

	// Seed every node as an origin with an unbounded budget, bounded by the
	// first declared link when it is crossed.
	for _, node := range order {
		entries := idx.inflow[node]
		entries[node] = originEntry{bits: bitDeliver, remaining: s.depthCap, basis: node}

		applyOverride(entries, node, s.overrides[node])

		for _, link := range idx.outLinks[node] {
			target := idx.inflow[link.To]
			for origin, entry := range entries {
				crossed := crossEntry(entry, link)
				crossed.bits &^= blockSelfBit
				if crossed.bits == 0 {
					continue
				}
				mergeEntry(target, origin, crossed)
			}
		}
		// The internal severing marker must never appear in observable inflow.
		for origin, entry := range entries {
			entry.bits &^= blockSelfBit
			entries[origin] = entry
		}
	}
	return idx
}

// applyOverride transforms incoming paths at the node. The node's own origin
// is never transformed by its own override (its direct grants are effective
// locally); at a Block node the own-origin's continuation is severed by
// converting it to a block marker before forwarding.
func applyOverride(entries map[string]originEntry, node string, mode OverrideMode) {
	switch mode {
	case NoOverride:
		return
	case ReplaceOverride:
		for origin := range entries {
			if origin == node {
				continue
			}
			e := entries[origin]
			if e.has(bitDeliver) {
				e.bits &^= bitDeliver
				e.bits |= bitReplaced
			}
			entries[origin] = e
		}
	case BlockOverride:
		for origin := range entries {
			e := entries[origin]
			if origin == node {
				// Own direct grants hold locally; the self entry is still
				// deliver at THIS node but must not leave as deliver. The
				// conversion to a block marker happens while forwarding
				// (see crossEntry via blockSelf flag encoded by the caller).
				continue
			}
			if e.has(bitDeliver) || e.has(bitReplaced) {
				e.bits &^= bitDeliver | bitReplaced
				e.bits |= bitBlocked
			}
			entries[origin] = e
		}
		// Mark the self entry for forwarding-time severing.
		self := entries[node]
		self.bits |= blockSelfBit
		entries[node] = self
	}
}

// blockSelfBit is an internal marker on a node's self entry meaning "deliver
// locally but turn into block when crossing an outgoing edge".
const blockSelfBit = 1 << 4

func crossEntry(entry originEntry, link LinkType) originEntry {
	basis := entry.basis + " --" + link.Name + "-->" + link.To
	out := originEntry{basis: basis}

	// Delivered paths cross with budget min(remaining, depth)-1. The hop that
	// reaches remaining zero still delivers; the next hop becomes depth.
	severedSelf := entry.bits&blockSelfBit != 0
	if entry.has(bitDeliver) && !severedSelf && link.PropagationDepth > 0 && entry.remaining > 0 {
		remaining := entry.remaining
		if link.PropagationDepth < remaining {
			remaining = link.PropagationDepth
		}
		out.bits |= bitDeliver
		out.remaining = remaining - 1
	} else if entry.has(bitDeliver) && !severedSelf {
		// Natural termination: the hop is attempted but no token leaves, so
		// the destination is marked depth-exhausted exactly once and the
		// marker propagates no further (and is not subject to the target's
		// override, since nothing is delivered into it).
		out.bits |= bitDepth
	}

	// Structural obstruction markers propagate without consuming budget.
	if entry.has(bitBlocked) {
		out.bits |= bitBlocked
	}
	if entry.has(bitReplaced) {
		out.bits |= bitReplaced
	}
	// Note: bitDepth is intentionally never forwarded. It is recorded only on
	// the node where a concrete hop terminates due to exhausted budget.

	// A block node's own token is severed while crossing out.
	if severedSelf {
		out.bits &^= bitDeliver
		out.bits |= bitBlocked
	}

	return out
}

// mergeEntry unions path-outcome sets for one origin.
func mergeEntry(target map[string]originEntry, origin string, candidate originEntry) {
	current, ok := target[origin]
	if !ok {
		target[origin] = candidate
		return
	}
	current.bits |= candidate.bits &^ blockSelfBit
	if candidate.has(bitDeliver) && candidate.remaining > current.remaining {
		current.remaining = candidate.remaining
	}
	if current.basis == "" {
		current.basis = candidate.basis
	}
	target[origin] = current
}

func sortedLinks(links map[string]LinkType) []LinkType {
	out := make([]LinkType, 0, len(links))
	for _, link := range links {
		out = append(out, link)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1].Name > out[j].Name; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}
