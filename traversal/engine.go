package traversal

type engineState struct {
	snapshot       Snapshot
	mode           Mode
	hopLimits      []int
	maxHops        int
	frontier       []string
	nextFrontier   []string
	currentSources []string
	visited        map[string]bool
	hop            int
	candidates     []ResultItem
	candidate      int
	hopPrepared    bool
	pendingMarker  *TruncationMarker
	lookahead      *engineStep
	finished       bool
}

type engineStep struct {
	item       ResultItem
	truncation *TruncationMarker
}

func newEngine(snapshot Snapshot, startObjectID string, mode Mode, hopLimits []int, maxHops int) (*engineState, error) {
	if !snapshot.HasObject(startObjectID) {
		return nil, &TraversalError{Code: ErrStartObjectNotFound, Message: "start object not found"}
	}
	return &engineState{
		snapshot:  snapshot,
		mode:      mode,
		hopLimits: append([]int(nil), hopLimits...),
		maxHops:   maxHops,
		frontier:  []string{startObjectID},
		visited:   map[string]bool{startObjectID: true},
	}, nil
}

func (state *engineState) next() (engineStep, bool) {
	if state.lookahead != nil {
		step := *state.lookahead
		state.lookahead = nil
		return step, true
	}
	for {
		if state.finished {
			return engineStep{}, false
		}
		if state.hop == 0 {
			startID := state.frontier[0]
			state.hop = 1
			return engineStep{item: ResultItem{Hop: 0, Object: pointerObject(state.snapshot.Object(startID))}}, true
		}
		if !state.hopPrepared && !state.prepareHop() {
			state.finished = true
			return engineStep{}, false
		}
		for state.candidate < len(state.candidates) {
			candidate := state.candidates[state.candidate]
			state.candidate++
			targetID := candidate.Object.ID
			if state.visited[targetID] {
				continue
			}
			state.visited[targetID] = true
			state.nextFrontier = append(state.nextFrontier, targetID)
			candidate.Hop = state.hop
			return engineStep{item: candidate}, true
		}
		if state.pendingMarker != nil {
			marker := *state.pendingMarker
			state.pendingMarker = nil
			state.hop++
			state.hopPrepared = false
			state.candidates = nil
			state.candidate = 0
			state.frontier = state.nextFrontier
			return engineStep{truncation: &marker}, true
		}
		state.hop++
		state.hopPrepared = false
		state.candidates = nil
		state.candidate = 0
		state.frontier = state.nextFrontier
	}
}

func (state *engineState) peek() (engineStep, bool) {
	for {
		if state.lookahead == nil {
			step, ok := state.next()
			if !ok {
				return engineStep{}, false
			}
			state.lookahead = &step
		}
		if state.lookahead.truncation != nil {
			return *state.lookahead, true
		}
		return *state.lookahead, true
	}
}

func (state *engineState) prepareHop() bool {
	if state.maxHops > 0 && state.hop > state.maxHops {
		return false
	}

	sources := append([]string(nil), state.frontier...)
	state.currentSources = sources
	state.frontier = nil
	state.nextFrontier = nil
	var candidates []ResultItem
	for _, sourceID := range state.currentSources {
		for _, linkID := range state.snapshot.OutgoingLinkIDs(sourceID) {
			link := state.snapshot.Link(linkID)
			object, exists := state.snapshot.objects[link.ToID]
			if exists {
				candidates = append(candidates, ResultItem{
					Link:   pointerLink(link),
					Object: pointerObject(object),
				})
			}
		}
	}

	dropped := 0
	if state.hop <= len(state.hopLimits) && state.hopLimits[state.hop-1] > 0 && len(candidates) > state.hopLimits[state.hop-1] {
		dropped = len(candidates) - state.hopLimits[state.hop-1]
		candidates = candidates[:state.hopLimits[state.hop-1]]
	}

	var nextFrontier []string
	frontierSet := map[string]bool{}
	for _, candidate := range candidates {
		targetID := candidate.Object.ID
		if !state.visited[targetID] && !frontierSet[targetID] {
			nextFrontier = append(nextFrontier, targetID)
			frontierSet[targetID] = true
		}
	}

	state.candidates = candidates
	state.candidate = 0
	state.hopPrepared = true
	if dropped > 0 && state.mode == ExplicitTruncation {
		state.pendingMarker = &TruncationMarker{Hop: state.hop, Dropped: dropped}
	}
	return len(nextFrontier) > 0 || state.pendingMarker != nil
}
