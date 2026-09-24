// Package view maintains grouped aggregates over the change stream with
// retraction support, orchestrated as Apply -> Recompute -> Commit behind
// a write-ahead journal.
package view

import (
	"errors"
	"os"
	"sync"

	"ontology/agg"
	"ontology/change"
	"ontology/journal"
)

// Result holds one group's published aggregates.
type Result struct {
	Count    int64
	Sum      float64
	Min      float64
	Max      float64
	Distinct int64
}

// Stats exposes the internal recompute counters.
type Stats struct {
	Recomputes   map[agg.Kind]int64
	MemberVisits int64
}

// Phase identifies a crash-injection point inside Apply.
type Phase int

const (
	PhaseNone Phase = iota
	PhaseApply
	PhaseRecompute
	PhasePreCommit
)

var (
	ErrVersionRegression = errors.New("view: version regression")
	ErrMissingGroup      = errors.New("view: missing group key")
	ErrNaN               = errors.New("view: NaN value")
	ErrUnknownRecord     = errors.New("view: unknown record id")
	ErrDuplicateRecord   = errors.New("view: duplicate record id")
	ErrBadOp             = errors.New("view: unknown op")
	ErrCrashed           = errors.New("view: simulated crash")
)

type group struct {
	members  map[uint64]float64
	valRefs  map[float64]int
	count    int64
	sum      float64
	min      float64
	max      float64
	distinct int64
}

type record struct {
	group string
	value float64
}

// View is the in-memory grouped aggregate state plus its WAL.
type View struct {
	mu           sync.RWMutex
	groups       map[string]*group
	records      map[uint64]record
	last         change.Change
	hasLast      bool
	rejected     int64
	recomputes   map[agg.Kind]int64
	memberVisits int64
	journal      *journal.Journal
	crashAt      Phase
}

// New creates a view whose WAL lives in a fresh file under dir.
func New(dir string) (*View, error) {
	f, err := os.CreateTemp(dir, "view-*.log")
	if err != nil {
		return nil, err
	}
	path := f.Name()
	f.Close()
	j, err := journal.Create(path)
	if err != nil {
		return nil, err
	}
	return &View{groups: map[string]*group{}, records: map[uint64]record{},
		recomputes: map[agg.Kind]int64{}, journal: j}, nil
}

// Recover rebuilds a view by replaying the journal at path.
func Recover(path string) (*View, error) {
	recs, _, err := journal.Replay(path)
	if err != nil {
		return nil, err
	}
	j, err := journal.Open(path)
	if err != nil {
		return nil, err
	}
	v := &View{groups: map[string]*group{}, records: map[uint64]record{},
		recomputes: map[agg.Kind]int64{}, journal: j}
	for _, c := range recs {
		if err := v.apply(c, false); err != nil {
			return nil, err
		}
	}
	return v, nil
}

// JournalPath returns the WAL file path.
func (v *View) JournalPath() string { return v.journal.Path() }

// Close closes the WAL.
func (v *View) Close() error { return v.journal.Close() }

// SetCrashHook arms a crash injection point (tests only).
func (v *View) SetCrashHook(p Phase) { v.crashAt = p }
