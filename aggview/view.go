package aggview

import (
	"fmt"
	"sort"
	"sync"
)

// Config configures a View.
type Config struct {
	// MinCount is the inclusive row-count threshold; a group is visible only
	// when its count is >= MinCount. Must be >= 1.
	MinCount int64
	// MinSum is the inclusive sum threshold; a group is visible only when its
	// sum is >= MinSum.
	MinSum int64
	// MaxGroups caps the number of distinct groups the view may track. A batch
	// that would exceed it is rejected atomically. Zero means unlimited.
	MaxGroups int
	// Logger receives a human-readable line for every input, decision and
	// output. May be nil.
	Logger Logger
}

// View is an incrementally maintained grouped aggregation view with a filter.
// It is safe for concurrent readers; Apply is serialized by an internal lock.
type View struct {
	mu     sync.RWMutex
	cfg    Config
	rows   map[string]Row
	groups map[string]*Agg
	log    []LogEntry
	seq    int64
}

// New creates an empty view.
func New(cfg Config) *View {
	if cfg.MinCount < 1 {
		cfg.MinCount = 1
	}
	return &View{
		cfg:    cfg,
		rows:   make(map[string]Row),
		groups: make(map[string]*Agg),
	}
}

// Apply validates the whole batch first; if any mutation is invalid it returns
// a distinguishable error without changing aggregates, the view or the log.
// Otherwise it applies the mutations in order, appending net-change entries.
func (v *View) Apply(batch []Mutation) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	if err := v.validateLocked(batch); err != nil {
		v.logf("reject batch size=%d reason=%q", len(batch), err.Error())
		return err
	}

	for _, m := range batch {
		v.applyOneLocked(m)
	}
	return nil
}

// Snapshot returns a copy of the currently visible aggregates sorted by group
// name. Readers may use it without coordinating with writers.
func (v *View) Snapshot() []Agg {
	v.mu.RLock()
	defer v.mu.RUnlock()

	out := make([]Agg, 0, len(v.groups))
	for _, g := range v.groups {
		if g.Visible(v.cfg.MinCount, v.cfg.MinSum) {
			out = append(out, *g)
		}
	}
	sortAggs(out)
	return out
}

// Log returns a copy of the net-change log produced so far.
func (v *View) Log() []LogEntry {
	v.mu.RLock()
	defer v.mu.RUnlock()

	out := make([]LogEntry, len(v.log))
	copy(out, v.log)
	return out
}

// validateLocked checks the batch against a simulation of the state without
// mutating anything. It must run under the write lock.
func (v *View) validateLocked(batch []Mutation) error {
	type simAgg struct {
		count, sum int64
	}
	simRows := make(map[string]Row, len(v.rows))
	for id, r := range v.rows {
		simRows[id] = r
	}
	simGroups := make(map[string]simAgg)
	groupCount := len(v.groups)

	remove := func(id string) {
		old := simRows[id]
		g, ok := simGroups[old.Group]
		if !ok {
			if base, has := v.groups[old.Group]; has {
				g = simAgg{count: base.Count, sum: base.Sum}
			}
		}
		g.count--
		g.sum -= old.Value
		if g.count == 0 {
			delete(simGroups, old.Group)
			if _, base := v.groups[old.Group]; !base {
				groupCount--
			}
		} else {
			simGroups[old.Group] = g
		}
		delete(simRows, id)
	}
	add := func(r Row) {
		if _, exists := simGroups[r.Group]; !exists {
			if base, has := v.groups[r.Group]; has {
				simGroups[r.Group] = simAgg{count: base.Count, sum: base.Sum}
			} else {
				groupCount++
				simGroups[r.Group] = simAgg{}
			}
		}
		g := simGroups[r.Group]
		g.count++
		g.sum += r.Value
		simGroups[r.Group] = g
		simRows[r.ID] = r
	}

	for i, m := range batch {
		switch m.Op {
		case OpInsert:
			if m.Row.Group == "" {
				return fmt.Errorf("%w: batch[%d] row id=%q has empty group",
					ErrEmptyGroup, i, m.Row.ID)
			}
			if _, exists := simRows[m.Row.ID]; exists {
				remove(m.Row.ID)
			}
			if v.cfg.MaxGroups > 0 {
				_, inSim := simGroups[m.Row.Group]
				_, inBase := v.groups[m.Row.Group]
				wouldCreate := !inSim && !inBase
				if wouldCreate && groupCount+1 > v.cfg.MaxGroups {
					return fmt.Errorf(
						"%w: batch[%d] would create group %q (limit=%d)",
						ErrGroupLimit, i, m.Row.Group, v.cfg.MaxGroups)
				}
			}
			add(m.Row)
		case OpDelete:
			if _, exists := simRows[m.ID]; !exists {
				return fmt.Errorf("%w: batch[%d] id=%q",
					ErrRowNotFound, i, m.ID)
			}
			remove(m.ID)
		default:
			return fmt.Errorf("%w: batch[%d] op=%d", ErrUnknownOp, i, m.Op)
		}
	}
	return nil
}

