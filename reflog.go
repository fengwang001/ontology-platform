package retention

type refState struct {
	active  []*refEntry
	roots   map[ID]int
	current ID
	existed bool
	version int64
	lastAt  int64
}

type refEntry struct {
	record      Record
	active      bool
	activeIndex int
	state       *refState
	item        *scheduleItem
}

func (s *Store) appendRefRecord(name string, next ID, now int64, operator string) error {
	state := s.refs[name]
	if state == nil {
		state = &refState{existed: true}
		s.refs[name] = state
	}
	if now < state.lastAt {
		return ErrClockMovedBack
	}
	entry := &refEntry{
		record: Record{
			Old:      state.currentValue(),
			New:      next,
			At:       now,
			Operator: operator,
		},
		state: state,
	}
	state.current = next
	state.lastAt = now
	s.addEntry(state, entry)
	s.rebuildReference(state)
	return nil
}

func (s *Store) addEntry(state *refState, entry *refEntry) {
	entry.active = true
	entry.activeIndex = len(state.active)
	state.active = append(state.active, entry)
}

func (s *Store) removeEntry(state *refState, entry *refEntry) {
	position := entry.activeIndex
	last := len(state.active) - 1
	moved := state.active[last]
	state.active[position] = moved
	moved.activeIndex = position
	state.active = state.active[:last]
	entry.active = false
	entry.activeIndex = -1
}

func (s *Store) rebuildReference(state *refState) {
	nextRoots := make(map[ID]int)
	if current := state.currentValue(); current != "" {
		nextRoots[current]++
	}
	for _, entry := range state.active {
		if entry.record.Old != "" {
			nextRoots[entry.record.Old]++
		}
		if entry.record.New != "" {
			nextRoots[entry.record.New]++
		}
	}
	for commit, count := range state.roots {
		s.pins[commit] -= count
		if s.pins[commit] == 0 {
			delete(s.pins, commit)
		}
	}
	for commit, count := range nextRoots {
		s.pins[commit] += count
	}
	state.roots = nextRoots

	state.version++
	for _, entry := range state.active {
		s.scheduleEntry(entry)
	}
}

func (state *refState) currentValue() ID {
	return state.current
}

func (s *Store) deadlineFor(entry *refEntry, current ID) int64 {
	retention := s.policy.UnreachableRetention
	if current != "" && entry.record.Old != "" && s.isAncestor(entry.record.Old, current) {
		retention = s.policy.ReachableRetention
	}
	return entry.record.At + retention
}

func (s *Store) isAncestor(maybeAncestor ID, descendant ID) bool {
	if closure := s.ancestors[descendant]; closure != nil {
		return closure[maybeAncestor]
	}
	closure := map[ID]bool{descendant: true}
	frontier := []ID{descendant}
	for len(frontier) > 0 {
		current := frontier[len(frontier)-1]
		frontier = frontier[:len(frontier)-1]
		commit := s.commits[current]
		if commit == nil {
			continue
		}
		for _, parent := range commit.Parents {
			if !closure[parent] {
				closure[parent] = true
				frontier = append(frontier, parent)
			}
		}
	}
	s.ancestors[descendant] = closure
	return closure[maybeAncestor]
}
