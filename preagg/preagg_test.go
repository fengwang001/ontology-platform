package preagg

import (
	"errors"
	"reflect"
	"sync"
	"testing"
)

// snapshot captures every externally visible piece of state so tests can
// assert that rejected input leaves no trace.
type snapshot struct {
	View    map[string]int64
	Pending map[string]BufferState
	Hot     []string
	Log     []PushRecord
}

func takeSnapshot(a *Aggregator) snapshot {
	return snapshot{
		View:    a.View(),
		Pending: a.Pending(),
		Hot:     a.HotKeys(),
		Log:     a.PushLog(),
	}
}

func mustNew(t *testing.T, threshold int) *Aggregator {
	t.Helper()
	a, err := New(threshold)
	if err != nil {
		t.Fatalf("New(%d) returned unexpected error: %v", threshold, err)
	}
	return a
}

func mustAddBatch(t *testing.T, a *Aggregator, events ...Event) {
	t.Helper()
	t.Logf("input batch: %+v", events)
	if err := a.AddBatch(events); err != nil {
		t.Fatalf("AddBatch(%+v) returned unexpected error: %v", events, err)
	}
}

func groupSum(batches [][]Event) map[string]int64 {
	out := make(map[string]int64)
	for _, batch := range batches {
		for _, ev := range batch {
			out[ev.Key] += ev.Delta
		}
	}
	return out
}

func replay(log []PushRecord) map[string]int64 {
	out := make(map[string]int64)
	for _, rec := range log {
		out[rec.Key] += rec.Delta
	}
	return out
}

