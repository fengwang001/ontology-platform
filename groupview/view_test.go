package groupview

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// applyEntries simulates a downstream KV store applying the ordered log:
// retractions delete the key, upserts set it.
func applyEntries(store map[string]Aggregate, entries []Entry) {
	for _, e := range entries {
		if e.Kind == Retract {
			delete(store, e.Group)
		} else {
			store[e.Group] = e.Value
		}
	}
}

func mustApply(t *testing.T, v *View, batch []Mutation) []Entry {
	t.Helper()
	entries, err := v.Apply(batch)
	if err != nil {
		t.Fatalf("Apply(%v) unexpected error: %v", batch, err)
	}
	return entries
}

func wantReason(t *testing.T, err error, want RejectReason) {
	t.Helper()
	var be *BatchError
	if !errors.As(err, &be) {
		t.Fatalf("want *BatchError, got %T: %v", err, err)
	}
	if be.Reason != want {
		t.Fatalf("want reason %s, got %s (%v)", want, be.Reason, err)
	}
	if be.Reason.String() == "" || be.Reason.String() == "UNKNOWN" {
		t.Fatalf("reason %v has no distinguishable token", be.Reason)
	}
}

func TestThresholdExactBoundary(t *testing.T) {
	v := New(Config{MinCount: 2, MinSum: 10, MaxGroups: 10})
	store := map[string]Aggregate{}

	applyEntries(store, mustApply(t, v, []Mutation{
		{Op: Insert, RowID: "r1", Group: "g", Value: 10},
	}))
	if _, ok := store["g"]; ok {
		t.Fatalf("group should not appear below count threshold: %+v", store)
	}

	entries := mustApply(t, v, []Mutation{
		{Op: Insert, RowID: "r2", Group: "g", Value: 0},
	})
	if len(entries) != 1 || entries[0].Kind != Upsert ||
		entries[0].Value != (Aggregate{Count: 2, Sum: 10}) {
		t.Fatalf("want single ENTER upsert {2,10}, got %+v", entries)
	}
	applyEntries(store, entries)
	if store["g"] != (Aggregate{Count: 2, Sum: 10}) {
		t.Fatalf("downstream view mismatch: %+v", store)
	}

	entries = mustApply(t, v, []Mutation{
		{Op: Insert, RowID: "r3", Group: "g", Value: 5},
	})
	if len(entries) != 2 ||
		entries[0] != (Entry{Group: "g", Kind: Retract, Value: Aggregate{Count: 2, Sum: 10}}) ||
		entries[1] != (Entry{Group: "g", Kind: Upsert, Value: Aggregate{Count: 3, Sum: 15}}) {
		t.Fatalf("want CHANGE retract/upsert, got %+v", entries)
	}
	applyEntries(store, entries)
	if store["g"] != (Aggregate{Count: 3, Sum: 15}) {
		t.Fatalf("downstream view mismatch after change: %+v", store)
	}

	entries = mustApply(t, v, []Mutation{{Op: Delete, RowID: "r3"}})
	if len(entries) != 2 || entries[0].Kind != Retract ||
		entries[1] != (Entry{Group: "g", Kind: Upsert, Value: Aggregate{Count: 2, Sum: 10}}) {
		t.Fatalf("want CHANGE back to boundary, got %+v", entries)
	}
	applyEntries(store, entries)

	entries = mustApply(t, v, []Mutation{{Op: Delete, RowID: "r2"}})
	if len(entries) != 1 || entries[0].Kind != Retract ||
		entries[0].Value != (Aggregate{Count: 2, Sum: 10}) {
		t.Fatalf("want single LEAVE retract of {2,10}, got %+v", entries)
	}
	applyEntries(store, entries)
	if _, ok := store["g"]; ok {
		t.Fatalf("group should have left the view: %+v", store)
	}

	if got := v.Snapshot(); len(got) != 0 {
		t.Fatalf("snapshot should be empty, got %+v", got)
	}
}

