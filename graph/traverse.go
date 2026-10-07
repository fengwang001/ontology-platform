package graph

type stackFrame struct {
	path  Path
	depth int
}

func (s *Snapshot) Traverse(req TraverseRequest) (TraverseResult, error) {
	if !s.state.hasObject(req.StartID) {
		return TraverseResult{}, ErrStartNotFound
	}
	if req.MaxDepth <= 0 {
		return TraverseResult{}, ErrInvalidMaxDepth
	}
	if req.ResultLimit <= 0 {
		return TraverseResult{}, ErrInvalidResultLimit
	}

	directions := normalizeDirections(req.Directions)
	if len(directions) == 0 {
		return TraverseResult{}, ErrEmptyLinkDirections
	}

	logf := req.Logger
	if logf == nil {
		logf = func(string, ...any) {}
	}
	logf("traverse start snapshot=%d start=%s max_depth=%d result_limit=%d directions=%v", s.id, req.StartID, req.MaxDepth, req.ResultLimit, directions)

	counter := newResultCounter(req.ResultLimit)
	result := TraverseResult{
		Paths:      make([]Path, 0),
		Truncated:  make([]TruncatedBranch, 0),
		SnapshotID: s.id,
	}
	stack := []stackFrame{{
		path:  Path{ObjectIDs: []string{req.StartID}, LinkTypes: []string{}},
		depth: 0,
	}}

	for len(stack) > 0 {
		current := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		neighbors := s.state.neighbors(current.path.ObjectIDs[current.depth], directions)
		if counter.reached() {
			reason := LimitOnly
			if current.depth == req.MaxDepth && len(neighbors) > 0 {
				reason = LimitBeforeDepth
			}
			result.Truncated = append(result.Truncated, TruncatedBranch{
				Prefix:           clonePath(current.path),
				PrefixDepth:      current.depth,
				Reason:           reason,
				DepthWhenStopped: current.depth,
			})
			logf("truncated prefix=%v depth=%d reason=%s basis=result limit already reached before this queued branch was processed", current.path.ObjectIDs, current.depth, reason)
			continue
		}

		counter.acceptOne()
		result.Paths = append(result.Paths, clonePath(current.path))

		atDepthBoundary := current.depth == req.MaxDepth && len(neighbors) > 0
		limitReached := counter.reached()

		switch {
		case atDepthBoundary && limitReached:
			result.Truncated = append(result.Truncated, TruncatedBranch{
				Prefix:           clonePath(current.path),
				PrefixDepth:      current.depth,
				Reason:           DepthBeforeLimit,
				DepthWhenStopped: current.depth,
			})
			logf("path=%v depth=%d returned=true truncation=%s basis=depth boundary was reached before accepting this path filled the result limit", current.path.ObjectIDs, current.depth, DepthBeforeLimit)
			stack = drainQueuedBranches(stack, &result, req.MaxDepth, s.state, directions, logf)
		case atDepthBoundary:
			result.Truncated = append(result.Truncated, TruncatedBranch{
				Prefix:           clonePath(current.path),
				PrefixDepth:      current.depth,
				Reason:           DepthOnly,
				DepthWhenStopped: current.depth,
			})
			logf("path=%v depth=%d returned=true truncation=%s basis=path reached max depth while result slots remained", current.path.ObjectIDs, current.depth, DepthOnly)
		case limitReached:
			result.Truncated = append(result.Truncated, TruncatedBranch{
				Prefix:           clonePath(current.path),
				PrefixDepth:      current.depth,
				Reason:           LimitOnly,
				DepthWhenStopped: current.depth,
			})
			logf("path=%v depth=%d returned=true truncation=%s basis=accepting this path filled the result limit before depth was reached", current.path.ObjectIDs, current.depth, LimitOnly)
			for idx := len(neighbors) - 1; idx >= 0; idx-- {
				next := neighbors[idx]
				stack = append(stack, stackFrame{
					path:  extendPath(current.path, next),
					depth: current.depth + 1,
				})
			}
			stack = drainQueuedBranches(stack, &result, req.MaxDepth, s.state, directions, logf)
		default:
			logf("path=%v depth=%d returned=true truncation=none basis=path was within both limits", current.path.ObjectIDs, current.depth)
			for idx := len(neighbors) - 1; idx >= 0; idx-- {
				next := neighbors[idx]
				stack = append(stack, stackFrame{
					path: Path{
						ObjectIDs: append(append([]string(nil), current.path.ObjectIDs...), next.toID),
						LinkTypes: append(append([]string(nil), current.path.LinkTypes...), next.linkType),
					},
					depth: current.depth + 1,
				})
			}
		}
	}

	result.CompletedPaths = len(result.Paths)
	logf("traverse complete snapshot=%d returned_paths=%d truncated_branches=%d", s.id, len(result.Paths), len(result.Truncated))
	return result, nil
}

func normalizeDirections(requested []Direction) []Direction {
	seen := make(map[Direction]struct{}, len(requested))
	for _, direction := range requested {
		if direction == Outgoing || direction == Incoming {
			seen[direction] = struct{}{}
		}
	}

	directions := make([]Direction, 0, len(seen))
	if _, ok := seen[Outgoing]; ok {
		directions = append(directions, Outgoing)
	}
	if _, ok := seen[Incoming]; ok {
		directions = append(directions, Incoming)
	}
	return directions
}

func drainQueuedBranches(stack []stackFrame, result *TraverseResult, maxDepth int, state *graphState, directions []Direction, logf func(string, ...any)) []stackFrame {
	for len(stack) > 0 {
		queued := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		reason := LimitOnly
		queuedNeighbors := state.neighbors(queued.path.ObjectIDs[queued.depth], directions)
		if queued.depth == maxDepth && len(queuedNeighbors) > 0 {
			reason = LimitBeforeDepth
		}
		result.Truncated = append(result.Truncated, TruncatedBranch{
			Prefix:           clonePath(queued.path),
			PrefixDepth:      queued.depth,
			Reason:           reason,
			DepthWhenStopped: queued.depth,
		})
		logf("truncated prefix=%v depth=%d reason=%s basis=queued branch received no preference after the result limit was reached", queued.path.ObjectIDs, queued.depth, reason)
	}
	return stack
}

func clonePath(path Path) Path {
	return Path{
		ObjectIDs: append([]string(nil), path.ObjectIDs...),
		LinkTypes: append([]string(nil), path.LinkTypes...),
	}
}

func extendPath(path Path, next edge) Path {
	return Path{
		ObjectIDs: append(append([]string(nil), path.ObjectIDs...), next.toID),
		LinkTypes: append(append([]string(nil), path.LinkTypes...), next.linkType),
	}
}