func TestThresholdTrigger(t *testing.T) {
	a := mustNew(t, 3)
	t.Log("threshold=3: key becomes hot and pushes once its buffered count reaches 3")

	mustAddBatch(t, a, Event{Key: "a", Delta: 1})
	mustAddBatch(t, a, Event{Key: "a", Delta: 2})
	if got := a.PushLog(); len(got) != 0 {
		t.Fatalf("after 2 events: push log = %+v, want empty (count 2 < threshold 3)", got)
	}
	t.Log("after 2 events: no push, buffered count 2 < threshold 3 (as expected)")

	mustAddBatch(t, a, Event{Key: "a", Delta: 4})
	log := a.PushLog()
	wantLog := []PushRecord{{Seq: 0, Key: "a", Delta: 7}}
	if !reflect.DeepEqual(log, wantLog) {
		t.Fatalf("after 3rd event: push log = %+v, want %+v (1+2+4 pushed at threshold)", log, wantLog)
	}
	t.Logf("after 3rd event: pushed %+v, buffer cleared, key marked hot", log)

	if got := a.Pending(); len(got) != 0 {
		t.Fatalf("pending = %+v, want empty after threshold push", got)
	}
	if hot := a.HotKeys(); !reflect.DeepEqual(hot, []string{"a"}) {
		t.Fatalf("hot keys = %v, want [a]", hot)
	}

	mustAddBatch(t, a, Event{Key: "a", Delta: 10})
	log = a.PushLog()
	wantLog = append(wantLog, PushRecord{Seq: 1, Key: "a", Delta: 10})
	if !reflect.DeepEqual(log, wantLog) {
		t.Fatalf("hot key event: push log = %+v, want %+v (hot keys push each event individually)", log, wantLog)
	}
	t.Logf("hot key pushes every event individually: %+v", log)

	if err := a.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

func TestNewRejectsNonPositiveThreshold(t *testing.T) {
	for _, threshold := range []int{0, -1, -100} {
		_, err := New(threshold)
		if !errors.Is(err, ErrNonPositiveThreshold) {
			t.Fatalf("New(%d) error = %v, want ErrNonPositiveThreshold", threshold, err)
		}
		t.Logf("New(%d) rejected: %v", threshold, err)
	}
}

func TestInvalidInputsRejectedWithoutTrace(t *testing.T) {
	a := mustNew(t, 3)
	mustAddBatch(t, a, Event{Key: "k", Delta: 2})
	mustAddBatch(t, a, Event{Key: "k", Delta: 3})
	before := takeSnapshot(a)
	t.Logf("state before invalid input: %+v", before)

	cases := []struct {
		name   string
		events []Event
		want   error
	}{
		{"empty batch", nil, ErrEmptyBatch},
		{"empty key", []Event{{Key: "", Delta: 1}}, ErrEmptyKey},
		{"zero delta", []Event{{Key: "k", Delta: 0}}, ErrZeroDelta},
		{"bad event among good ones", []Event{{Key: "good", Delta: 1}, {Key: "bad", Delta: 0}}, ErrZeroDelta},
	}
	for _, tc := range cases {
		err := a.AddBatch(tc.events)
		if !errors.Is(err, tc.want) {
			t.Fatalf("%s: AddBatch(%+v) error = %v, want %v", tc.name, tc.events, err, tc.want)
		}
		after := takeSnapshot(a)
		if !reflect.DeepEqual(after, before) {
			t.Fatalf("%s: state changed after rejection\nbefore: %+v\nafter:  %+v", tc.name, before, after)
		}
		t.Logf("%s: rejected with %q; state unchanged (view/pending/hot/log identical)", tc.name, err)
	}

	// Distinct, decidable reasons: the four sentinels must not match each other.
	sentinels := []error{ErrNonPositiveThreshold, ErrEmptyBatch, ErrEmptyKey, ErrZeroDelta}
	for i, x := range sentinels {
		for j, y := range sentinels {
			if i != j && errors.Is(x, y) {
				t.Fatalf("rejection reasons %q and %q are not distinguishable", x, y)
			}
		}
	}

	mustAddBatch(t, a, Event{Key: "k", Delta: 1})
	if got := a.View()["k"]; got != 6 {
		t.Fatalf("view[k] = %d, want 6 (aggregator still usable: 2+3 buffered, +1 hit threshold)", got)
	}
	t.Log("aggregator remains fully usable after rejections: k pushed 2+3+1=6 at threshold")
}

func TestInvariantViewPlusPendingAndReplay(t *testing.T) {
	a := mustNew(t, 3)
	batches := [][]Event{
		{{Key: "a", Delta: 1}, {Key: "b", Delta: 10}, {Key: "a", Delta: 2}},
		{{Key: "a", Delta: 3}},
		{{Key: "b", Delta: -4}, {Key: "c", Delta: 7}},
		{{Key: "c", Delta: 1}, {Key: "c", Delta: 2}},
		{{Key: "a", Delta: 5}},
	}
	fed := make(map[string]int64)
	for i, batch := range batches {
		mustAddBatch(t, a, batch...)
		for _, ev := range batch {
			fed[ev.Key] += ev.Delta
		}
		view, pending := a.View(), a.Pending()
		for key, total := range fed {
			if got := view[key] + pending[key].Sum; got != total {
				t.Fatalf("after batch %d: view[%q]+pending[%q] = %d, want fed sum %d",
					i, key, key, got, total)
			}
		}
		if got := replay(a.PushLog()); !reflect.DeepEqual(got, view) {
			t.Fatalf("after batch %d: replayed push log = %+v, want view %+v", i, got, view)
		}
		if err := a.SelfCheck(); err != nil {
			t.Fatalf("after batch %d: SelfCheck: %v", i, err)
		}
		t.Logf("after batch %d: view+pending == fed sums, push log replays to view %+v", i, view)
	}

	a.FlushAll()
	want := groupSum(batches)
	if got := a.View(); !reflect.DeepEqual(got, want) {
		t.Fatalf("after FlushAll: view = %+v, want group-sum %+v", got, want)
	}
	t.Logf("after FlushAll: view == batch group-sum %+v (reproducible)", want)
}

func TestConcurrentReadConsistency(t *testing.T) {
	a := mustNew(t, 2)
	batches := [][]Event{
		{{Key: "a", Delta: 1}, {Key: "b", Delta: 2}, {Key: "a", Delta: 3}},
		{{Key: "c", Delta: 5}, {Key: "b", Delta: 7}},
		{{Key: "a", Delta: 11}},
	}
	for _, batch := range batches {
		mustAddBatch(t, a, batch...)
	}
	want := takeSnapshot(a)
	t.Logf("fed instance quiescent; expected snapshot: %+v", want)

	const readers = 8
	const rounds = 200
	var wg sync.WaitGroup
	errs := make(chan error, readers)
	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				got := takeSnapshot(a)
				if !reflect.DeepEqual(got, want) {
					errs <- &snapshotMismatch{reader: id, round: i, got: got}
					return
				}
				if err := a.SelfCheck(); err != nil {
					errs <- err
					return
				}
			}
		}(r)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent read inconsistency: %v", err)
	}
	t.Logf("%d readers x %d rounds: every snapshot field-for-field identical", readers, rounds)
}

type snapshotMismatch struct {
	reader int
	round  int
	got    snapshot
}

func (e *snapshotMismatch) Error() string {
	return "reader mismatch"
}

