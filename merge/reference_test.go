package merge

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"sync"
	"testing"
)

// --- naive one-by-one reference --------------------------------------------
//
// naiveApply is an INDEPENDENT reference model: it applies the original events
// one by one to a copy of the pre-batch table, validating each event. The
// merged engine's result, applied to the same pre-batch table, must produce an
// identical final table.

func naiveApply(schema ColumnSet, pre map[string]Row, events []Event) (map[string]Row, *BatchError) {
	rows := deepCopyRows(pre)
	for ix, ev := range events {
		switch ev.Kind {
		case EventInsert:
			if !ColumnsOf(ev.Columns).EqualSet(schema) {
				return nil, &BatchError{Kind: ErrIllegalColumn, Key: ev.Key, EventIx: ix, Detail: "naive: insert columns != schema"}
			}
			for c := range schema {
				if ev.Columns[c].IsAbsent() {
					return nil, &BatchError{Kind: ErrIllegalColumn, Key: ev.Key, Column: c, EventIx: ix, Detail: "naive: absent insert value"}
				}
			}
			if _, ok := rows[ev.Key]; ok {
				return nil, &BatchError{Kind: ErrKeyExists, Key: ev.Key, EventIx: ix}
			}
			nr := Row{}
			for c := range schema {
				nr[c] = ev.Columns[c]
			}
			rows[ev.Key] = nr
		case EventUpdate:
			if len(ev.Columns) == 0 {
				return nil, &BatchError{Kind: ErrIllegalColumn, Key: ev.Key, EventIx: ix, Detail: "naive: empty update"}
			}
			for c, v := range ev.Columns {
				if !schema.Has(c) || v.IsAbsent() {
					return nil, &BatchError{Kind: ErrIllegalColumn, Key: ev.Key, Column: c, EventIx: ix}
				}
			}
			if !ColumnsOf(ev.Before).EqualSet(ColumnsOf(ev.Columns)) {
				return nil, &BatchError{Kind: ErrIllegalColumn, Key: ev.Key, EventIx: ix, Detail: "naive: before set != changed set"}
			}
			cur, ok := rows[ev.Key]
			if !ok {
				return nil, &BatchError{Kind: ErrKeyNotFound, Key: ev.Key, EventIx: ix}
			}
			for c, bv := range ev.Before {
				if !cur[c].Equal(bv) {
					return nil, &BatchError{Kind: ErrBeforeImageMismatch, Key: ev.Key, Column: c, EventIx: ix}
				}
			}
			for c, v := range ev.Columns {
				cur[c] = v
			}
		}
	}
	return rows, nil
}

// --- helpers -----------------------------------------------------------------

func deepCopyRows(m map[string]Row) map[string]Row {
	out := make(map[string]Row, len(m))
	for k, v := range m {
		out[k] = v.Clone()
	}
	return out
}

func keysOf(m map[string]Row) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out) // deterministic iteration => reproducible generation
	return out
}

func randValue(r *rand.Rand) Value {
	switch r.Intn(4) {
	case 0:
		return Null()
	case 1:
		return Str("")
	default:
		return Str(fmt.Sprintf("s%d", r.Intn(4))) // small alphabet forces collisions
	}
}

func randSubset(r *rand.Rand, cols []string) []string {
	perm := r.Perm(len(cols))
	n := 1 + r.Intn(len(cols))
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, cols[perm[i]])
	}
	return out
}

// genBatch builds a VALID random batch against the given pre-state. keyPrefix
// keeps generated insert keys unique across calls.
func genBatch(r *rand.Rand, schema []string, pre map[string]Row, n int, keyPrefix string) []Event {
	sim := deepCopyRows(pre)
	events := make([]Event, 0, n)
	counter := 0
	for len(events) < n {
		existing := keysOf(sim)
		doInsert := len(existing) == 0 || r.Intn(3) == 0
		if doInsert {
			key := fmt.Sprintf("%s%d", keyPrefix, counter)
			counter++
			if _, ok := sim[key]; ok {
				continue
			}
			cols := Row{}
			for _, c := range schema {
				cols[c] = randValue(r)
			}
			events = append(events, ins(key, cols))
			sim[key] = cols.Clone()
		} else {
			key := existing[r.Intn(len(existing))]
			sub := randSubset(r, schema)
			cols := Row{}
			before := Row{}
			for _, c := range sub {
				before[c] = sim[key][c]
				cols[c] = randValue(r)
			}
			events = append(events, upd(key, cols, before))
			for _, c := range sub {
				sim[key][c] = cols[c]
			}
		}
	}
	return events
}

func firstAppearanceRank(events []Event) map[string]int {
	rank := map[string]int{}
	for _, e := range events {
		if _, ok := rank[e.Key]; !ok {
			rank[e.Key] = len(rank)
		}
	}
	return rank
}

func assertOutputInvariants(t *testing.T, schema []string, events []Event, res *Result) {
	t.Helper()
	rank := firstAppearanceRank(events)
	seen := map[string]bool{}
	prev := -1
	for _, ch := range res.Changes {
		if seen[ch.Key] {
			t.Fatalf("duplicate key %q in merged output", ch.Key)
		}
		seen[ch.Key] = true
		rk, ok := rank[ch.Key]
		if !ok {
			t.Fatalf("merged output key %q not present in batch", ch.Key)
		}
		if rk <= prev {
			t.Fatalf("merged output order violates first-appearance order (key %q)", ch.Key)
		}
		prev = rk
		switch ch.Kind {
		case EventUpdate:
			if len(ch.Columns) == 0 {
				t.Fatalf("update change %q has no surviving columns", ch.Key)
			}
			if !ColumnsOf(ch.Before).EqualSet(ColumnsOf(ch.Columns)) {
				t.Fatalf("update change %q: before set != columns set", ch.Key)
			}
			for c, v := range ch.Columns {
				if v.Equal(ch.Before[c]) {
					t.Fatalf("update change %q col %q not pruned (value == before)", ch.Key, c)
				}
			}
		case EventInsert:
			if !ColumnsOf(ch.Columns).EqualSet(NewColumnSet(schema...)) {
				t.Fatalf("insert change %q is not a full row", ch.Key)
			}
		}
	}
}

