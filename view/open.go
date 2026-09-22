package view

import (
	"os"

	"ontology/agg"
	"ontology/change"
	"ontology/journal"
)

// Options configures a View.
type Options struct {
	// JournalPath is the local log file. Empty disables durability.
	JournalPath string
	// CrashHook, when non-nil, is invoked at the named phase during
	// Submit and again during recovery replay. Returning an error aborts
	// the in-memory Submit (used to simulate a crash before the journal
	// append) and the change is not applied.
	CrashHook func(p Phase, c change.Change) error
}

// New creates an empty view and replays any existing journal at path.
// A torn tail is classified and ignored: recovery applies exactly the
// intact prefix of the log.
func New(opts Options) (*View, error) {
	v := &View{
		journalPath: opts.JournalPath,
		versionSeen: map[uint64]uint64{},
		members:     map[string]memberEntry{},
		groups:      map[string]*groupState{},
	}
	v.stats = Stats{
		TriggersByKind: map[agg.Kind]int64{},
		MembersByKind:  map[agg.Kind]int64{},
	}
	v.hook = opts.CrashHook
	if opts.JournalPath != "" {
		recs, perr := journal.ReadAll(opts.JournalPath)
		for _, c := range recs {
			if err := v.applyReplay(c); err != nil {
				return nil, err
			}
		}
		if perr != nil {
			// Intact prefix replayed; classify for the caller via nil
			// return. TailClass records the category for inspection.
			v.tailClass = journal.Classify(perr)
		}
		if _, err := os.Stat(opts.JournalPath); err == nil {
			w, err := journal.Open(opts.JournalPath)
			if err != nil {
				return nil, err
			}
			v.w = w
		} else if os.IsNotExist(err) {
			w, err := journal.Create(opts.JournalPath)
			if err != nil {
				return nil, err
			}
			v.w = w
		} else {
			return nil, err
		}
	}
	return v, nil
}

// Close releases journal resources.
func (v *View) Close() error {
	if v.w != nil {
		return v.w.Close()
	}
	return nil
}

func newGroup() *groupState {
	g := &groupState{
		members: map[string]float64{},
		aggs:    map[agg.Kind]agg.Aggregator{},
		dirty:   map[agg.Kind]bool{},
	}
	for _, k := range agg.Family {
		g.aggs[k] = agg.New(k)
	}
	return g
}
