package graph

type oracleRun struct {
	paths      []Path
	truncated  []TruncatedBranch
	accepted   int
	limit      int
	maxDepth   int
	state      *graphState
	directions []Direction
}

func naiveTraverse(state *graphState, startID string, maxDepth, limit int, directions []Direction) (paths []Path, truncated []TruncatedBranch) {
	run := &oracleRun{
		paths:      make([]Path, 0),
		truncated:  make([]TruncatedBranch, 0),
		limit:      limit,
		maxDepth:   maxDepth,
		state:      state,
		directions: directions,
	}
	root := Path{ObjectIDs: []string{startID}, LinkTypes: []string{}}
	run.visit(root, 0)
	return run.paths, run.truncated
}

func (r *oracleRun) visit(path Path, depth int) {
	if r.accepted >= r.limit {
		r.appendTruncation(path, depth)
		return
	}

	r.accepted++
	r.paths = append(r.paths, clonePath(path))

	neighbors := r.state.neighbors(path.ObjectIDs[depth], r.directions)
	atDepthBoundary := depth == r.maxDepth && len(neighbors) > 0
	if atDepthBoundary && r.accepted >= r.limit {
		r.truncated = append(r.truncated, TruncatedBranch{
			Prefix:           clonePath(path),
			PrefixDepth:      depth,
			Reason:           DepthBeforeLimit,
			DepthWhenStopped: depth,
		})
		return
	}
	if atDepthBoundary {
		r.truncated = append(r.truncated, TruncatedBranch{
			Prefix:           clonePath(path),
			PrefixDepth:      depth,
			Reason:           DepthOnly,
			DepthWhenStopped: depth,
		})
		return
	}

	if r.accepted >= r.limit {
		r.truncated = append(r.truncated, TruncatedBranch{
			Prefix:           clonePath(path),
			PrefixDepth:      depth,
			Reason:           LimitOnly,
			DepthWhenStopped: depth,
		})
		for _, next := range neighbors {
			r.visit(extendPath(path, next), depth+1)
		}
		return
	}

	for _, next := range neighbors {
		r.visit(extendPath(path, next), depth+1)
	}
}

func (r *oracleRun) appendTruncation(path Path, depth int) {
	reason := LimitOnly
	neighbors := r.state.neighbors(path.ObjectIDs[depth], r.directions)
	if depth == r.maxDepth && len(neighbors) > 0 {
		reason = LimitBeforeDepth
	}
	r.truncated = append(r.truncated, TruncatedBranch{
		Prefix:           clonePath(path),
		PrefixDepth:      depth,
		Reason:           reason,
		DepthWhenStopped: depth,
	})
}
