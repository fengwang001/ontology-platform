package audit_test

import (
	"fmt"
	"math/rand/v2"
	"testing"

	"ontology/audit"
	"ontology/change"
	"ontology/view"
)

// row describes a currently live base-table record in the generator.
type row struct {
	group string
	value float64
}

// TestIncrementalEqualsFullReplay drives 50,000 random valid changes
// (insert/delete/update mixed, including group moves) through the view and
// audits against full recomputation every 1,000 changes.
func TestIncrementalEqualsFullReplay(t *testing.T) {
	v, err := view.New(view.Options{JournalPath: ""})
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	ref := audit.NewReference()

	rng := rand.New(rand.NewPCG(20260922, 42))
	live := map[string]row{}
	var nextKey int

	valid := func(c change.Change) bool {
		switch c.Op {
		case change.OpInsert:
			if _, exists := live[c.Key]; exists {
				return false
			}
		case change.OpDelete, change.OpUpdate:
			if _, exists := live[c.Key]; !exists {
				return false
			}
		}
		return true
	}

	const total = 50_000
	var version uint64
	for i := 0; i < total; i++ {
		// Keep a healthy live set so all three operations stay possible.
		choice := rng.IntN(3)
		if len(live) < 50 {
			choice = 0
		}
		var c change.Change
		switch choice {
		case 0: // insert
			nextKey++
			k := fmt.Sprintf("k%06d", nextKey)
			c = mkIns(version+1, k, fmt.Sprintf("g%d", rng.IntN(20)), rng.Float64()*100-50)
		case 1: // delete
			var k string
			for kk := range live {
				k = kk
				break
			}
			r := live[k]
			c = mkDel(version+1, k, r)
		default: // update (may move groups)
			var k string
			for kk := range live {
				k = kk
				break
			}
			old := live[k]
			c = mkUpd(version+1, k, old, row{
				group: fmt.Sprintf("g%d", rng.IntN(20)),
				value: rng.Float64()*100 - 50,
			})
		}
		if !valid(c) {
			i--
			continue
		}
		if err := v.Submit(c); err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
		ref.Apply(c)
		version++
		switch c.Op {
		case change.OpInsert:
			live[c.Key] = row{c.From.Group, c.From.Value}
		case change.OpDelete:
			delete(live, c.Key)
		case change.OpUpdate:
			live[c.Key] = row{c.To.Group, c.To.Value}
		}

		if (i+1)%1000 == 0 {
			if mm := audit.Check(v, ref); mm != nil {
				t.Fatalf("after %d changes: %v", i+1, mm)
			}
		}
	}
	if mm := audit.Check(v, ref); mm != nil {
		t.Fatalf("final: %v", mm)
	}
}

func mkRow(g string, val float64) change.Row {
	return change.Row{Group: g, GroupPresent: true, Value: val}
}

func mkIns(vn uint64, key, g string, val float64) change.Change {
	return change.Change{Version: vn, Op: change.OpInsert, Key: key, From: mkRow(g, val)}
}

func mkDel(vn uint64, key string, r row) change.Change {
	return change.Change{Version: vn, Op: change.OpDelete, Key: key, From: mkRow(r.group, r.value)}
}

func mkUpd(vn uint64, key string, old, nw row) change.Change {
	return change.Change{Version: vn, Op: change.OpUpdate, Key: key,
		From: mkRow(old.group, old.value), To: mkRow(nw.group, nw.value)}
}