// --- randomized consistency vs naive reference -------------------------------

func TestConsistencyWithNaiveReference(t *testing.T) {
	r := rand.New(rand.NewSource(1)) // fixed seed => reproducible
	for trial := 0; trial < 300; trial++ {
		tbl := NewTable(testSchema, nil)
		for i := 0; i < r.Intn(4); i++ {
			cols := Row{}
			for _, c := range testSchema {
				cols[c] = randValue(r)
			}
			if _, err := tbl.Commit([]Event{ins(fmt.Sprintf("seed%d", i), cols)}); err != nil {
				t.Fatalf("trial %d seed: %v", trial, err)
			}
		}
		pre := tbl.Snapshot()
		events := genBatch(r, testSchema, pre, 1+r.Intn(14), fmt.Sprintf("t%d_nk", trial))

		res, err := tbl.Commit(events)
		if err != nil {
			t.Fatalf("trial %d: valid batch rejected: %v\nevents=%+v", trial, err, events)
		}
		naive, nerr := naiveApply(NewColumnSet(testSchema...), pre, events)
		if nerr != nil {
			t.Fatalf("trial %d: naive reference rejected: %v", trial, nerr)
		}
		if got := tbl.Snapshot(); !reflect.DeepEqual(got, naive) {
			t.Fatalf("trial %d: merged result != naive one-by-one\nevents=%+v\ngot=%v\nnaive=%v", trial, events, got, naive)
		}
		assertOutputInvariants(t, testSchema, events, res)
		if err := tbl.SelfCheck(); err != nil {
			t.Fatalf("trial %d: self-check: %v", trial, err)
		}
	}
}

// --- Merge/Apply semantics -----------------------------------------------------

func TestMergeDoesNotMutateTable(t *testing.T) {
	tbl := newTable()
	seed(t, tbl, "k", Row{"a": Str("A"), "b": Str("B"), "c": Str("C")})
	before := tbl.Snapshot()
	if _, err := tbl.Merge([]Event{upd("k", Row{"a": Str("Z")}, Row{"a": Str("A")})}); err != nil {
		t.Fatalf("merge: %v", err)
	}
	if !reflect.DeepEqual(before, tbl.Snapshot()) {
		t.Fatal("Merge must not mutate the table")
	}
}

func TestApplyOptimisticConflict(t *testing.T) {
	tbl := newTable()
	seed(t, tbl, "k", Row{"a": Str("A"), "b": Str("B"), "c": Str("C")})

	res, err := tbl.Merge([]Event{upd("k", Row{"a": Str("Z")}, Row{"a": Str("A")})})
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	// A concurrent commit changes the table before we apply.
	if _, err := tbl.Commit([]Event{upd("k", Row{"a": Str("OTHER")}, Row{"a": Str("A")})}); err != nil {
		t.Fatalf("commit: %v", err)
	}
	// Applying the now-stale merge must conflict and change nothing.
	mustErrKind(t, tbl.Apply(res), ErrConflict)
	row, _ := tbl.Get("k")
	if !row["a"].Equal(Str("OTHER")) {
		t.Fatalf("conflicted apply must not write; a=%v", row["a"])
	}
	// A fresh merge against current state applies cleanly.
	res2, err := tbl.Merge([]Event{upd("k", Row{"a": Str("Z")}, Row{"a": Str("OTHER")})})
	if err != nil {
		t.Fatalf("merge2: %v", err)
	}
	if err := tbl.Apply(res2); err != nil {
		t.Fatalf("apply2: %v", err)
	}
	row, _ = tbl.Get("k")
	if !row["a"].Equal(Str("Z")) {
		t.Fatalf("a=%v, want Z", row["a"])
	}
}

// --- concurrency ----------------------------------------------------------------

func TestConcurrentQuerySelfCheckAndCommit(t *testing.T) {
	tbl := NewTable(testSchema, nil)
	for i := 0; i < 8; i++ {
		seed(t, tbl, fmt.Sprintf("k%d", i), Row{"a": Str("0"), "b": Null(), "c": Str("")})
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func(ix int) {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				_, _ = tbl.Get(fmt.Sprintf("k%d", ix%8))
				_ = tbl.Snapshot()
				_ = tbl.Len()
				if err := tbl.SelfCheck(); err != nil {
					t.Errorf("self-check during commits: %v", err)
					return
				}
			}
		}(i)
	}

	// Single writer commits valid batches derived from the current snapshot.
	r := rand.New(rand.NewSource(2))
	for n := 0; n < 300; n++ {
		pre := tbl.Snapshot()
		events := genBatch(r, testSchema, pre, 1+r.Intn(6), fmt.Sprintf("w%d_nk", n))
		if _, err := tbl.Commit(events); err != nil {
			close(stop)
			t.Fatalf("commit %d: %v", n, err)
		}
	}
	close(stop)
	wg.Wait()

	if err := tbl.SelfCheck(); err != nil {
		t.Fatalf("final self-check: %v", err)
	}
}
