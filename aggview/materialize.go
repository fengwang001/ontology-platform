package aggview

// MaterializedView replays a net-change log in order to reconstruct the
// filtered view. It is a small stand-in for any downstream consumer.
type MaterializedView struct {
	groups map[string]Agg
}

// NewMaterializedView returns an empty downstream view.
func NewMaterializedView() *MaterializedView {
	return &MaterializedView{groups: make(map[string]Agg)}
}

// Apply applies one log entry to the downstream view.
func (m *MaterializedView) Apply(e LogEntry) {
	switch e.Op {
	case EntryUpsert:
		if e.New == nil {
			return
		}
		cp := *e.New
		m.groups[cp.Group] = cp
	case EntryRetract:
		if e.Old == nil {
			return
		}
		delete(m.groups, e.Old.Group)
	}
}

// Snapshot returns the current downstream aggregates sorted by group name.
func (m *MaterializedView) Snapshot() []Agg {
	out := make([]Agg, 0, len(m.groups))
	for _, g := range m.groups {
		out = append(out, g)
	}
	sortAggs(out)
	return out
}
