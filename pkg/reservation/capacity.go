package reservation

import "sort"

type CapacityRecord struct {
	EffectiveAt int
	Capacity    int
}

type capacityTable struct {
	recs []CapacityRecord
}

// newCapacityTable builds the table from initial records. The initial table
// must define the capacity for all times the system may reach; records with
// equal effective time keep the last one.
func newCapacityTable(initial []CapacityRecord) *capacityTable {
	t := &capacityTable{}
	for _, rec := range initial {
		t.add(rec)
	}
	return t
}

// at returns the capacity at tm: the record with the greatest EffectiveAt <= tm.
func (t *capacityTable) at(tm int) int {
	i := sort.Search(len(t.recs), func(i int) bool { return t.recs[i].EffectiveAt > tm }) - 1
	if i < 0 {
		return 0
	}
	return t.recs[i].Capacity
}

// add inserts (or replaces at the same EffectiveAt) a record while keeping
// the table ordered.
func (t *capacityTable) add(rec CapacityRecord) {
	i := sort.Search(len(t.recs), func(i int) bool { return t.recs[i].EffectiveAt >= rec.EffectiveAt })
	if i < len(t.recs) && t.recs[i].EffectiveAt == rec.EffectiveAt {
		t.recs[i].Capacity = rec.Capacity
		return
	}
	t.recs = append(t.recs, CapacityRecord{})
	copy(t.recs[i+1:], t.recs[i:])
	t.recs[i] = rec
}

func (t *capacityTable) len() int { return len(t.recs) }

func (t *capacityTable) records() []CapacityRecord {
	out := make([]CapacityRecord, len(t.recs))
	copy(out, t.recs)
	return out
}

// effectiveTimesIn appends record EffectiveAt values within [lo, hi).
func (t *capacityTable) effectiveTimesIn(lo, hi int, out []int) []int {
	i := sort.Search(len(t.recs), func(i int) bool { return t.recs[i].EffectiveAt >= lo })
	for ; i < len(t.recs) && t.recs[i].EffectiveAt < hi; i++ {
		out = append(out, t.recs[i].EffectiveAt)
	}
	return out
}
