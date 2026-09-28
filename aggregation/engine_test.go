package aggregation

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"sync"
	"testing"
)

// recompute derives the expected metrics by replaying the whole serial of
// ops from scratch, serving as the batch-recompute reference.
func recompute(serial []RowOp) map[string]Metrics {
	type acc struct {
		sum    float64
		count  int64
		values map[float64]int64
	}
	states := map[string]*acc{}
	for _, row := range serial {
		st := states[row.Group]
		if st == nil {
			st = &acc{values: map[float64]int64{}}
			states[row.Group] = st
		}
		if row.Op == OpAdd {
			st.sum += row.Value
			st.count++
			st.values[row.Value]++
		} else {
			st.sum -= row.Value
			st.count--
			st.values[row.Value]--
			if st.values[row.Value] == 0 {
				delete(st.values, row.Value)
			}
		}
		if st.count == 0 {
			delete(states, row.Group)
		}
	}
	out := map[string]Metrics{}
	for g, st := range states {
		m := Metrics{Sum: st.sum, Count: st.count, DistinctVals: int64(len(st.values))}
		if st.count > 0 {
			m.Avg = st.sum / float64(st.count)
		}
		out[g] = m
	}
	return out
}

func assertSnapshot(t *testing.T, got, want map[string]Metrics) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("group count mismatch: got %v, want %v", got, want)
	}
	for g, wm := range want {
		gm, ok := got[g]
		if !ok {
			t.Fatalf("group %q missing in %v", g, got)
		}
		if gm.Count != wm.Count || gm.DistinctVals != wm.DistinctVals ||
			math.Abs(gm.Sum-wm.Sum) > 1e-9 || math.Abs(gm.Avg-wm.Avg) > 1e-9 {
			t.Fatalf("group %q: got %+v, want %+v", g, gm, wm)
		}
	}
}

// submitBatches runs the two-phase pipeline over already-cut batches,
// logging every batch input, its partial aggregates, and the decision.
func submitBatches(t *testing.T, e *Engine, batches [][]RowOp) {
	t.Helper()
	for i, batch := range batches {
		partials, err := AggregateBatch(batch)
		if err != nil {
			t.Fatalf("batch %d local phase rejected: %v", i, err)
		}
		keys := make([]string, 0, len(partials))
		for g := range partials {
			keys = append(keys, g)
		}
		sort.Strings(keys)
		t.Logf("batch %d input=%v", i, batch)
		for _, g := range keys {
			t.Logf("batch %d partial[%s]=%+v", i, g, partials[g])
		}
		if err := e.Submit(partials); err != nil {
			t.Fatalf("batch %d global merge rejected: %v", i, err)
		}
		t.Logf("batch %d merged: %d non-zero partial(s), decision=accept", i, len(partials))
	}
}

// splitCuts partitions serial into batches at the given cut points.
func splitCuts(serial []RowOp, cuts []int) [][]RowOp {
	var batches [][]RowOp
	prev := 0
	for _, c := range cuts {
		batches = append(batches, serial[prev:c])
		prev = c
	}
	return append(batches, serial[prev:])
}

func testSerial() []RowOp {
	return []RowOp{
		{OpAdd, "a", 1}, {OpAdd, "a", 2}, {OpAdd, "b", 5},
		{OpAdd, "a", 2}, {OpAdd, "b", 5}, {OpAdd, "c", 9},
		{OpRetract, "a", 1}, {OpAdd, "b", 7}, {OpRetract, "b", 5},
		{OpAdd, "c", 1}, {OpRetract, "c", 9}, {OpRetract, "c", 1}, // c 归零删除
		{OpAdd, "a", 4}, {OpRetract, "a", 2}, {OpAdd, "c", 3}, // c 重建
	}
}

func TestFourMetricsMergeAcrossBatches(t *testing.T) {
	serial := testSerial()
	e := NewEngine(16)
	submitBatches(t, e, splitCuts(serial, []int{4, 9, 12}))
	assertSnapshot(t, e.Snapshot(), recompute(serial))
}

func TestBatchingInvariance(t *testing.T) {
	serial := testSerial()
	want := recompute(serial)
	cutSets := [][]int{
		{}, // 单批
		{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}, // 每行一批
		{5, 10},           // 均匀三段
		{3, 6, 7, 11, 14}, // 不规则
	}
	for _, cuts := range cutSets {
		e := NewEngine(16)
		submitBatches(t, e, splitCuts(serial, cuts))
		assertSnapshot(t, e.Snapshot(), want)
		t.Logf("cuts=%v decision=accept: snapshot matches batch recompute", cuts)
	}
}

func TestAllZeroGroupsNotSent(t *testing.T) {
	batch := []RowOp{
		{OpAdd, "z", 3}, {OpRetract, "z", 3}, // 净零，不得发送
		{OpAdd, "k", 1},
	}
	partials, err := AggregateBatch(batch)
	if err != nil {
		t.Fatalf("unexpected reject: %v", err)
	}
	t.Logf("input=%v partials=%v", batch, partials)
	if _, ok := partials["z"]; ok {
		t.Fatalf("all-zero group z must be filtered, got %v", partials["z"])
	}
	if len(partials) != 1 {
		t.Fatalf("expected only group k, got %v", partials)
	}

	e := NewEngine(16)
	if err := e.Submit(partials); err != nil {
		t.Fatalf("unexpected reject: %v", err)
	}
	if _, ok := e.Snapshot()["z"]; ok {
		t.Fatal("filtered group z must not appear in global state")
	}
}

