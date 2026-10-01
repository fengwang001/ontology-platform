package gesture

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
)

func TestRequiredBoundaries(t *testing.T) {
	cases := []struct {
		name   string
		d      int
		l      int
		w      int
		levels []int
		want   []Event
	}{
		{
			name:   "debounce reset and exact threshold",
			d:      3,
			l:      4,
			w:      1,
			levels: []int{1, 0, 1, 1, 0, 1, 1, 1, 0, 0, 0, 1, 0, 0},
			want: []Event{
				{Press, 7},
				{Release, 10},
				{SingleClick, 12},
			},
		},
		{
			name:   "press duration exactly L emits long before release",
			d:      1,
			l:      3,
			w:      2,
			levels: []int{1, 1, 1, 0, 0, 0, 0},
			want: []Event{
				{Press, 0},
				{LongPress, 3},
				{Release, 3},
			},
		},
		{
			name:   "press duration L minus one remains short",
			d:      1,
			l:      3,
			w:      1,
			levels: []int{1, 1, 0, 0, 0},
			want: []Event{
				{Press, 0},
				{Release, 2},
				{SingleClick, 4},
			},
		},
		{
			name:   "next press gap exactly W emits double click",
			d:      1,
			l:      3,
			w:      2,
			levels: []int{1, 0, 0, 1, 0, 0, 0},
			want: []Event{
				{Press, 0},
				{Release, 1},
				{Press, 3},
				{Release, 4},
				{DoubleClick, 4},
			},
		},
		{
			name:   "next press gap W plus one emits single first",
			d:      1,
			l:      3,
			w:      2,
			levels: []int{1, 0, 0, 0, 1, 0, 0, 0, 0},
			want: []Event{
				{Press, 0},
				{Release, 1},
				{SingleClick, 4},
				{Press, 4},
				{Release, 5},
				{SingleClick, 8},
			},
		},
		{
			name:   "W zero never double clicks",
			d:      1,
			l:      3,
			w:      0,
			levels: []int{1, 0, 1, 0, 0},
			want: []Event{
				{Press, 0},
				{Release, 1},
				{SingleClick, 2},
				{Press, 2},
				{Release, 3},
				{SingleClick, 4},
			},
		},
		{
			name:   "second click becoming long discards first",
			d:      1,
			l:      2,
			w:      3,
			levels: []int{1, 0, 0, 1, 1, 0, 0},
			want: []Event{
				{Press, 0},
				{Release, 1},
				{Press, 3},
				{LongPress, 5},
				{Release, 5},
			},
		},
		{
			name:   "third press after double click restarts as first click",
			d:      1,
			l:      4,
			w:      5,
			levels: []int{1, 0, 0, 1, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0},
			want: []Event{
				{Press, 0},
				{Release, 1},
				{Press, 3},
				{Release, 4},
				{DoubleClick, 4},
				{Press, 6},
				{Release, 7},
				{SingleClick, 13},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Logf("input D=%d L=%d W=%d levels=%v", tc.d, tc.l, tc.w, tc.levels)
			recognizer, err := New(tc.d, tc.l, tc.w)
			if err != nil {
				t.Fatal(err)
			}

			var got []Event
			for index, level := range tc.levels {
				events, err := recognizer.Sample(level)
				if err != nil {
					t.Fatalf("index=%d level=%d: %v", index, level, err)
				}
				for _, event := range events {
					t.Logf("output index=%d event=%s; order follows due-time before stable change, release before derived double click", event.Index, event.Kind)
				}
				got = append(got, events...)
			}

			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("events mismatch\ngot:  %v\nwant: %v", got, tc.want)
			}

			naive := naiveRun(t, tc.d, tc.l, tc.w, tc.levels)
			if len(naive) != len(got) {
				t.Fatalf("naive event count=%d, got=%d", len(naive), len(got))
			}
			for index := range got {
				if EventKind(naive[index].kind) != got[index].Kind || naive[index].index != got[index].Index {
					t.Fatalf("naive event %d=(%s,%d), got=(%s,%d)", index, naive[index].kind, naive[index].index, got[index].Kind, got[index].Index)
				}
			}

			stats := recognizer.Stats()
			if stats.ShortCount != stats.SingleClickCount+2*stats.DoubleClickCount+stats.DiscardedCount+stats.PendingCount {
				t.Fatalf("short accounting invariant failed: %+v", stats)
			}
		})
	}
}

func TestNaiveSimulation(t *testing.T) {
	for d := 1; d <= 3; d++ {
		for l := 1; l <= 3; l++ {
			for w := 0; w <= 2; w++ {
				levels := []int{
					0, 1, 0, 1, 1, 1, 0, 1, 0, 0, 1, 1, 1, 1,
					0, 0, 0, 1, 0, 1, 1, 0, 0, 0, 1, 1, 0, 1, 0, 0,
				}
				t.Run("fixed sequence", func(t *testing.T) {
					t.Logf("input D=%d L=%d W=%d levels=%v", d, l, w, levels)
					recognizer, err := New(d, l, w)
					if err != nil {
						t.Fatal(err)
					}

					got, err := recognizer.Run(levels)
					if err != nil {
						t.Fatal(err)
					}

					naive := naiveRun(t, d, l, w, levels)
					if len(naive) != len(got) {
						t.Fatalf("event count naive=%d got=%d", len(naive), len(got))
					}
					for i := range got {
						t.Logf("output event[%d]=(%s,%d); decision checked against explicit stepwise rules", i, got[i].Kind, got[i].Index)
						if EventKind(naive[i].kind) != got[i].Kind || naive[i].index != got[i].Index {
							t.Fatalf("event %d mismatch: naive=(%s,%d) got=(%s,%d)", i, naive[i].kind, naive[i].index, got[i].Kind, got[i].Index)
						}
					}

					assertEventInvariants(t, recognizer)
				})
			}
		}
	}
}