// applyOneLocked applies a single mutation and appends the net-change entry.
// It must run under the write lock.
func (v *View) applyOneLocked(m Mutation) {
	var group string
	switch m.Op {
	case OpInsert:
		group = m.Row.Group
		v.logf("input op=%s id=%q group=%q value=%d",
			m.Op, m.Row.ID, m.Row.Group, m.Row.Value)
	case OpDelete:
		group = v.rows[m.ID].Group
		v.logf("input op=%s id=%q", m.Op, m.ID)
	}

	beforePtr, beforeExists := v.groups[group]
	var before Agg
	if beforeExists {
		before = *beforePtr
	}
	beforeVisible := beforeExists && before.Visible(v.cfg.MinCount, v.cfg.MinSum)

	switch m.Op {
	case OpInsert:
		if old, ok := v.rows[m.Row.ID]; ok {
			v.subtractLocked(old)
		}
		v.rows[m.Row.ID] = m.Row
		v.addLocked(m.Row)
	case OpDelete:
		old := v.rows[m.ID]
		v.subtractLocked(old)
		delete(v.rows, m.ID)
	}

	afterPtr, afterExists := v.groups[group]
	afterVisible := afterExists &&
		afterPtr.Visible(v.cfg.MinCount, v.cfg.MinSum)

	var after *Agg
	if afterExists {
		a := *afterPtr
		after = &a
	}
	var old *Agg
	if beforeExists {
		b := before
		old = &b
	}

	kind := decide(beforeVisible, afterVisible)
	v.logf("decision group=%q before=%s after=%s filter={count>=%d sum>=%d} kind=%s",
		group, aggText(old, beforeVisible), aggText(after, afterVisible),
		v.cfg.MinCount, v.cfg.MinSum, kind)

	emit := func(op EntryOp, value *Agg) {
		v.seq++
		e := LogEntry{Seq: v.seq, Kind: kind, Op: op, Group: group,
			Old: old, New: after}
		v.log = append(v.log, e)
		v.logf("output seq=%d kind=%s op=%s group=%q value=%s",
			e.Seq, kind, op, group, aggText(value, true))
	}

	switch kind {
	case KindEnter:
		emit(EntryUpsert, after)
	case KindLeave:
		emit(EntryRetract, old)
	case KindChange:
		emit(EntryRetract, old)
		emit(EntryUpsert, after)
	case KindNone:
	}
}

// decide maps the before/after visibility pair to a change kind.
func decide(beforeVisible, afterVisible bool) ChangeKind {
	switch {
	case !beforeVisible && afterVisible:
		return KindEnter
	case beforeVisible && !afterVisible:
		return KindLeave
	case beforeVisible && afterVisible:
		return KindChange
	default:
		return KindNone
	}
}

func (v *View) addLocked(r Row) {
	g, ok := v.groups[r.Group]
	if !ok {
		g = &Agg{Group: r.Group}
		v.groups[r.Group] = g
	}
	g.Count++
	g.Sum += r.Value
}

func (v *View) subtractLocked(r Row) {
	g := v.groups[r.Group]
	g.Count--
	g.Sum -= r.Value
	if g.Count == 0 {
		delete(v.groups, r.Group)
	}
}

func (v *View) logf(format string, args ...any) {
	if v.cfg.Logger != nil {
		v.cfg.Logger.Printf(format, args...)
	}
}

func aggText(a *Agg, visible bool) string {
	if a == nil {
		return "<nil>"
	}
	return fmt.Sprintf("{count:%d sum:%d visible:%t}", a.Count, a.Sum, visible)
}

func sortAggs(a []Agg) {
	sort.Slice(a, func(i, j int) bool { return a[i].Group < a[j].Group })
}
