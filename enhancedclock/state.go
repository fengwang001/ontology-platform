package enhancedclock

type State struct {
	Frames     []Frame
	Pointer    int
	WriteBacks int
}

func (r *Replacer) Snapshot() State {
	r.mu.Lock()
	defer r.mu.Unlock()

	return State{
		Frames:     append([]Frame(nil), r.frames...),
		Pointer:    r.pointer,
		WriteBacks: r.writeBacks,
	}
}