func assertEventInvariants(t *testing.T, recognizer *GestureRecognizer) {
	t.Helper()

	events := recognizer.Events()
	stable := false
	longOpen := false
	for _, event := range events {
		switch event.Kind {
		case Press:
			if stable {
				t.Fatalf("press without preceding release at %d", event.Index)
			}
			stable = true
			longOpen = true
		case Release:
			if !stable {
				t.Fatalf("release without preceding press at %d", event.Index)
			}
			stable = false
			longOpen = false
		case LongPress:
			if !stable || !longOpen {
				t.Fatalf("long press at %d is not inside one press", event.Index)
			}
			longOpen = false
		}
	}

	stats := recognizer.Stats()
	if stats.PendingCount != 0 && stats.PendingCount != 1 {
		t.Fatalf("pending count must be 0 or 1: %d", stats.PendingCount)
	}
	if stats.ShortCount != stats.SingleClickCount+2*stats.DoubleClickCount+stats.DiscardedCount+stats.PendingCount {
		t.Fatalf("short accounting invariant failed: %+v", stats)
	}
}

func TestValidationRejectsWholeOperation(t *testing.T) {
	cases := []struct {
		name string
		d    int
		l    int
		w    int
		want error
	}{
		{name: "debounce", d: 0, l: 1, w: 0, want: ErrInvalidDebounceCount},
		{name: "long press", d: 1, l: 0, w: 0, want: ErrInvalidLongPressThreshold},
		{name: "window", d: 1, l: 1, w: -1, want: ErrInvalidDoubleClickWindow},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(tc.d, tc.l, tc.w)
			if !errors.Is(err, tc.want) {
				t.Fatalf("error=%v, want=%v", err, tc.want)
			}
		})
	}

	recognizer, err := New(1, 3, 2)
	if err != nil {
		t.Fatal(err)
	}

	before := recognizer.Events()
	if _, err := recognizer.Sample(2); !errors.Is(err, ErrInvalidLevel) {
		t.Fatalf("Sample error=%v, want=%v", err, ErrInvalidLevel)
	}
	if after := recognizer.Events(); !reflect.DeepEqual(after, before) {
		t.Fatalf("invalid Sample changed events: before=%v after=%v", before, after)
	}

	badBatch := []int{1, 1, 2}
	if _, err := recognizer.Run(badBatch); !errors.Is(err, ErrInvalidLevel) {
		t.Fatalf("Run error=%v, want=%v", err, ErrInvalidLevel)
	}
	if after := recognizer.Events(); !reflect.DeepEqual(after, before) {
		t.Fatalf("invalid Run changed events: before=%v after=%v", before, after)
	}
}

func TestDebounceCountOneBelowThreshold(t *testing.T) {
	recognizer, err := New(3, 5, 2)
	if err != nil {
		t.Fatal(err)
	}

	events, err := recognizer.Run([]int{1, 1, 0, 1, 1})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("input D=3 levels=[1 1 0 1 1]; output=%v; decision=counts reach two but reset before reaching three, so no press", events)
	if len(events) != 0 {
		t.Fatalf("expected no stable event one count below D, got=%v", events)
	}
}

func TestRunSplittingReplayAndConcurrency(t *testing.T) {
	levels := []int{1, 1, 0, 0, 0, 1, 0, 0, 0, 1, 1, 1, 0, 0, 0}
	reference, err := New(2, 3, 2)
	if err != nil {
		t.Fatal(err)
	}
	referenceEvents, err := reference.Run(levels)
	if err != nil {
		t.Fatal(err)
	}

	for split := 0; split <= len(levels); split++ {
		t.Run(fmt.Sprintf("split-%d", split), func(t *testing.T) {
			recognizer, err := New(2, 3, 2)
			if err != nil {
				t.Fatal(err)
			}

			first, err := recognizer.Run(levels[:split])
			if err != nil {
				t.Fatal(err)
			}
			second, err := recognizer.Run(levels[split:])
			if err != nil {
				t.Fatal(err)
			}
			got := append(append([]Event{}, first...), second...)
			if !reflect.DeepEqual(got, referenceEvents) {
				t.Fatalf("split events mismatch\ngot:  %v\nwant: %v", got, referenceEvents)
			}
			if all := recognizer.Events(); !reflect.DeepEqual(all, referenceEvents) {
				t.Fatalf("stored events mismatch\ngot:  %v\nwant: %v", all, referenceEvents)
			}
			assertEventInvariants(t, recognizer)
		})
	}

	replay, err := New(2, 3, 2)
	if err != nil {
		t.Fatal(err)
	}
	replayEvents, err := replay.Run(levels)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(replayEvents, referenceEvents) {
		t.Fatalf("replay events differ: %v vs %v", replayEvents, referenceEvents)
	}

	concurrent, err := New(2, 4, 2)
	if err != nil {
		t.Fatal(err)
	}
	var waitGroup sync.WaitGroup
	for range 10 {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			if _, sampleErr := concurrent.Sample(1); sampleErr != nil {
				t.Error(sampleErr)
			}
		}()
	}
	for range 20 {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			_ = concurrent.Events()
			_ = concurrent.Stats()
		}()
	}
	waitGroup.Wait()

	wantConcurrent := []Event{{Press, 1}, {LongPress, 5}}
	if got := concurrent.Events(); !reflect.DeepEqual(got, wantConcurrent) {
		t.Fatalf("concurrent events=%v, want=%v", got, wantConcurrent)
	}
	assertEventInvariants(t, concurrent)
	t.Logf("concurrent input=ten level-1 samples; output=%v; all calls are linearized by one mutex", wantConcurrent)
}