func TestCountToZeroGroupDisappears(t *testing.T) {
	v := New(Config{MinCount: 1, MinSum: 1, MaxGroups: 2})
	store := map[string]Aggregate{}

	applyEntries(store, mustApply(t, v, []Mutation{
		{Op: Insert, RowID: "a", Group: "alpha", Value: 1},
		{Op: Insert, RowID: "b", Group: "beta", Value: 2},
	}))
	if store["alpha"] != (Aggregate{Count: 1, Sum: 1}) ||
		store["beta"] != (Aggregate{Count: 1, Sum: 2}) {
		t.Fatalf("unexpected view: %+v", store)
	}

	applyEntries(store, mustApply(t, v, []Mutation{{Op: Delete, RowID: "a"}}))
	if _, ok := store["alpha"]; ok {
		t.Fatalf("alpha must disappear when count reaches zero: %+v", store)
	}

	applyEntries(store, mustApply(t, v, []Mutation{
		{Op: Insert, RowID: "c", Group: "gamma", Value: 3},
	}))
	if store["gamma"] != (Aggregate{Count: 1, Sum: 3}) {
		t.Fatalf("group slot should be reusable after disappearance: %+v", store)
	}

	entries := mustApply(t, v, []Mutation{{Op: Delete, RowID: "b"}})
	if len(entries) != 1 || entries[0].Kind != Retract {
		t.Fatalf("deleting visible beta should emit one retract, got %+v", entries)
	}
	applyEntries(store, entries)

	if entries := mustApply(t, v, []Mutation{{Op: Delete, RowID: "c"}}); len(entries) != 0 {
		t.Fatalf("deleting an invisible row must emit nothing, got %+v", entries)
	}
}

func TestBelowOnOneAxisStaysHidden(t *testing.T) {
	v := New(Config{MinCount: 2, MinSum: 100, MaxGroups: 10})
	if e := mustApply(t, v, []Mutation{
		{Op: Insert, RowID: "r1", Group: "g", Value: 1},
		{Op: Insert, RowID: "r2", Group: "g", Value: 1},
	}); len(e) != 0 {
		t.Fatalf("group with low sum must stay hidden, got %+v", e)
	}
	if e := mustApply(t, v, []Mutation{
		{Op: Insert, RowID: "h1", Group: "h", Value: 100},
	}); len(e) != 0 {
		t.Fatalf("group with low count must stay hidden, got %+v", e)
	}
	if len(v.Snapshot()) != 0 {
		t.Fatalf("snapshot must be empty")
	}
}

func TestRejectionsAreAtomicAndDistinguishable(t *testing.T) {
	cases := []struct {
		name   string
		seed   []Mutation
		bad    []Mutation
		reason RejectReason
	}{
		{
			name:   "empty group name",
			bad:    []Mutation{{Op: Insert, RowID: "r1", Group: "  ", Value: 1}},
			reason: ReasonEmptyGroup,
		},
		{
			name:   "delete missing row",
			bad:    []Mutation{{Op: Delete, RowID: "ghost"}},
			reason: ReasonDeleteMissing,
		},
		{
			name:   "duplicate row id",
			seed:   []Mutation{{Op: Insert, RowID: "dup", Group: "g", Value: 1}},
			bad:    []Mutation{{Op: Insert, RowID: "dup", Group: "h", Value: 1}},
			reason: ReasonDuplicateRow,
		},
		{
			name: "too many groups mid batch",
			bad: []Mutation{
				{Op: Insert, RowID: "r1", Group: "g1", Value: 1},
				{Op: Insert, RowID: "r2", Group: "g2", Value: 1},
				{Op: Insert, RowID: "r3", Group: "g3", Value: 1},
			},
			reason: ReasonTooManyGroups,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var log bytes.Buffer
			v := New(Config{MinCount: 1, MinSum: 1, MaxGroups: 2}, WithLogWriter(&log))
			if tc.seed != nil {
				mustApply(t, v, tc.seed)
			}
			snapBefore := v.Snapshot()
			logBefore := log.String()

			entries, err := v.Apply(tc.bad)
			wantReason(t, err, tc.reason)
			if entries != nil {
				t.Fatalf("rejected batch must return no entries, got %+v", entries)
			}

			snapAfter := v.Snapshot()
			if fmt.Sprint(snapAfter) != fmt.Sprint(snapBefore) {
				t.Fatalf("snapshot changed after rejection: before=%+v after=%+v",
					snapBefore, snapAfter)
			}
			if !strings.HasPrefix(log.String(), logBefore) {
				t.Fatalf("rejection rewrote prior log")
			}
			extra := strings.TrimPrefix(log.String(), logBefore)
			if !strings.Contains(extra, "REJECTED") ||
				!strings.Contains(extra, "reason="+tc.reason.String()) {
				t.Fatalf("rejection log missing reason token:\n%s", extra)
			}
			if strings.Contains(extra, "COMMITTED") || strings.Contains(extra, "OUTPUT") {
				t.Fatalf("rejected batch must not produce output/commit log:\n%s", extra)
			}

			good := mustApply(t, v, []Mutation{{Op: Insert, RowID: "ok", Group: "g", Value: 1}})
			if len(good) != 1 {
				t.Fatalf("view should recover after rejected batch, got %+v", good)
			}
		})
	}
}

