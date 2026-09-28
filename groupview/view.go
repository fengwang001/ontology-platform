package groupview

import (
	"io"
	"sort"
	"strings"
	"sync"
)

// Config holds the filter thresholds and the hard group-count limit. A group
// is visible iff Count >= MinCount AND Sum >= MinSum. MaxGroups bounds the
// number of distinct live groups (groups with at least one row).
type Config struct {
	MinCount  int64
	MinSum    int64
	MaxGroups int
}

// Option customizes a View at construction.
type Option func(*View)

// WithLogWriter sends the deterministic per-batch decision log to w. When
// unset, nothing is logged.
func WithLogWriter(w io.Writer) Option {
	return func(v *View) { v.log = &bufferedLogger{w: w} }
}

type storedRow struct {
	group string
	value int64
}

// View incrementally maintains a filtered grouped aggregate view.
type View struct {
	mu   sync.RWMutex
	cfg  Config
	rows map[string]storedRow
	agg  map[string]Aggregate
	seq  int64
	log  *bufferedLogger
}

// New constructs an empty View.
func New(cfg Config, opts ...Option) *View {
	v := &View{
		cfg:  cfg,
		rows: map[string]storedRow{},
		agg:  map[string]Aggregate{},
	}
	for _, opt := range opts {
		opt(v)
	}
	return v
}

func (v *View) visible(a Aggregate) bool {
	return a.Count >= v.cfg.MinCount && a.Sum >= v.cfg.MinSum
}

// Apply validates and applies one batch, returning the ordered net-change
// entries (retractions before upserts per mutation). An invalid batch is
// rejected atomically: aggregates, the downstream view and the produced
// change log stay untouched.
func (v *View) Apply(batch []Mutation) ([]Entry, error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	seq := v.seq + 1
	v.log.reset()
	v.log.logf("BATCH seq=%d size=%d INPUT-BEGIN", seq, len(batch))
	for i, m := range batch {
		v.log.logf("  input[%d] %s", i, mutationText(m))
	}

	simRows := make(map[string]storedRow, len(v.rows))
	for id, row := range v.rows {
		simRows[id] = row
	}
	simAgg := make(map[string]Aggregate, len(v.agg))
	for g, a := range v.agg {
		simAgg[g] = a
	}

	entries := make([]Entry, 0, len(batch)*2)
	decisions := make([]decision, 0, len(batch))

	reject := func(i int, reason RejectReason, format string, args ...any) error {
		err := &BatchError{
			Reason: reason,
			Index:  i,
			Detail: reason.String() + ": " + sprintf(format, args...),
		}
		v.log.reset()
		v.log.rejectf("REJECTED seq=%d index=%d reason=%s detail=%q",
			seq, i, reason.String(), err.Detail)
		return err
	}

	for i, m := range batch {
		if strings.TrimSpace(m.Group) == "" {
			return nil, reject(i, ReasonEmptyGroup,
				"mutation %d uses an empty group name", i)
		}

		switch m.Op {
		case Insert:
			if _, exists := simRows[m.RowID]; exists {
				return nil, reject(i, ReasonDuplicateRow,
					"row %q already exists", m.RowID)
			}
			if _, known := simAgg[m.Group]; !known {
				if len(simAgg) >= v.cfg.MaxGroups {
					return nil, reject(i, ReasonTooManyGroups,
						"inserting group %q would exceed MaxGroups=%d (current groups=%d)",
						m.Group, v.cfg.MaxGroups, len(simAgg))
				}
			}
		case Delete:
			if _, exists := simRows[m.RowID]; !exists {
				return nil, reject(i, ReasonDeleteMissing,
					"row %q does not exist", m.RowID)
			}
		default:
			return nil, reject(i, RejectReason(0),
				"unknown op %d at mutation %d", m.Op, i)
		}

		before := simAgg[m.Group]
		inView := v.visible(before)

		switch m.Op {
		case Insert:
			simRows[m.RowID] = storedRow{group: m.Group, value: m.Value}
			a := simAgg[m.Group]
			a.Count++
			a.Sum += m.Value
			simAgg[m.Group] = a
		case Delete:
			row := simRows[m.RowID]
			delete(simRows, m.RowID)
			a := simAgg[row.group]
			a.Count--
			a.Sum -= row.value
			if a.Count == 0 {
				delete(simAgg, row.group)
			} else {
				simAgg[row.group] = a
			}
		}

		after, live := simAgg[m.Group]
		outView := live && v.visible(after)

		var action string
		switch {
		case !inView && outView:
			action = "ENTER"
			entries = append(entries, Entry{Group: m.Group, Kind: Upsert, Value: after})
		case inView && !outView:
			action = "LEAVE"
			entries = append(entries, Entry{Group: m.Group, Kind: Retract, Value: before})
		case inView && outView:
			action = "CHANGE"
			entries = append(entries,
				Entry{Group: m.Group, Kind: Retract, Value: before},
				Entry{Group: m.Group, Kind: Upsert, Value: after},
			)
		default:
			action = "SKIP"
		}
		decisions = append(decisions, decision{
			group: m.Group, before: before, after: after,
			inView: inView, outView: outView, action: action,
		})
	}

	v.rows = simRows
	v.agg = simAgg
	v.seq = seq

	v.log.logf("DECISIONS seq=%d rule=%s", seq, v.ruleText())
	for i, d := range decisions {
		v.log.logf("  decision[%d] %s", i, d.explain(v.cfg))
	}
	v.log.logf("OUTPUT seq=%d entries=%d OUTPUT-BEGIN", seq, len(entries))
	for _, e := range entries {
		v.log.logf("  %s", entryText(e))
	}
	v.log.logf("COMMITTED seq=%d visibleGroups=%d", seq, len(v.visibleGroupsLocked()))
	v.log.flush()

	return entries, nil
}

func (v *View) ruleText() string {
	return "visible=(count>=" + itoa(v.cfg.MinCount) + " && sum>=" +
		itoa(v.cfg.MinSum) + "); disappears when count==0; maxGroups=" +
		itoa(int64(v.cfg.MaxGroups))
}

func (v *View) visibleGroupsLocked() []string {
	groups := make([]string, 0, len(v.agg))
	for g, a := range v.agg {
		if v.visible(a) {
			groups = append(groups, g)
		}
	}
	sort.Strings(groups)
	return groups
}

// Snapshot returns a point-in-time copy of the visible view. It is safe to
// call concurrently with Apply and with other Snapshots; every returned
// group satisfies the filter thresholds.
func (v *View) Snapshot() map[string]Aggregate {
	v.mu.RLock()
	defer v.mu.RUnlock()

	out := make(map[string]Aggregate, len(v.agg))
	for g, a := range v.agg {
		if v.visible(a) {
			out[g] = a
		}
	}
	return out
}
