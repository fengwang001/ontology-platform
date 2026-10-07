package ontology

import "sort"

// Snapshot is a self-contained, point-in-time image of the extracted subgraph.
//
// It is an invariant of every returned snapshot that every link in Links has
// both endpoints present in Objects; a boundary link with only one included
// end never appears in Links and is instead reported in Dangling.
type Snapshot struct {
	// Epoch is the unique, fixed point-in-time of the main graph from which
	// this snapshot was taken.
	Epoch uint64

	// Objects included in the snapshot, sorted by id.
	Objects []Object
	// Links fully contained in the snapshot, sorted deterministically.
	Links []Link
	// Dangling lists excluded boundary links, sorted deterministically.
	Dangling []DanglingLink

	// removed is the set of requested ids that were stripped because the
	// caller lacks existence permission. Unexposed: callers must not be able
	// to learn of objects they cannot see through the snapshot API.
	removed map[ObjectID]struct{}

	// metrics is an internal-only cost measure (see extractMetrics).
	metrics extractMetrics
}

// extractMetrics is an internal, verifiable cost measure for one extraction.
// It counts only work this extraction actually performed; it never scales with
// graph elements outside the scope's neighborhoods. Fields are deliberately
// unexported so they are not part of the caller-visible snapshot contract.
type extractMetrics struct {
	// scopeMembershipChecks is len(deduplicated requested scope).
	scopeMembershipChecks int
	// permissionChecks counts existence-permission lookups on scope objects.
	permissionChecks int
	// candidateLinksExamined counts every link key reached via the adjacency
	// index of an in-scope-or-removed object. This is the headline measure:
	// it grows only with scope size and direct incident-link counts.
	candidateLinksExamined int
}

// metrics returns the internal cost measure for a snapshot. It is accessible
// only from within the package (and thus from this module's own tests); it is
// intentionally absent from any caller-facing interface.
func (s *Snapshot) metricsSnapshot() extractMetrics { return s.metrics }

