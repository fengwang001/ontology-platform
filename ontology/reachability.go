package ontology

import "context"

// reachableFrom implements the full decision procedure against one fixed
// snapshot, following the mandated precedence:
//
//  1. malformed endpoint id
//  2. endpoint genuinely absent from the graph (independent of permissions)
//  3. endpoint not visible to the caller -> RestrictedUnknown
//  4. permission-aware search, with truncated-vs-exhausted separation.
func reachableFrom(ctx context.Context, s *snap, start, end string, caller CallerID, tracer Tracer) (Outcome, Reason, *Metrics, error) {
	m := &Metrics{}
	trace := func(ev map[string]any) {
		if tracer != nil {
			ev["epoch"] = s.epoch
			tracer.Trace(ev)
		}
	}

	if !validIdentifier(start) {
		return 0, "", nil, ErrInvalidID(start)
	}
	if !validIdentifier(end) {
		return 0, "", nil, ErrInvalidID(end)
	}

	startObj, startExists := s.objects[start]
	endObj, endExists := s.objects[end]

	// Absence is a permission-independent fact. Invisibility cannot turn a
	// missing object into RestrictedUnknown: the caller of the API contract
	// distinguishes the two classes explicitly, and the response never leaks
	// visibility status through the "missing" channel.
	if !startExists {
		return 0, "", nil, ErrMissingStart(start)
	}
	if !endExists {
		return 0, "", nil, ErrMissingEnd(end)
	}

	startVisible := s.visible(caller, start)
	endVisible := s.visible(caller, end)
	if !startVisible || !endVisible {
		which := start
		if !endVisible {
			which = end
		}
		trace(map[string]any{"stage": "precheck", "invisible": which,
			"start_visible": startVisible, "end_visible": endVisible})
		return RestrictedUnknown, ReasonInvisibleEndpoint, m, nil
	}
	_ = startObj
	_ = endObj

	// Zero-length path: a visible start that is also the target is reachable
	// without crossing any link.
	if start == end {
		trace(map[string]any{"stage": "certified", "result": "found", "zero_length": true})
		return Reachable, ReasonCertifiedPath, m, nil
	}

	// Stage 4a: certified search. An edge may be crossed only when the caller
	// holds traversal permission for its link type AND existence permission
	// on its head. Everything this BFS reaches is provably visible, so
	// reaching end is a proof (not a guess).
	certified := map[string]bool{start: true}
	queue := []string{start}
	for len(queue) > 0 {
		if err := ctx.Err(); err != nil {
			return 0, "", nil, err
		}
		cur := queue[0]
		queue = queue[1:]
		m.ObjectsDequeued++
		trace(map[string]any{"stage": "dequeue", "node": cur})
		for _, e := range s.adj[cur] {
			m.LinksInspected++
			if !s.canTraverse(caller, e.linkType) {
				m.LinksBlocked++
				trace(map[string]any{"stage": "certified", "blocked": "traversal",
					"from": cur, "to": e.to, "link_type": e.linkType})
				continue
			}
			if !s.visible(caller, e.to) {
				m.ObjectsInvisible++
				trace(map[string]any{"stage": "certified", "blocked": "existence",
					"from": cur, "to": e.to, "link_type": e.linkType})
				continue
			}
			if certified[e.to] {
				continue
			}
			certified[e.to] = true
			if e.to == end {
				trace(map[string]any{"stage": "certified", "result": "found", "metrics": *m})
				return Reachable, ReasonCertifiedPath, m, nil
			}
			queue = append(queue, e.to)
		}
	}

	// Stage 4b: no certified path. The only remaining question is whether the
	// "no path" conclusion is provable or merely an artifact of permissions.
	//
	// If permissions were the obstruction, then at least one ground-truth path
	// start -> end exists. A ground-truth BFS ignores both permission kinds;
	// it expands only what a hypothetical privileged caller could reach from
	// start, so its cost is bounded by the connected component of start and
	// never by unrelated, unreachable regions of the graph.
	ground := map[string]bool{start: true}
	gq := []string{start}
	for len(gq) > 0 {
		if err := ctx.Err(); err != nil {
			return 0, "", nil, err
		}
		cur := gq[0]
		gq = gq[1:]
		m.ShadowObjectsDequeued++
		for _, e := range s.adj[cur] {
			m.ShadowLinksInspected++
			if ground[e.to] {
				continue
			}
			ground[e.to] = true
			gq = append(gq, e.to)
		}
	}

	if !ground[end] {
		// Exhausted proof: not even an omniscient observer can get from start
		// to end. Every permission-skipped edge is therefore irrelevant.
		trace(map[string]any{"stage": "shadow", "result": "no_ground_truth_path", "metrics": *m})
		return Unreachable, ReasonNoGroundTruthPath, m, nil
	}

	// A ground-truth path exists but none survived the caller's permission
	// filters: some skipped edge on some candidate path could have led to
	// end, so absence of a path cannot be certified.
	trace(map[string]any{"stage": "shadow", "result": "truncated", "metrics": *m})
	return RestrictedUnknown, ReasonCandidatesTruncated, m, nil
}
