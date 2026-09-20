// Command demo exercises the unique-constraint checker end to
// end: normalization toggles, value preservation, NULL semantics,
// conflict reports, atomic batches and concurrent writers.
// It reads no arguments and touches no network.
package main

import (
	"fmt"
	"os"
	"sync"

	"ontology"
)

var passed, total int

func check(ok bool, format string, args ...any) {
	total++
	verdict := "OK  "
	if !ok {
		verdict = "FAIL"
	} else {
		passed++
	}
	fmt.Printf("%s %s\n", verdict, fmt.Sprintf(format, args...))
}

func main() {
	nameUQ := ontology.Constraint{Name: "uniq_name", Columns: []string{"name"}}

	// 1. Four combinations of the two normalization toggles.
	combos := []struct {
		label string
		norm  ontology.NormOptions
		want  bool
	}{
		{"trim=off fold=off", ontology.NormOptions{TrimSpace: false, CaseFold: false}, false},
		{"trim=on  fold=off", ontology.NormOptions{TrimSpace: true, CaseFold: false}, false},
		{"trim=off fold=on ", ontology.NormOptions{TrimSpace: false, CaseFold: true}, false},
		{"trim=on  fold=on ", ontology.NormOptions{TrimSpace: true, CaseFold: true}, true},
	}
	for _, c := range combos {
		s := ontology.New(c.norm, nameUQ)
		_ = s.Insert("r1", map[string]ontology.Value{"name": ontology.Str("  Alice")})
		got := s.Insert("r2", map[string]ontology.Value{"name": ontology.Str("alice")}) != nil
		check(got == c.want, "norm[%s]: %q vs %q conflict=%v (want %v)",
			c.label, "  Alice", "alice", got, c.want)
	}

	// 2. Read-back is byte-identical to the written value.
	norm := ontology.NormOptions{TrimSpace: true, CaseFold: true}
	s := ontology.New(norm, nameUQ)
	const original = "  Alice Straße İ "
	_ = s.Insert("r1", map[string]ontology.Value{"name": ontology.Str(original)})
	rec, _ := s.Get("r1")
	check(rec.Props["name"].String() == original, "read-back byte-identical: %q", rec.Props["name"].String())

	// 3. NULL semantics: SQL default vs nulls-equal mode.
	sql := ontology.New(norm, nameUQ)
	_ = sql.Insert("n1", map[string]ontology.Value{"name": ontology.Null()})
	err := sql.Insert("n2", map[string]ontology.Value{"name": ontology.Null()})
	check(err == nil, "NULL default mode: two NULLs do not conflict")
	eq := ontology.New(norm, ontology.Constraint{Name: "uniq_name", Columns: []string{"name"}, NullsEqual: true})
	_ = eq.Insert("n1", map[string]ontology.Value{"name": ontology.Null()})
	err = eq.Insert("n2", map[string]ontology.Value{"name": ontology.Null()})
	check(err != nil, "NULL equal mode: two NULLs conflict")

	// 4. Empty string is not NULL.
	dist := ontology.New(norm, ontology.Constraint{Name: "uniq_name", Columns: []string{"name"}, NullsEqual: true})
	_ = dist.Insert("e1", map[string]ontology.Value{"name": ontology.Str("")})
	err = dist.Insert("e2", map[string]ontology.Value{"name": ontology.Null()})
	check(err == nil, "empty string and NULL are distinct (no conflict)")

	// 5. Conflict report carries both originals and the folded key.
	_ = s.Insert("u1", map[string]ontology.Value{"name": ontology.Str("ALICE")})
	err = s.Insert("u2", map[string]ontology.Value{"name": ontology.Str("  alice")})
	ce, ok := err.(*ontology.ConflictError)
	check(ok && ce.ExistingID == "u1" && ce.Key == "alice" &&
		ce.Incoming[0].String() == "  alice" && ce.Existing[0].String() == "ALICE",
		"report: constraint=%q existing=%q key=%q incoming=%q existing-value=%q",
		ce.Constraint, ce.ExistingID, ce.Key, ce.Incoming[0].String(), ce.Existing[0].String())

	// 6. Batch: delete then insert of the same folded key succeeds.
	b := ontology.New(norm, nameUQ)
	_ = b.Insert("old", map[string]ontology.Value{"name": ontology.Str("Alice")})
	err = b.Apply(
		ontology.DeleteOp("old"),
		ontology.InsertOp("new", map[string]ontology.Value{"name": ontology.Str("  ALICE")}),
	)
	check(err == nil, "batch delete-then-insert of same key succeeded")

	// 7. Batch: two inserts of the same folded key are rejected.
	err = b.Apply(
		ontology.InsertOp("b1", map[string]ontology.Value{"name": ontology.Str("Bob")}),
		ontology.InsertOp("b2", map[string]ontology.Value{"name": ontology.Str(" BOB ")}),
	)
	be, ok := err.(*ontology.BatchError)
	check(ok && be.Index == 1 && be.OtherIndex == 0,
		"batch double-insert rejected: op %d conflicts with op %d", be.Index+1, be.OtherIndex+1)
	_, alive := b.Get("b1")
	check(!alive, "aborted batch left no trace (b1 absent)")

	// 8. Concurrent inserts of one folded key: exactly one wins.
	c := ontology.New(norm, nameUQ)
	const n = 32
	var wg sync.WaitGroup
	var wins int64
	var mu sync.Mutex
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("c%d", i)
			if c.Insert(id, map[string]ontology.Value{"name": ontology.Str("Alice")}) == nil {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	check(wins == 1 && c.SelfCheck() == nil, "concurrent inserts: exactly %d winner, index consistent", wins)

	fmt.Printf("TOTAL %d/%d checks passed\n", passed, total)
	if passed != total {
		os.Exit(1)
	}
}