// Extract takes a point-in-time subgraph snapshot.
//
// Decision order is strict:
//  1. parameter validation (empty scope / malformed id / oversize scope),
//  2. empty snapshot when, after stripping objects invisible to caller, no
//     object remains (not an error),
//  3. normal extraction.
//
// Permission stripping happens before dangling-link classification: every
// link incident to a stripped object is excluded as if that end did not exist
// for the caller. When one of these links still has an included other end it
// is recorded as DanglingByPermission; if a link simultaneously qualifies as
// both boundary and permission-induced (included end -> id that is neither
// visible nor ever requested), DanglingByScope wins.
func (g *Graph) Extract(caller Principal, scope []ObjectID) (*Snapshot, error) {
	if len(scope) == 0 {
		return nil, invalidArgumentf("scope must not be empty")
	}

	// Parameter validation precedes everything: no permission check, no lock
	// ordering dependence on graph state, no partial side effects.
	requested := make(map[ObjectID]struct{}, len(scope))
	for _, id := range scope {
		if !validObjectID(id) {
			return nil, invalidArgumentf("malformed object id %q", id)
		}
		requested[id] = struct{}{}
	}
	if len(requested) > MaxScopeSize {
		return nil, invalidArgumentf("scope size %d exceeds limit %d", len(requested), MaxScopeSize)
	}

	// The whole read (permission filtering, dangling classification, image
	// copy, epoch) executes under one RLock, giving a single atomic
	// point-in-time consistent with all writers serialized by the mutex.
	g.mu.RLock()
	defer g.mu.RUnlock()

	m := extractMetrics{scopeMembershipChecks: len(requested)}

	// Step A: strip objects the caller has no existence permission on.
	// Requested ids that do not currently exist in the graph contribute
	// nothing: they were never objects at this point in time.
	included := make(map[ObjectID]struct{}, len(requested))
	removed := make(map[ObjectID]struct{})
	for id := range requested {
		m.permissionChecks++
		if _, exists := g.objects[id]; !exists {
			continue
		}
		if g.canSeeLocked(caller, id) {
			included[id] = struct{}{}
		} else {
			removed[id] = struct{}{}
		}
	}

	snap := &Snapshot{Epoch: g.epoch, removed: removed, metrics: m}

	// Step B: degenerate case — empty snapshot after stripping.
	if len(included) == 0 {
		snap.Objects = []Object{}
		snap.Links = []Link{}
		snap.Dangling = []DanglingLink{}
		return snap, nil
	}

	// Step C: gather the snapshot image. Only adjacency sets of objects in
	// requested (included or removed) are ever touched; the global link/object
	// maps are never scanned.
	examined := make(map[linkKey]struct{})
	linkSeen := make(map[linkKey]struct{})
	danglingSeen := make(map[linkKey]struct{})

	for _, set := range []map[ObjectID]struct{}{included, removed} {
		for id := range set {
			for key := range g.adjacency[id] {
				examined[key] = struct{}{}
			}
		}
	}
	snap.metrics.candidateLinksExamined = len(examined)

	for key := range examined {
		link := g.links[key]
		_, srcIn := included[link.Source]
		_, sinkIn := included[link.Sink]
		switch {
		case srcIn && sinkIn:
			linkSeen[key] = struct{}{}
		case srcIn || sinkIn:
			danglingSeen[key] = struct{}{}
		}
		// neither end included: link fully out of the caller's image.
	}

	objects := make([]Object, 0, len(included))
	for id := range included {
		o := g.objects[id]
		o.Readers = append([]Principal(nil), o.Readers...)
		objects = append(objects, o)
	}
	sort.Slice(objects, func(i, j int) bool { return objects[i].ID < objects[j].ID })
	snap.Objects = objects

	links := make([]Link, 0, len(linkSeen))
	for key := range linkSeen {
		links = append(links, g.links[key])
	}
	sortLinks(links)
	snap.Links = links

	dangling := make([]DanglingLink, 0, len(danglingSeen))
	for key := range danglingSeen {
		link := g.links[key]
		t := g.linkTypes[link.Type]
		var localID, remoteID ObjectID
		if _, ok := included[link.Source]; ok {
			localID, remoteID = link.Source, link.Sink
		} else {
			localID, remoteID = link.Sink, link.Source
		}

		// Source precedence: boundary before permission.
		var src DanglingSource
		if _, requestedRemote := requested[remoteID]; !requestedRemote {
			src = DanglingByScope
		} else {
			src = DanglingByPermission
		}

		// Redact the remote id unless the caller may know of its existence.
		redacted := !g.canSeeLocked(caller, remoteID)
		out := DanglingLink{
			Type:           link.Type,
			LinkDirection:  t.Direct,
			LocalEnd:       localID,
			RemoteEnd:      remoteID,
			RemoteRedacted: redacted,
			Source:         src,
		}
		if redacted {
			out.RemoteEnd = ""
		}
		dangling = append(dangling, out)
	}
	sortDangling(dangling)
	snap.Dangling = dangling

	return snap, nil
}

// sortLinks orders links deterministically by (type, source, sink).
func sortLinks(ls []Link) {
	sort.Slice(ls, func(i, j int) bool {
		if ls[i].Type != ls[j].Type {
			return ls[i].Type < ls[j].Type
		}
		if ls[i].Source != ls[j].Source {
			return ls[i].Source < ls[j].Source
		}
		return ls[i].Sink < ls[j].Sink
	})
}

// sortDangling orders dangling records deterministically by their visible
// content: (local end, type, source, remote end, direction).
func sortDangling(ds []DanglingLink) {
	sort.Slice(ds, func(i, j int) bool {
		a, b := ds[i], ds[j]
		if a.LocalEnd != b.LocalEnd {
			return a.LocalEnd < b.LocalEnd
		}
		if a.Type != b.Type {
			return a.Type < b.Type
		}
		if a.Source != b.Source {
			return a.Source < b.Source
		}
		if a.RemoteEnd != b.RemoteEnd {
			return a.RemoteEnd < b.RemoteEnd
		}
		return a.LinkDirection < b.LinkDirection
	})
}
