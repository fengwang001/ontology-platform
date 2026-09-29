package aggview

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

type testLogger struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *testLogger) Printf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintf(&l.buf, format+"\n", args...)
}

func (l *testLogger) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

func ins(id, group string, value int64) Mutation {
	return Mutation{Op: OpInsert, Row: Row{ID: id, Group: group, Value: value}}
}

func del(id string) Mutation {
	return Mutation{Op: OpDelete, ID: id}
}

func mustApply(t *testing.T, v *View, batch []Mutation) {
	t.Helper()
	if err := v.Apply(batch); err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
}

func mustApplyReplay(t *testing.T, v *View, mv *MaterializedView, batch []Mutation) {
	t.Helper()
	prev := len(v.Log())
	mustApply(t, v, batch)
	for _, e := range v.Log()[prev:] {
		mv.Apply(e)
	}
}

func assertDownstreamInSync(t *testing.T, v *View, mv *MaterializedView) {
	t.Helper()
	src, dst := v.Snapshot(), mv.Snapshot()
	if len(src) != len(dst) {
		t.Fatalf("downstream out of sync: src=%v dst=%v", src, dst)
	}
	for i := range src {
		if src[i] != dst[i] {
			t.Fatalf("downstream out of sync: src=%v dst=%v", src, dst)
		}
	}
}

// TestThresholdsInclusive: count == MinCount and sum == MinSum exactly are
// visible; dropping one below the threshold produces a leave/retract.
func TestThresholdsInclusive(t *testing.T) {
	log := &testLogger{}
	v := New(Config{MinCount: 2, MinSum: 10, Logger: log})

	if err := v.Apply([]Mutation{ins("r1", "g", 5)}); err != nil {
		t.Fatal(err)
	}
	if len(v.Snapshot()) != 0 {
		t.Fatalf("group with count=1 sum=5 must be filtered out")
	}

	if err := v.Apply([]Mutation{ins("r2", "g", 5)}); err != nil {
		t.Fatal(err)
	}
	snap := v.Snapshot()
	if len(snap) != 1 || snap[0] != (Agg{Group: "g", Count: 2, Sum: 10}) {
		t.Fatalf("expected g{2,10} visible at exact thresholds, got %v", snap)
	}
	entries := v.Log()
	if len(entries) != 1 || entries[0].Kind != KindEnter ||
		entries[0].Op != EntryUpsert {
		t.Fatalf("expected single enter/upsert entry, got %#v", entries)
	}

	// sum falls below threshold while count stays at it => leaves.
	if err := v.Apply([]Mutation{ins("r2", "g", 4)}); err != nil {
		t.Fatal(err)
	}
	if len(v.Snapshot()) != 0 {
		t.Fatalf("count=2 sum=9 must be filtered out, got %v", v.Snapshot())
	}
	last := v.Log()[len(v.Log())-1]
	if last.Kind != KindLeave || last.Op != EntryRetract {
		t.Fatalf("expected leave/retract, got %#v", last)
	}

	if !strings.Contains(log.String(), "input op=insert") ||
		!strings.Contains(log.String(), "decision group=\"g\"") ||
		!strings.Contains(log.String(), "output seq=") ||
		!strings.Contains(log.String(), "filter={count>=2 sum>=10}") {
		t.Fatalf("log must show inputs, outputs and rationale:\n%s", log.String())
	}
	t.Logf("captured log:\n%s", log.String())
}

// TestCountZeroGroupDisappears: deleting the last row removes the group and
// emits a retract if the group was visible.
func TestCountZeroGroupDisappears(t *testing.T) {
	v := New(Config{MinCount: 1, MinSum: 0})
	mustApply(t, v, []Mutation{ins("r1", "g", 3)})
	mustApply(t, v, []Mutation{del("r1")})

	if len(v.Snapshot()) != 0 {
		t.Fatalf("group must disappear when count hits zero, got %v", v.Snapshot())
	}
	entries := v.Log()
	if len(entries) != 2 ||
		!(entries[0].Kind == KindEnter && entries[0].Op == EntryUpsert) ||
		!(entries[1].Kind == KindLeave && entries[1].Op != EntryUpsert) {
		t.Fatalf("expected enter then leave, got %#v", entries)
	}
	if entries[1].Op != EntryRetract {
		t.Fatalf("leave entry must be retract, got %v", entries[1].Op)
	}

	mustApply(t, v, []Mutation{ins("r9", "g", 7)})
	snap := v.Snapshot()
	if len(snap) != 1 || snap[0] != (Agg{Group: "g", Count: 1, Sum: 7}) {
		t.Fatalf("vanished group should be recreated fresh, got %v", snap)
	}
}