func TestRejectedBatchAppliesNothingWithinBatch(t *testing.T) {
	v := New(Config{MinCount: 1, MinSum: 1, MaxGroups: 10})
	_, err := v.Apply([]Mutation{
		{Op: Insert, RowID: "r1", Group: "g", Value: 5},
		{Op: Delete, RowID: "missing"},
	})
	wantReason(t, err, ReasonDeleteMissing)
	if len(v.Snapshot()) != 0 {
		t.Fatalf("valid prefix of invalid batch must not be applied")
	}
	if _, ok := v.rows["r1"]; ok {
		t.Fatalf("row from rejected batch must not exist")
	}
}

func TestDeterminism(t *testing.T) {
	script := [][]Mutation{
		{{Op: Insert, RowID: "r1", Group: "g", Value: 4}},
		{
			{Op: Insert, RowID: "r2", Group: "g", Value: 6},
			{Op: Insert, RowID: "r3", Group: "h", Value: 20},
		},
		{{Op: Delete, RowID: "r2"}},
		{
			{Op: Insert, RowID: "r4", Group: "g", Value: 10},
			{Op: Delete, RowID: "r1"},
		},
		{{Op: Delete, RowID: "r4"}},
		{{Op: Insert, RowID: "x", Group: "", Value: 1}},
	}

	run := func() (string, map[string]Aggregate) {
		v := New(Config{MinCount: 2, MinSum: 10, MaxGroups: 5})
		var out strings.Builder
		for i, batch := range script {
			entries, err := v.Apply(batch)
			if err != nil {
				fmt.Fprintf(&out, "batch %d: REJECTED %s\n", i, err.(*BatchError).Reason)
				continue
			}
			for _, e := range entries {
				fmt.Fprintf(&out, "batch %d: %s\n", i, entryText(e))
			}
		}
		return out.String(), v.Snapshot()
	}

	out1, snap1 := run()
	out2, snap2 := run()
	if out1 != out2 {
		t.Fatalf("non-deterministic output:\n%s\n---\n%s", out1, out2)
	}
	if fmt.Sprint(snap1) != fmt.Sprint(snap2) {
		t.Fatalf("non-deterministic snapshot: %+v vs %+v", snap1, snap2)
	}
}

func TestConcurrentReadsAlwaysSatisfyFilter(t *testing.T) {
	v := New(Config{MinCount: 1, MinSum: 0, MaxGroups: 50})
	var wg sync.WaitGroup

	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				id := fmt.Sprintf("w%d-r%d", w, i)
				group := fmt.Sprintf("g%d", i%8)
				if _, err := v.Apply([]Mutation{{Op: Insert, RowID: id, Group: group, Value: 1}}); err != nil {
					t.Errorf("unexpected insert error: %v", err)
					return
				}
				if _, err := v.Apply([]Mutation{{Op: Delete, RowID: id}}); err != nil {
					t.Errorf("unexpected delete error: %v", err)
					return
				}
			}
		}(w)
	}

	for r := 0; r < 8; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				snap := v.Snapshot()
				for g, a := range snap {
					if a.Count < v.cfg.MinCount || a.Sum < v.cfg.MinSum {
						t.Errorf("inconsistent snapshot group %q=%+v violates filter", g, a)
						return
					}
				}
			}
		}()
	}

	wg.Wait()
	if len(v.Snapshot()) != 0 {
		t.Fatalf("all inserted rows were deleted; view must be empty")
	}
}

func TestLogShowsInputsOutputsAndBasis(t *testing.T) {
	var log bytes.Buffer
	v := New(Config{MinCount: 1, MinSum: 1, MaxGroups: 10}, WithLogWriter(&log))

	mustApply(t, v, []Mutation{
		{Op: Insert, RowID: "r1", Group: "g", Value: 5},
	})

	text := log.String()
	for _, want := range []string{
		"INPUT-BEGIN",
		`INSERT row="r1" group="g" value=5`,
		"DECISIONS",
		"rule=(count>=1 && sum>=1)",
		"decision=ENTER",
		"inView=false",
		"inView=true",
		"OUTPUT-BEGIN",
		`UPSERT group="g" count=1 sum=5`,
		"COMMITTED",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("log missing %q in:\n%s", want, text)
		}
	}

	log.Reset()
	mustApply(t, v, []Mutation{{Op: Delete, RowID: "r1"}})
	text = log.String()
	if !strings.Contains(text, "decision=LEAVE") ||
		!strings.Contains(text, `RETRACT group="g" count=1 sum=5`) {
		t.Fatalf("leave log mismatch:\n%s", text)
	}
}
