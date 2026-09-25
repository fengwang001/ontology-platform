package main

import (
	"errors"
	"fmt"
	"os"

	"ontology/agg"
	"ontology/audit"
	"ontology/change"
	"ontology/journal"
	"ontology/view"
)

type verdict struct {
	ok   bool
	name string
}

func check(name string, ok bool) verdict { return verdict{ok: ok, name: name} }

func main() {
	path := "/tmp/ontology-demo.journal"
	_ = os.Remove(path)
	results := []verdict{}
	in := change.Change{Version: 1, Op: change.Insert, ID: "r1", HasGroup: true, Group: "", Value: -0}
	body, err := in.Encode()
	out, derr := change.Decode(body)
	results = append(results, check("change round-trip", err == nil && derr == nil && change.Equal(in, out)))
	countAgg := agg.New(agg.Count)
	countAgg.Insert(1)
	results = append(results, check("count decrements without members", !countAgg.NeedsMembers() && countAgg.Delete(1) == false))
	minAgg := agg.New(agg.Min)
	minAgg.Insert(3)
	minAgg.Insert(1)
	results = append(results, check("min removal asks for members", minAgg.NeedsMembers() && minAgg.Delete(1)))
	distinct := agg.New(agg.DistinctCount)
	distinct.Insert(2)
	results = append(results, check("distinct deletion asks for members", distinct.NeedsMembers() && distinct.Delete(2)))
	j, err := journal.Create(path)
	appendOK := err == nil && j.Append(in) == nil
	closeOK := j.Close() == nil
	replayed, replayErr := journal.Replay(path)
	results = append(results, check("journal append and replay", appendOK && closeOK && replayErr == nil && len(replayed) == 1))
	vw := view.New()
	insert := func(ver uint64, id, g string, value float64) error {
		return vw.Submit(change.Change{Version: ver, Op: change.Insert, ID: id, HasGroup: true, Group: g, Value: value})
	}
	okView := insert(1, "only", "g", 7) == nil
	countValue, exists := vw.Lookup("g", agg.Count)
	deleteOnly := vw.Submit(change.Change{Version: 2, Op: change.Delete, ID: "only", HasGroup: true, Group: "g", Value: 7}) == nil
	_, stillExists := vw.Lookup("g", agg.Count)
	dup := insert(2, "x", "x", 1)
	results = append(results, check("empty group disappears", okView && countValue == 1 && exists && deleteOnly && !stillExists && len(vw.Groups()) == 0 && errors.Is(dup, view.ErrVersionDup)))
	audited := view.New()
	auditChange := change.Change{Version: 1, Op: change.Insert, ID: "a1", HasGroup: true, Group: "g", Value: 2.5}
	_ = audited.Submit(auditChange)
	results = append(results, check("audit equals full recompute", audit.Check(audited, []change.Change{auditChange}) == nil))

	for _, r := range results {
		if r.ok {
			fmt.Println("OK", r.name)
		} else {
			fmt.Println("FAIL", r.name)
		}
	}
}