// TestChangeRetractThenUpsert: a visible group whose aggregate changes emits
// retract(old) followed by upsert(new).
func TestChangeRetractThenUpsert(t *testing.T) {
	v := New(Config{MinCount: 1, MinSum: 0})
	mv := NewMaterializedView()
	mustApplyReplay(t, v, mv, []Mutation{ins("r1", "g", 3)})
	mustApplyReplay(t, v, mv, []Mutation{ins("r2", "g", 4)})

	entries := v.Log()
	if len(entries) != 3 {
		t.Fatalf("expected enter + change pair (3 entries), got %d", len(entries))
	}
	if entries[1].Kind != KindChange || entries[1].Op != EntryRetract ||
		entries[2].Op != EntryUpsert {
		t.Fatalf("expected change retract then upsert, got %#v %#v",
			entries[1], entries[2])
	}
	if *entries[1].Old != (Agg{Group: "g", Count: 1, Sum: 3}) ||
		*entries[2].New != (Agg{Group: "g", Count: 2, Sum: 7}) {
		t.Fatalf("old/new aggregates mismatch, got %#v %#v",
			entries[1].Old, entries[2].New)
	}
	assertDownstreamInSync(t, v, mv)
}

// TestInvisibleBeforeAndAfterEmitsNothing: hidden-before/hidden-after changes
// produce no net-change output.
func TestInvisibleBeforeAndAfterEmitsNothing(t *testing.T) {
	v := New(Config{MinCount: 2, MinSum: 0})
	mustApply(t, v, []Mutation{ins("r1", "g", 3)})
	before := len(v.Log())
	// A separate group that never reaches the threshold: hidden on both sides
	// of each mutation, so nothing is emitted.
	mustApply(t, v, []Mutation{ins("r2", "h", 1), del("r2")})
	if len(v.Log()) != before {
		t.Fatalf("hidden-before/hidden-after changes must emit nothing")
	}
	if len(v.Snapshot()) != 0 {
		t.Fatalf("expected empty view, got %v", v.Snapshot())
	}
}

// TestInvalidInputs covers each distinguishable rejection reason and asserts
// aggregates, view and log are untouched.
func TestInvalidInputs(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
		seed []Mutation
		bad  []Mutation
		want error
	}{
		{
			name: "empty group on insert",
			cfg:  Config{MinCount: 1, MinSum: 0},
			bad:  []Mutation{ins("r1", "", 1)},
			want: ErrEmptyGroup,
		},
		{
			name: "delete missing row",
			cfg:  Config{MinCount: 1, MinSum: 0},
			bad:  []Mutation{del("ghost")},
			want: ErrRowNotFound,
		},
		{
			name: "delete row already deleted earlier in batch",
			cfg:  Config{MinCount: 1, MinSum: 0},
			seed: []Mutation{ins("r1", "g", 1)},
			bad:  []Mutation{del("r1"), del("r1")},
			want: ErrRowNotFound,
		},
		{
			name: "group limit on new group",
			cfg:  Config{MinCount: 1, MinSum: 0, MaxGroups: 1},
			seed: []Mutation{ins("r1", "g1", 1)},
			bad:  []Mutation{ins("r2", "g2", 1)},
			want: ErrGroupLimit,
		},
		{
			name: "group limit transient delete then create still rejected",
			cfg:  Config{MinCount: 1, MinSum: 0, MaxGroups: 1},
			seed: []Mutation{ins("r1", "g1", 1)},
			bad:  []Mutation{ins("r2", "g2", 1), del("r2")},
			want: ErrGroupLimit,
		},
		{
			name: "unknown op",
			cfg:  Config{MinCount: 1, MinSum: 0},
			bad:  []Mutation{{Op: OpKind(99)}},
			want: ErrUnknownOp,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			log := &testLogger{}
			tc.cfg.Logger = log
			v := New(tc.cfg)
			if tc.seed != nil {
				mustApply(t, v, tc.seed)
			}
			stateBefore := fmt.Sprintf("%v", v.Snapshot())
			logBefore := len(v.Log())

			err := v.Apply(tc.bad)
			if !errors.Is(err, tc.want) {
				t.Fatalf("want error wrapping %v, got %v", tc.want, err)
			}
			if got := fmt.Sprintf("%v", v.Snapshot()); got != stateBefore {
				t.Fatalf("rejected batch changed view: %s -> %s",
					stateBefore, got)
			}
			if len(v.Log()) != logBefore {
				t.Fatalf("rejected batch appended net-change entries")
			}
			if !strings.Contains(log.String(), "reject batch") ||
				!strings.Contains(log.String(), "reason=") {
				t.Fatalf("rejection not logged with reason:\n%s", log.String())
			}
		})
	}
}

// TestRejectedBatchAtomicity: an error late in the batch rolls back earlier
// mutations in the same batch.
func TestRejectedBatchAtomicity(t *testing.T) {
	v := New(Config{MinCount: 1, MinSum: 0})
	mustApply(t, v, []Mutation{ins("r1", "g", 1)})

	err := v.Apply([]Mutation{
		ins("r2", "g", 2),
		ins("r3", "", 1),
	})
	if !errors.Is(err, ErrEmptyGroup) {
		t.Fatalf("want ErrEmptyGroup, got %v", err)
	}
	snap := v.Snapshot()
	if len(snap) != 1 || snap[0] != (Agg{Group: "g", Count: 1, Sum: 1}) {
		t.Fatalf("state must be unchanged after rejected batch, got %v", snap)
	}
	if len(v.Log()) != 1 {
		t.Fatalf("no entries may be appended for a rejected batch")
	}
}

