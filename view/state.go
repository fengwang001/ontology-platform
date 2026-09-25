package view

import (
	"sync"

	"ontology/agg"
	"ontology/change"
	"ontology/journal"
)

type Metrics struct {
	Recomputes     int
	MembersScanned int
	Rejected       int
}

type member struct {
	group string
	value float64
}

type groupState struct {
	members map[string]float64
	aggs    map[agg.Kind]agg.Aggregator
}

type View struct {
	mu       sync.RWMutex
	groups   map[string]*groupState
	records  map[string]member
	version  uint64
	last     change.Change
	rejected int
	recomp   map[agg.Kind]int
	scanned  map[agg.Kind]int
	journal  *journal.Journal
	crash    CrashPoint
}

func New() *View {
	return &View{groups: map[string]*groupState{}, records: map[string]member{},
		recomp: map[agg.Kind]int{}, scanned: map[agg.Kind]int{}}
}

func Create(path string) (*View, error) {
	j, err := journal.Create(path)
	if err != nil {
		return nil, err
	}
	v := New()
	v.journal = j
	return v, nil
}

func Recover(path string) (*View, error) {
	cs, err := journal.Replay(path)
	if err != nil {
		return nil, err
	}
	j, err := journal.Open(path)
	if err != nil {
		return nil, err
	}
	v := New()
	v.journal = j
	for _, c := range cs {
		v.mu.Lock()
		err = v.advance(c, false)
		v.mu.Unlock()
		if err != nil {
			return nil, err
		}
	}
	return v, nil
}

type savedState struct {
	groups  map[string]map[string]float64
	records map[string]member
	version uint64
	last    change.Change
}

func (v *View) snapshotState() savedState {
	s := savedState{groups: map[string]map[string]float64{}, records: map[string]member{}, version: v.version, last: v.last}
	for id, m := range v.records {
		s.records[id] = m
	}
	for g, gs := range v.groups {
		s.groups[g] = map[string]float64{}
		for id, value := range gs.members {
			s.groups[g][id] = value
		}
	}
	return s
}

func (v *View) restoreState(s savedState) {
	v.groups = map[string]*groupState{}
	v.records = map[string]member{}
	for id, m := range s.records {
		v.records[id] = m
	}
	for g, members := range s.groups {
		gs := v.ensureGroup(g)
		for id, value := range members {
			gs.members[id] = value
			for _, a := range gs.aggs {
				a.Insert(value)
			}
		}
	}
	v.version = s.version
	v.last = s.last
}

func (v *View) SetCrashPoint(p CrashPoint) { v.crash = p }
func (v *View) ClearCrashPoint()           { v.crash = "" }

func (v *View) Close() error {
	if v.journal == nil {
		return nil
	}
	return v.journal.Close()
}

func (v *View) Version() uint64 {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.version
}

func (v *View) Rejected() int {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.rejected
}

func (v *View) Metrics() Metrics {
	v.mu.RLock()
	defer v.mu.RUnlock()
	var recomp, scanned int
	for _, n := range v.recomp {
		recomp += n
	}
	for _, n := range v.scanned {
		scanned += n
	}
	return Metrics{recomp, scanned, v.rejected}
}

func (v *View) RecomputeCount(k agg.Kind) int {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.recomp[k]
}

func (v *View) MembersScannedFor(k agg.Kind) int {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.scanned[k]
}

func (v *View) Lookup(g string, k agg.Kind) (float64, bool) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	if gs := v.groups[g]; gs != nil {
		return gs.aggs[k].Snapshot()
	}
	return 0, false
}

func (v *View) Groups() []string {
	v.mu.RLock()
	defer v.mu.RUnlock()
	out := make([]string, 0, len(v.groups))
	for g := range v.groups {
		out = append(out, g)
	}
	return out
}

func (v *View) Snapshot() map[string]map[agg.Kind]float64 {
	v.mu.RLock()
	defer v.mu.RUnlock()
	out := map[string]map[agg.Kind]float64{}
	for g, gs := range v.groups {
		out[g] = map[agg.Kind]float64{}
		for k, a := range gs.aggs {
			value, _ := a.Snapshot()
			out[g][k] = value
		}
	}
	return out
}
