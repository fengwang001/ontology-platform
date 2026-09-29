package ontology

import "sort"

// referenceStore is the naive oracle: tombstones are never removed.
type referenceStore struct {
	rows       map[string]Row
	tombstones map[string]int64
	watermark  int64
	ignored    int64
}

func newReferenceStore() *referenceStore {
	return &referenceStore{
		rows:       map[string]Row{},
		tombstones: map[string]int64{},
	}
}

func (r *referenceStore) apply(events []Event) {
	for _, ev := range events {
		if ev.Version > r.watermark {
			r.watermark = ev.Version
		}
		cur, ok := r.rows[ev.Key]
		if !ok {
			cur.Version = r.tombstones[ev.Key]
		}
		if ev.Version <= cur.Version {
			r.ignored++
			continue
		}
		switch ev.Op {
		case OpWrite:
			r.rows[ev.Key] = Row{Key: ev.Key, Value: ev.Value, Version: ev.Version}
			delete(r.tombstones, ev.Key)
		case OpDelete:
			delete(r.rows, ev.Key)
			r.tombstones[ev.Key] = ev.Version
		}
	}
}

func (r *referenceStore) liveRows() []Row {
	out := make([]Row, 0, len(r.rows))
	for _, row := range r.rows {
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

func (r *referenceStore) liveStones() []Tombstone {
	out := make([]Tombstone, 0, len(r.tombstones))
	for k, v := range r.tombstones {
		out = append(out, Tombstone{Key: k, Version: v})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}
