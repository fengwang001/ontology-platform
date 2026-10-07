package traversal

func FullTraversal(snapshot Snapshot, startObjectID string, mode Mode, hopLimits []int, maxHops int) ([]ResultItem, []TruncationMarker, error) {
	if !snapshot.HasObject(startObjectID) {
		return nil, nil, &TraversalError{Code: ErrStartObjectNotFound, Message: "start object not found"}
	}

	visited := map[string]bool{startObjectID: true}
	frontier := []string{startObjectID}
	items := []ResultItem{{Hop: 0, Object: pointerObject(snapshot.Object(startObjectID))}}
	var markers []TruncationMarker

	for hop := 1; maxHops <= 0 || hop <= maxHops; hop++ {
		var candidates []ResultItem
		for _, sourceID := range frontier {
			for _, linkID := range snapshot.OutgoingLinkIDs(sourceID) {
				link := snapshot.Link(linkID)
				object, exists := snapshot.objects[link.ToID]
				if exists {
					candidates = append(candidates, ResultItem{
						Hop:    hop,
						Link:   pointerLink(link),
						Object: pointerObject(object),
					})
				}
			}
		}

		dropped := 0
		if hop <= len(hopLimits) && hopLimits[hop-1] > 0 && len(candidates) > hopLimits[hop-1] {
			dropped = len(candidates) - hopLimits[hop-1]
			candidates = candidates[:hopLimits[hop-1]]
		}

		if dropped > 0 && mode == ExplicitTruncation {
			markers = append(markers, TruncationMarker{Hop: hop, Dropped: dropped})
		}

		var nextFrontier []string
		frontierSet := map[string]bool{}
		for _, candidate := range candidates {
			targetID := candidate.Object.ID
			if visited[targetID] || frontierSet[targetID] {
				continue
			}
			visited[targetID] = true
			frontierSet[targetID] = true
			candidate.Hop = hop
			nextFrontier = append(nextFrontier, targetID)
			items = append(items, candidate)
		}
		if len(nextFrontier) == 0 {
			break
		}
		frontier = nextFrontier
	}

	return items, markers, nil
}

func pointerObject(object Object) *Object {
	return &object
}

func pointerLink(link Link) *Link {
	return &link
}

func ChunkFullTraversal(items []ResultItem, truncations []TruncationMarker, batchSize int) ([]Page, error) {
	if batchSize <= 0 {
		return nil, &TraversalError{Code: ErrInvalidBatchSize, Message: "batch size must be a positive integer"}
	}
	var pages []Page
	current := Page{Items: []ResultItem{}, Truncations: []TruncationMarker{}}
	itemIndex := 0
	markerIndex := 0
	flush := func() {
		if len(current.Items) > 0 || len(current.Truncations) > 0 {
			pages = append(pages, current)
		}
		current = Page{Items: []ResultItem{}, Truncations: []TruncationMarker{}}
	}
	for itemIndex < len(items) || markerIndex < len(truncations) {
		if itemIndex < len(items) && (markerIndex >= len(truncations) || items[itemIndex].Hop <= truncations[markerIndex].Hop) {
			if len(current.Items) == batchSize {
				flush()
			}
			current.Items = append(current.Items, items[itemIndex])
			itemIndex++
			continue
		}
		if len(current.Items) == batchSize {
			flush()
		}
		current.Truncations = append(current.Truncations, truncations[markerIndex])
		markerIndex++
	}
	if len(current.Items) > 0 || len(current.Truncations) > 0 {
		current.Complete = true
		pages = append(pages, current)
	}
	if len(pages) == 0 {
		pages = []Page{{Items: []ResultItem{}, Truncations: []TruncationMarker{}, Complete: true}}
	}
	return pages, nil
}
