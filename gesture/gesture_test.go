package gesture

import (
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"testing"
)

type naiveRecognizer struct {
	d, l, w                int
	stableLevel, diffCount int
	pressed                bool
	pressIndex             int
	pending, second        bool
	pendingTr              int
	shortCount             int
	singleCount            int
	doubleCount            int
	discardedCount         int
}

func newNaive(d, l, w int) *naiveRecognizer {
	return &naiveRecognizer{d: d, l: l, w: w}
}

func (n *naiveRecognizer) sample(index, level int) ([]Event, []string) {
	var events []Event
	var reasons []string

	if n.pressed && index == n.pressIndex+n.l {
		events = append(events, Event{Kind: LongPress, Index: index})
		reasons = append(reasons, "LongPress at tp+L before transition")
		if n.second {
			n.pending = false
			n.second = false
			n.discardedCount++
			reasons = append(reasons, "second hit long press discards first short press")
		}
	}

	if n.pending && !n.second && index == n.pendingTr+n.w+1 {
		events = append(events, Event{Kind: SingleClick, Index: index})
		n.pending = false
		n.singleCount++
		reasons = append(reasons, "SingleClick at tr+W+1")
	}

	if level == n.stableLevel {
		n.diffCount = 0
		return events, reasons
	}

	n.diffCount++
	if n.diffCount < n.d {
		return events, reasons
	}

	n.stableLevel = level
	n.diffCount = 0

	if level == 1 {
		events = append(events, Event{Kind: Press, Index: index})
		n.pressed = true
		n.pressIndex = index
		if n.pending && index-n.pendingTr <= n.w {
			n.second = true
			reasons = append(reasons, fmt.Sprintf("Press at %d is second hit, gap %d <= W %d", index, index-n.pendingTr, n.w))
		} else {
			if n.pending {
				reasons = append(reasons, fmt.Sprintf("Press at %d starts a new hit, gap %d > W %d", index, index-n.pendingTr, n.w))
			} else {
				reasons = append(reasons, fmt.Sprintf("Press at %d starts first hit", index))
			}
			n.pending = false
			n.second = false
		}
		return events, reasons
	}

	events = append(events, Event{Kind: Release, Index: index})
	duration := index - n.pressIndex
	n.pressed = false

	if duration >= n.l {
		reasons = append(reasons, fmt.Sprintf("Release at %d duration %d >= L %d", index, duration, n.l))
		if n.second {
			n.pending = false
			n.second = false
			n.discardedCount++
		}
		return events, reasons
	}

	n.shortCount++
	reasons = append(reasons, fmt.Sprintf("Release at %d duration %d < L %d is short", index, duration, n.l))

	if n.second {
		events = append(events, Event{Kind: DoubleClick, Index: index})
		n.pending = false
		n.second = false
		n.doubleCount++
		reasons = append(reasons, "DoubleClick follows second short Release")
		return events, reasons
	}

	n.pending = true
	n.pendingTr = index
	reasons = append(reasons, fmt.Sprintf("first short press waits through %d", index+n.w+1))
	return events, reasons
}

func runNaive(t *testing.T, d, l, w int, levels []int) ([]Event, *naiveRecognizer) {
	t.Helper()
	n := newNaive(d, l, w)
	var events []Event
	t.Logf("naive input d=%d L=%d W=%d levels=%v", d, l, w, levels)
	for index, level := range levels {
		newEvents, reasons := n.sample(index, level)
		events = append(events, newEvents...)
		t.Logf("naive index=%d level=%d events=%v", index, level, newEvents)
		for _, reason := range reasons {
			t.Log("decision:", reason)
		}
	}
	return events, n
}

func runActual(t *testing.T, d, l, w int, levels []int) []Event {
	t.Helper()
	r, err := New(d, l, w)
	if err != nil {
		t.Fatal(err)
	}
	var events []Event
	t.Logf("actual input d=%d L=%d W=%d levels=%v", d, l, w, levels)
	for index, level := range levels {
		newEvents, err := r.Sample(level)
		if err != nil {
			t.Fatalf("Sample at %d: %v", index, err)
		}
		events = append(events, newEvents...)
		t.Logf("actual index=%d level=%d events=%v", index, level, newEvents)
	}
	return events
}