// TestDownstreamMaterializesCorrectly replays every emitted log entry against a
// downstream view and checks equality after each step.
func TestDownstreamMaterializesCorrectly(t *testing.T) {
	v := New(Config{MinCount: 2, MinSum: 5})
	mv := NewMaterializedView()
	steps := [][]Mutation{
		{ins("a1", "a", 2), ins("a2", "a", 4)}, // a enters {2,6}
		{ins("b1", "b", 10)},                   // b hidden (count 1)
		{ins("b2", "b", 1)},                    // b enters {2,11}
		{ins("a3", "a", -3)},                   // a {3,3} sum<5 => leave
		{ins("a3", "a", 3)},                    // a {3,9} => enter
		{del("b1")},                            // b {1,1} => leave
		{del("a1"), del("a2")},                 // a {1,3} => leave
	}
	for i, step := range steps {
		mustApplyReplay(t, v, mv, step)
		t.Logf("after step %d snapshot=%v", i, v.Snapshot())
		assertDownstreamInSync(t, v, mv)
	}
	if len(v.Snapshot()) != 0 {
		t.Fatalf("final view must be empty, got %v", v.Snapshot())
	}
}

// TestDeterministicReplay runs the same input sequence twice and requires
// byte-identical logs and snapshots.
func TestDeterministicReplay(t *testing.T) {
	steps := [][]Mutation{
		{ins("a1", "a", 2), ins("a2", "a", 4)},
		{ins("b1", "b", 10)},
		{ins("b2", "b", 1)},
		{del("a1")},
		{ins("a1", "a", 6)},
		{del("b1"), del("b2")},
	}
	run := func() ([]LogEntry, []Agg) {
		v := New(Config{MinCount: 2, MinSum: 5})
		for _, s := range steps {
			mustApply(t, v, s)
		}
		return v.Log(), v.Snapshot()
	}
	log1, snap1 := run()
	log2, snap2 := run()
	if stableLog(log1) != stableLog(log2) {
		t.Fatalf("logs differ:\n%s\n%s", stableLog(log1), stableLog(log2))
	}
	if fmt.Sprintf("%v", snap1) != fmt.Sprintf("%v", snap2) {
		t.Fatalf("snapshots differ: %v vs %v", snap1, snap2)
	}
	for i := range log1 {
		if log1[i].Seq != int64(i+1) {
			t.Fatalf("log seq must be gapless, got %d at index %d",
				log1[i].Seq, i)
		}
	}
}

func stableLog(entries []LogEntry) string {
	var b strings.Builder
	for _, e := range entries {
		fmt.Fprintf(&b, "%d:%s:%s:%s old=%v new=%v\n",
			e.Seq, e.Kind, e.Op, e.Group, e.Old, e.New)
	}
	return b.String()
}

// TestConcurrentReadsSeeConsistentView: concurrent readers must only ever
// observe groups satisfying the filter while writers mutate the view.
func TestConcurrentReadsSeeConsistentView(t *testing.T) {
	const (
		writers = 4
		rounds  = 50
		readers = 4
	)
	v := New(Config{MinCount: 2, MinSum: 3})
	var id int64
	var violations atomic.Int64
	var stop atomic.Bool
	var writerWg, readerWg sync.WaitGroup

	readerWg.Add(readers)
	for r := 0; r < readers; r++ {
		go func() {
			defer readerWg.Done()
			for !stop.Load() {
				for _, a := range v.Snapshot() {
					if !a.Visible(2, 3) {
						violations.Add(1)
					}
				}
			}
		}()
	}

	writerWg.Add(writers)
	for w := 0; w < writers; w++ {
		go func(w int) {
			defer writerWg.Done()
			group := fmt.Sprintf("g%d", w)
			for r := 0; r < rounds; r++ {
				n := atomic.AddInt64(&id, 1)
				id1 := fmt.Sprintf("w%d-%d", w, n)
				id2 := fmt.Sprintf("w%d-%d-x", w, n)
				if err := v.Apply([]Mutation{
					ins(id1, group, 3),
					ins(id2, group, 4),
				}); err != nil {
					panic(err)
				}
				if err := v.Apply([]Mutation{del(id1), del(id2)}); err != nil {
					panic(err)
				}
			}
		}(w)
	}

	writerWg.Wait()
	stop.Store(true)
	readerWg.Wait()

	if violations.Load() != 0 {
		t.Fatalf("readers observed %d groups violating the filter",
			violations.Load())
	}
	if len(v.Snapshot()) != 0 {
		t.Fatalf("after all paired deletes view must be empty, got %v",
			v.Snapshot())
	}
}
