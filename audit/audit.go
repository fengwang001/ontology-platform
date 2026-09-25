package audit

import (
	"fmt"
	"math"
	"sort"

	"ontology/agg"
	"ontology/change"
	"ontology/view"
)

type rec struct {
	group string
	value float64
	ord   uint64
}

type Mismatch struct {
	Group string
	Kind  agg.Kind
	Want  float64
	Got   float64
}

func (m Mismatch) Error() string {
	return fmt.Sprintf("mismatch group=%q kind=%s want=%v got=%v", m.Group, m.Kind, m.Want, m.Got)
}

func Recompute(changes []change.Change) (map[string]map[agg.Kind]float64, error) {
	records := map[string]rec{}
	for _, c := range changes {
		if err := view.Validate(c); err != nil {
			return nil, err
		}
		switch c.Op {
		case change.Insert:
			records[c.ID] = rec{c.Group, c.Value, c.Version}
		case change.Delete:
			delete(records, c.ID)
		case change.Update:
			records[c.ID] = rec{c.NewGroup, c.NewValue, c.Version}
		}
	}
	type current struct {
		id  string
		rec rec
	}
	currentRecords := make([]current, 0, len(records))
	for id, r := range records {
		currentRecords = append(currentRecords, current{id, r})
	}
	sort.Slice(currentRecords, func(i, j int) bool {
		if currentRecords[i].rec.ord != currentRecords[j].rec.ord {
			return currentRecords[i].rec.ord < currentRecords[j].rec.ord
		}
		return currentRecords[i].id < currentRecords[j].id
	})
	groups := map[string][]agg.Member{}
	for _, r := range currentRecords {
		groups[r.rec.group] = append(groups[r.rec.group], agg.Member{ID: r.id, Value: r.rec.value})
	}
	out := map[string]map[agg.Kind]float64{}
	for g, members := range groups {
		out[g] = map[agg.Kind]float64{}
		for _, k := range agg.All() {
			a := agg.New(k)
			a.Recompute(members)
			value, ok := a.Snapshot()
			if ok {
				out[g][k] = value
			}
		}
	}
	return out, nil
}

func Compare(v *view.View, expected map[string]map[agg.Kind]float64) error {
	got := v.Snapshot()
	if len(got) != len(expected) {
		return fmt.Errorf("group count want %d got %d", len(expected), len(got))
	}
	for g, wantGroup := range expected {
		gotGroup, ok := got[g]
		if !ok {
			return fmt.Errorf("missing group %q", g)
		}
		for _, k := range agg.All() {
			want, wok := wantGroup[k]
			got, gok := gotGroup[k]
			if wok != gok || math.Float64bits(want) != math.Float64bits(got) {
				return Mismatch{Group: g, Kind: k, Want: want, Got: got}
			}
		}
	}
	return nil
}

func Check(v *view.View, changes []change.Change) error {
	expected, err := Recompute(changes)
	if err != nil {
		return err
	}
	return Compare(v, expected)
}