func assertEventShape(t *testing.T, events []Event) {
	t.Helper()
	pressed := false
	for i, event := range events {
		switch event.Kind {
		case Press:
			if pressed {
				t.Fatalf("event %d: Press not alternating: %v", i, events)
			}
			pressed = true
		case Release:
			if !pressed {
				t.Fatalf("event %d: Release not alternating: %v", i, events)
			}
			pressed = false
		}
	}
}

func assertEvents(t *testing.T, got, want []Event) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("events mismatch\ngot:  %v\nwant: %v", got, want)
	}
	assertEventShape(t, got)
}

func TestSpecifiedBoundariesAndNaiveModel(t *testing.T) {
	tests := []struct {
		name    string
		d, l, w int
		levels  []int
		want    []Event
	}{
		{"debounce resets then reaches D", 3, 2, 2,
			[]int{1, 0, 1, 1, 1, 0, 0, 0},
			[]Event{{Press, 4}, {LongPress, 6}, {Release, 7}}},
		{"debounce one before D", 3, 2, 2,
			[]int{1, 1, 0, 1, 1}, nil},
		{"duration exactly L", 1, 3, 2,
			[]int{1, 1, 1, 0, 0, 0, 0},
			[]Event{{Press, 0}, {LongPress, 3}, {Release, 3}}},
		{"duration L minus one", 1, 3, 2,
			[]int{1, 1, 0, 0, 0, 0},
			[]Event{{Press, 0}, {Release, 2}, {SingleClick, 5}}},
		{"second gap exactly W", 1, 5, 2,
			[]int{1, 0, 0, 1, 0, 0, 0, 0},
			[]Event{{Press, 0}, {Release, 1}, {Press, 3}, {Release, 4}, {DoubleClick, 4}}},
		{"second gap W plus one", 1, 5, 2,
			[]int{1, 0, 0, 0, 1, 0, 0, 0, 0, 0},
			[]Event{{Press, 0}, {Release, 1}, {SingleClick, 4}, {Press, 4}, {Release, 5}, {SingleClick, 8}}},
		{"W zero never double clicks", 1, 5, 0,
			[]int{1, 0, 1, 0, 0},
			[]Event{{Press, 0}, {Release, 1}, {SingleClick, 2}, {Press, 2}, {Release, 3}, {SingleClick, 4}}},
		{"second hit long press discarded", 1, 3, 5,
			[]int{1, 0, 0, 1, 1, 1, 0, 0, 0, 0},
			[]Event{{Press, 0}, {Release, 1}, {Press, 3}, {LongPress, 6}, {Release, 6}}},
		{"third press restarts first hit", 1, 5, 3,
			[]int{1, 0, 0, 1, 0, 0, 1, 0, 0, 0, 0, 0},
			[]Event{{Press, 0}, {Release, 1}, {Press, 3}, {Release, 4}, {DoubleClick, 4}, {Press, 6}, {Release, 7}, {SingleClick, 11}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := runActual(t, tt.d, tt.l, tt.w, tt.levels)
			assertEvents(t, got, tt.want)
			naiveEvents, n := runNaive(t, tt.d, tt.l, tt.w, tt.levels)
			if !reflect.DeepEqual(got, naiveEvents) {
				t.Fatalf("naive mismatch\ngot:   %v\nnaive: %v", got, naiveEvents)
			}
			pending := 0
			if n.pending {
				pending = 1
			}
			if want := n.singleCount + 2*n.doubleCount + n.discardedCount + pending; want != n.shortCount {
				t.Fatalf("short invariant short=%d singles=%d doubles=%d discarded=%d pending=%d",
					n.shortCount, n.singleCount, n.doubleCount, n.discardedCount, pending)
			}
		})
	}
}

func TestValidationRejectsEntireOperation(t *testing.T) {
	if _, err := New(0, 1, 0); err != ErrInvalidDebounce {
		t.Fatalf("debounce error = %v", err)
	}
	if _, err := New(1, 0, 0); err != ErrInvalidLongPress {
		t.Fatalf("long-press error = %v", err)
	}
	if _, err := New(1, 1, -1); err != ErrInvalidDoubleClickWindow {
		t.Fatalf("window error = %v", err)
	}

	r, err := New(1, 3, 2)
	if err != nil {
		t.Fatal(err)
	}
	if events, err := r.Sample(2); err != ErrInvalidLevel || events != nil {
		t.Fatalf("invalid Sample = (%v, %v)", events, err)
	}
	if events, err := r.Run([]int{1, 2, 0}); err != ErrInvalidLevel || events != nil {
		t.Fatalf("invalid Run = (%v, %v)", events, err)
	}
	got, err := r.Run([]int{1, 0, 0, 0, 0})
	if err != nil {
		t.Fatal(err)
	}
	assertEvents(t, got, []Event{{Press, 0}, {Release, 1}, {SingleClick, 4}})
}