func TestGroupDeletionAndRecreation(t *testing.T) {
	e := NewEngine(16)
	submitBatches(t, e, [][]RowOp{
		{{OpAdd, "g", 2}, {OpAdd, "g", 4}},
		{{OpRetract, "g", 2}, {OpRetract, "g", 4}}, // 计数归零 -> 删除
	})
	if got := e.Snapshot(); len(got) != 0 {
		t.Fatalf("group g must be deleted after count hits zero, got %v", got)
	}
	t.Log("group g deleted: merged count reached zero")

	submitBatches(t, e, [][]RowOp{{{OpAdd, "g", 10}}}) // 重建
	got := e.Snapshot()
	m, ok := got["g"]
	if !ok {
		t.Fatal("group g must be recreatable after deletion")
	}
	if m.Count != 1 || m.Sum != 10 || m.Avg != 10 || m.DistinctVals != 1 {
		t.Fatalf("recreated group g: got %+v", m)
	}
	t.Logf("group g recreated: %+v", m)
}

func TestRejectionsAreAtomicAndDistinguishable(t *testing.T) {
	e := NewEngine(2)
	submitBatches(t, e, [][]RowOp{
		{{OpAdd, "a", 1}, {OpAdd, "b", 2}, {OpAdd, "b", 2}},
	})
	before := e.Snapshot()
	beforeAccepted := e.AcceptedBatches()

	cases := []struct {
		name string
		run  func() error
		code ErrCode
	}{
		{"invalid op", func() error {
			_, err := AggregateBatch([]RowOp{{Op(7), "x", 1}})
			return err
		}, ErrCodeInvalidOp},
		{"empty group local", func() error {
			_, err := AggregateBatch([]RowOp{{OpAdd, "", 1}})
			return err
		}, ErrCodeEmptyGroup},
		{"empty group global", func() error {
			return e.Submit(map[string]PartialAgg{"": {Sum: 1, Count: 1}})
		}, ErrCodeEmptyGroup},
		{"retract missing row", func() error {
			return e.Submit(map[string]PartialAgg{
				"a": {Sum: -9, Count: -1, Values: map[float64]int64{9: -1}},
			})
		}, ErrCodeRetractMissingRow},
		{"retract absent group", func() error {
			return e.Submit(map[string]PartialAgg{
				"ghost": {Sum: -1, Count: -1, Values: map[float64]int64{1: -1}},
			})
		}, ErrCodeRetractMissingRow},
		{"too many groups", func() error {
			return e.Submit(map[string]PartialAgg{
				"c": {Sum: 1, Count: 1, Values: map[float64]int64{1: 1}},
			})
		}, ErrCodeTooManyGroups},
	}

	codes := map[ErrCode]bool{}
	for _, c := range []ErrCode{ErrCodeInvalidOp, ErrCodeEmptyGroup, ErrCodeRetractMissingRow, ErrCodeTooManyGroups} {
		if codes[c] {
			t.Fatalf("error codes are not distinguishable: %d duplicated", c)
		}
		codes[c] = true
	}
	for _, tc := range cases {
		err := tc.run()
		var aerr *Error
		if !errors.As(err, &aerr) {
			t.Fatalf("%s: expected *Error, got %v", tc.name, err)
		}
		if aerr.Code != tc.code {
			t.Fatalf("%s: got code %d, want %d", tc.name, aerr.Code, tc.code)
		}
		t.Logf("reject %-20s code=%d reason=%q", tc.name, aerr.Code, aerr.Msg)

		assertSnapshot(t, e.Snapshot(), before)
		if got := e.AcceptedBatches(); got != beforeAccepted {
			t.Fatalf("%s: accepted count changed after rejection: %d -> %d",
				tc.name, beforeAccepted, got)
		}
	}
	t.Log("all rejections left global state and accepted count untouched")
}

func TestConcurrentSubmitAndQuery(t *testing.T) {
	const workers = 8
	e := NewEngine(64)
	var wg sync.WaitGroup
	var serials [workers][]RowOp
	for w := 0; w < workers; w++ {
		for i := 0; i < 50; i++ {
			g := fmt.Sprintf("g%d", (w+i)%10)
			serials[w] = append(serials[w], RowOp{OpAdd, g, float64(i%5 + 1)})
		}
	}
	var all []RowOp
	for w := 0; w < workers; w++ {
		all = append(all, serials[w]...)
	}

	wg.Add(workers * 2)
	for w := 0; w < workers; w++ {
		go func(serial []RowOp) {
			defer wg.Done()
			for _, batch := range splitCuts(serial, []int{10, 25, 40}) {
				partials, err := AggregateBatch(batch)
				if err != nil {
					t.Errorf("local reject: %v", err)
					return
				}
				if err := e.Submit(partials); err != nil {
					t.Errorf("global reject: %v", err)
					return
				}
			}
		}(serials[w])
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				_ = e.Snapshot()
				_ = e.AcceptedBatches()
			}
		}()
	}
	wg.Wait()

	assertSnapshot(t, e.Snapshot(), recompute(all))
	if got := e.AcceptedBatches(); got != workers*4 {
		t.Fatalf("accepted batches: got %d, want %d", got, workers*4)
	}
	t.Logf("concurrent merge of %d rows matches batch recompute", len(all))
}
