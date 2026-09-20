// Command demo exercises the unique-constraint checker end to end and
// prints one OK/FAIL verdict line per scenario, then a summary.
package main

import (
	"fmt"
	"os"
	"sync"

	"ontology"
)

var passed, failed int

func check(ok bool, msg string) {
	if ok {
		passed++
		fmt.Println("OK   " + msg)
	} else {
		failed++
		fmt.Println("FAIL " + msg)
	}
}

func nameStore(n ontology.Normalize, nullsEqual bool) *ontology.Store {
	return ontology.NewStore(n,
		ontology.Constraint{Name: "uq_name", Cols: []string{"name"}, NullsEqual: nullsEqual})
}

func props(v ontology.Value) map[string]ontology.Value {
	return map[string]ontology.Value{"name": v}
}

func main() {
	// 1. All four normalization toggle combinations on "  Alice" / "alice".
	combos := []struct {
		name string
		norm ontology.Normalize
		want bool
	}{
		{"trim=off fold=off", ontology.Normalize{TrimSpace: false, CaseFold: false}, false},
		{"trim=on  fold=off", ontology.Normalize{TrimSpace: true, CaseFold: false}, false},
		{"trim=off fold=on ", ontology.Normalize{TrimSpace: false, CaseFold: true}, false},
		{"trim=on  fold=on ", ontology.Normalize{TrimSpace: true, CaseFold: true}, true},
	}
	for _, c := range combos {
		s := nameStore(c.norm, false)
		_ = s.Insert("a", props(ontology.Str("  Alice")))
		conflict := s.Insert("b", props(ontology.Str("alice"))) != nil
		check(conflict == c.want, fmt.Sprintf("combo %s conflict=%v (want %v)", c.name, conflict, c.want))
	}

	// 2. Read-back is byte-identical to what was written.
	s := nameStore(ontology.Normalize{TrimSpace: true, CaseFold: true}, false)
	raw := "  Aliceß "
	_ = s.Insert("a", props(ontology.Str(raw)))
	got, _ := s.Get("a")
	check(got["name"].Str == raw, fmt.Sprintf("read-back byte-identical: %q", got["name"].Str))

	// 3. NULL semantics: SQL default never conflicts; NullsEqual does.
	sql := nameStore(ontology.Normalize{}, false)
	_ = sql.Insert("a", props(ontology.Null()))
	check(sql.Insert("b", props(ontology.Null())) == nil, "NULL default mode: two NULLs do not conflict")
	eq := nameStore(ontology.Normalize{}, true)
	_ = eq.Insert("a", props(ontology.Null()))
	check(eq.Insert("b", props(ontology.Null())) != nil, "NULLs-equal mode: two NULLs conflict")

	// 4. Empty string and NULL are distinct.
	es := nameStore(ontology.Normalize{}, true)
	_ = es.Insert("empty", props(ontology.Str("")))
	check(es.Insert("null", props(ontology.Null())) == nil, `empty string "" and NULL are distinct`)

	// 5. One conflict report shows both raw values and the normalized key.
	_ = s.Insert("p1", props(ontology.Str("ALICE")))
	err := s.Insert("p2", props(ontology.Str("  alice")))
	ce, isConflict := err.(*ontology.ConflictError)
	check(isConflict, fmt.Sprintf("report: incoming %q vs existing %q (pk=%s) on normalized key %q",
		ce.Incoming["name"].Str, ce.Existing["name"].Str, ce.ExistingPK, ce.Key))

	// 6. Batch: delete then re-insert same normalized key succeeds.
	s.Delete("p1")
	_ = s.Insert("old", props(ontology.Str("Alice")))
	err = s.ApplyBatch([]ontology.Op{
		ontology.DeleteOp("old"),
		ontology.InsertOp("new", props(ontology.Str("  ALICE"))),
	})
	check(err == nil, "batch delete-then-insert same key succeeds")

	// 7. Batch: two inserts with equal normalized keys are rejected.
	fresh := nameStore(ontology.Normalize{TrimSpace: true, CaseFold: true}, false)
	err = fresh.ApplyBatch([]ontology.Op{
		ontology.InsertOp("x", props(ontology.Str("alice"))),
		ontology.InsertOp("y", props(ontology.Str("ALICE"))),
	})
	be, isBatch := err.(*ontology.BatchError)
	check(isBatch && be.OpIndex == 1 && be.OtherOpIndex == 0,
		fmt.Sprintf("batch double insert rejected: op %d clashes with op %d", be.OpIndex, be.OtherOpIndex))
	check(fresh.Len() == 0, "rejected batch left store untouched")

	// 8. Concurrent inserts of the same normalized key: exactly one wins.
	race := nameStore(ontology.Normalize{TrimSpace: true, CaseFold: true}, false)
	const n = 32
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = race.Insert(fmt.Sprintf("pk-%d", i), props(ontology.Str(" Alice")))
		}(i)
	}
	wg.Wait()
	wins := 0
	for _, e := range errs {
		if e == nil {
			wins++
		}
	}
	check(wins == 1 && race.CheckIndex() == nil,
		fmt.Sprintf("concurrent inserts: exactly one success (got %d), index consistent", wins))

	fmt.Printf("summary: %d passed, %d failed\n", passed, failed)
	if failed > 0 {
		os.Exit(1)
	}
}