func TestRandomizedNaiveEquivalenceAndInvariant(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for iteration := 0; iteration < 40; iteration++ {
		d := 1 + rng.Intn(4)
		l := 1 + rng.Intn(6)
		w := rng.Intn(6)
		levels := make([]int, 40+rng.Intn(80))
		for i := range levels {
			levels[i] = rng.Intn(2)
		}

		t.Run(fmt.Sprintf("case-%d-d%d-l%d-w%d", iteration, d, l, w), func(t *testing.T) {
			got := runActual(t, d, l, w, levels)
			want, n := runNaive(t, d, l, w, levels)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("events mismatch\ngot:  %v\nwant: %v\ninput: %v", got, want, levels)
			}
			assertEventShape(t, got)

			r, err := New(d, l, w)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := r.Run(levels); err != nil {
				t.Fatal(err)
			}
			if r.stableLevel != n.stableLevel || r.pressed != n.pressed ||
				r.pendingShort != n.pending || r.discardedCount != n.discardedCount {
				t.Fatalf("state mismatch actual=(stable=%d pressed=%v pending=%v discarded=%d) naive=(stable=%d pressed=%v pending=%v discarded=%d)",
					r.stableLevel, r.pressed, r.pendingShort, r.discardedCount,
					n.stableLevel, n.pressed, n.pending, n.discardedCount)
			}

			pending := 0
			if n.pending {
				pending = 1
			}
			if wantShort := n.singleCount + 2*n.doubleCount + n.discardedCount + pending; wantShort != n.shortCount {
				t.Fatalf("short invariant: short=%d singles=%d doubles=%d discarded=%d pending=%d",
					n.shortCount, n.singleCount, n.doubleCount, n.discardedCount, pending)
			}
		})
	}
}

func TestRunSegmentationAndReplayEquivalent(t *testing.T) {
	levels := []int{1, 0, 0, 1, 0, 0, 1, 1, 1, 0, 0, 1, 0, 0, 0}

	whole, err := New(2, 3, 3)
	if err != nil {
		t.Fatal(err)
	}
	want, err := whole.Run(levels)
	if err != nil {
		t.Fatal(err)
	}

	segmented, err := New(2, 3, 3)
	if err != nil {
		t.Fatal(err)
	}
	cuts := []int{0, 1, 4, 4, 9, 13, len(levels)}
	var got []Event
	for i := 0; i < len(cuts)-1; i++ {
		events, err := segmented.Run(levels[cuts[i]:cuts[i+1]])
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, events...)
	}
	t.Logf("input=%v whole=%v segmented=%v", levels, want, got)
	assertEvents(t, got, want)

	replay, err := New(2, 3, 3)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := replay.Run(levels)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(replayed, want) {
		t.Fatalf("replay mismatch\ngot:  %v\nwant: %v", replayed, want)
	}
}

func TestConcurrentSamplesAreSerializedAndInvalidCallsRejected(t *testing.T) {
	r, err := New(1, 3, 2)
	if err != nil {
		t.Fatal(err)
	}

	const validCalls = 100
	var wg sync.WaitGroup
	for i := 0; i < validCalls; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if events, err := r.Sample(0); err != nil || events != nil {
				t.Errorf("concurrent Sample(0) = (%v, %v)", events, err)
			}
		}()
	}
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if events, err := r.Sample(3); err != ErrInvalidLevel || events != nil {
				t.Errorf("concurrent invalid Sample = (%v, %v)", events, err)
			}
		}()
	}
	wg.Wait()

	if r.sampleCount != validCalls {
		t.Fatalf("sampleCount=%d, want %d", r.sampleCount, validCalls)
	}
	if r.stableLevel != 0 || r.diffCount != 0 {
		t.Fatalf("state after level-zero calls = stable:%d diff:%d", r.stableLevel, r.diffCount)
	}
}