func TestHotKeyFromBatchDuplicates(t *testing.T) {
	a := mustNew(t, 10)
	t.Log("threshold=10: key appearing twice in one batch becomes hot after that batch ends")

	mustAddBatch(t, a,
		Event{Key: "b", Delta: 1},
		Event{Key: "c", Delta: 5},
		Event{Key: "b", Delta: 2},
	)
	if hot := a.HotKeys(); !reflect.DeepEqual(hot, []string{"b"}) {
		t.Fatalf("hot keys = %v, want [b] (b occurred twice in the batch)", hot)
	}
	t.Log("after batch: b is hot (2 occurrences), c is not (1 occurrence)")

	wantPending := map[string]BufferState{"b": {Sum: 3, Count: 2}, "c": {Sum: 5, Count: 1}}
	if got := a.Pending(); !reflect.DeepEqual(got, wantPending) {
		t.Fatalf("pending = %+v, want %+v (no pushes, count 2 < threshold 10)", got, wantPending)
	}
	t.Logf("pending kept locally: %+v (global view untouched)", wantPending)

	mustAddBatch(t, a, Event{Key: "b", Delta: 7})
	wantLog := []PushRecord{{Seq: 0, Key: "b", Delta: 7}}
	if got := a.PushLog(); !reflect.DeepEqual(got, wantLog) {
		t.Fatalf("push log = %+v, want %+v (hot key pushes immediately, old buffer stays)", got, wantLog)
	}
	t.Log("hot key b pushed its new event individually; pre-hot buffer still pending")

	if got := a.Pending()["b"]; !reflect.DeepEqual(got, BufferState{Sum: 3, Count: 2}) {
		t.Fatalf("pending[b] = %+v, want {Sum:3 Count:2} (buffered before hotness stays until flush)", got)
	}
	if err := a.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

func TestFlushKey(t *testing.T) {
	a := mustNew(t, 5)
	mustAddBatch(t, a, Event{Key: "x", Delta: 3}, Event{Key: "x", Delta: 4})
	mustAddBatch(t, a, Event{Key: "y", Delta: 9})
	t.Log("input: x buffered {7,2} and hot (duplicate in batch), y buffered {9,1}")

	rec, ok := a.FlushKey("x")
	if !ok || rec != (PushRecord{Seq: 0, Key: "x", Delta: 7}) {
		t.Fatalf("FlushKey(x) = %+v, %v; want {Seq:0 Key:x Delta:7}, true", rec, ok)
	}
	t.Logf("FlushKey(x) pushed %+v and removed x from the hot set", rec)

	if hot := a.HotKeys(); len(hot) != 0 {
		t.Fatalf("hot keys = %v, want empty after FlushKey(x)", hot)
	}
	if _, ok := a.Pending()["x"]; ok {
		t.Fatalf("pending still contains x after FlushKey")
	}
	if got := a.Pending()["y"]; got != (BufferState{Sum: 9, Count: 1}) {
		t.Fatalf("pending[y] = %+v, want {Sum:9 Count:1} (other keys untouched)", got)
	}

	if _, ok := a.FlushKey("x"); ok {
		t.Fatalf("second FlushKey(x) reported a push, want no-op")
	}
	t.Log("second FlushKey(x) is a no-op: nothing pending, not hot")

	if err := a.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

func TestFlushAll(t *testing.T) {
	a := mustNew(t, 4)
	mustAddBatch(t, a,
		Event{Key: "b", Delta: 2},
		Event{Key: "a", Delta: 1},
		Event{Key: "c", Delta: 3},
		Event{Key: "b", Delta: 5},
	)
	t.Log("input: pending a=1, b=7, c=3; b hot (duplicate in batch)")

	pushed := a.FlushAll()
	want := []PushRecord{
		{Seq: 0, Key: "a", Delta: 1},
		{Seq: 1, Key: "b", Delta: 7},
		{Seq: 2, Key: "c", Delta: 3},
	}
	if !reflect.DeepEqual(pushed, want) {
		t.Fatalf("FlushAll pushed %+v, want %+v (lexicographic key order)", pushed, want)
	}
	t.Logf("FlushAll pushed in lexicographic order: %+v", pushed)

	if got := a.Pending(); len(got) != 0 {
		t.Fatalf("pending = %+v, want empty after FlushAll", got)
	}
	if hot := a.HotKeys(); len(hot) != 0 {
		t.Fatalf("hot keys = %v, want empty after FlushAll", hot)
	}

	wantView := map[string]int64{"a": 1, "b": 7, "c": 3}
	if got := a.View(); !reflect.DeepEqual(got, wantView) {
		t.Fatalf("view = %+v, want %+v (equals batch group-sum after full flush)", got, wantView)
	}
	t.Logf("view after full flush equals group-sum: %+v", wantView)

	if err := a.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}
