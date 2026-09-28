package slidingwindow

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"sync"
	"testing"
)

func mustWindow(t *testing.T, capacity int, opts ...Option) *Window {
	t.Helper()
	w, err := New(capacity, opts...)
	if err != nil {
		t.Fatalf("New(%d): unexpected error %v", capacity, err)
	}
	return w
}

func assertMax(t *testing.T, w *Window, want float64) {
	t.Helper()
	got, err := w.Max()
	if err != nil {
		t.Fatalf("Max: unexpected error %v", err)
	}
	if got != want {
		t.Fatalf("Max = %v, want %v", got, want)
	}
}

// Tied maxima: the older duplicate is evicted while the surviving duplicate
// must keep reporting the same maximum.
func TestTiedMaxEviction(t *testing.T) {
	var logs bytes.Buffer
	w := mustWindow(t, 3, WithLogger(&logs))

	for _, v := range []float64{5, 5, 1} {
		if _, _, err := w.Append(v); err != nil {
			t.Fatalf("Append(%v): %v", v, err)
		}
	}
	assertMax(t, w, 5)

	evicted, ok, err := w.Append(2)
	if err != nil || !ok || evicted != 5 {
		t.Fatalf("Append eviction = (%v,%v,%v), want (5,true,nil)", evicted, ok, err)
	}
	// Window is now [5,1,2]; the remaining tied 5 is still the maximum.
	assertMax(t, w, 5)

	evicted, err = w.Evict()
	if err != nil || evicted != 5 {
		t.Fatalf("Evict = (%v,%v), want (5,nil)", evicted, err)
	}
	// Window is now [1,2]; the tied maximum is gone.
	assertMax(t, w, 2)

	assertLogShape(t, &logs)
}

// A strictly decreasing run means every element stays on the deque;
// evictions then walk the front one by one.
func TestMonotonicDecreasingRun(t *testing.T) {
	var logs bytes.Buffer
	w := mustWindow(t, 4, WithLogger(&logs))

	for _, v := range []float64{4, 3, 2, 1} {
		if _, _, err := w.Append(v); err != nil {
			t.Fatalf("Append(%v): %v", v, err)
		}
	}
	if got := w.Moves(); got != 0 {
		t.Fatalf("Moves after decreasing run = %d, want 0", got)
	}

	evicted, ok, _ := w.Append(0)
	if !ok || evicted != 4 {
		t.Fatalf("Append eviction = (%v,%v), want (4,true)", evicted, ok)
	}
	assertMax(t, w, 3)

	evicted, err := w.Evict()
	if err != nil || evicted != 3 {
		t.Fatalf("Evict = (%v,%v), want (3,nil)", evicted, err)
	}
	assertMax(t, w, 2)
	assertLogShape(t, &logs)
}

// Increasing input causes each newcomer to displace all smaller tail
// elements; each element is displaced at most once over the run.
func TestMovesAtMostOncePerElement(t *testing.T) {
	w := mustWindow(t, 3)
	input := []float64{1, 2, 3, 4, 5, 6}
	var totalPopped uint64
	for _, v := range input {
		before := w.Moves()
		if _, _, err := w.Append(v); err != nil {
			t.Fatalf("Append(%v): %v", v, err)
		}
		totalPopped += w.Moves() - before
	}
	// Element 1 displaced by 2, 2 by 3, 3 by 4, 4 by 5, 5 by 6 -> 5 moves.
	if totalPopped != 5 || w.Moves() != 5 {
		t.Fatalf("Moves = %d (delta sum %d), want 5", w.Moves(), totalPopped)
	}
	assertMax(t, w, 6)
}

func TestInvalidInputsAndEmptyWindow(t *testing.T) {
	t.Run("invalid capacity", func(t *testing.T) {
		for _, c := range []int{0, -1, -42} {
			_, err := New(c)
			if !errors.Is(err, ErrInvalidCapacity) {
				t.Fatalf("New(%d) err = %v, want ErrInvalidCapacity", c, err)
			}
		}
	})

	t.Run("invalid value leaves state untouched", func(t *testing.T) {
		var logs bytes.Buffer
		w := mustWindow(t, 2, WithLogger(&logs))
		if _, _, err := w.Append(1); err != nil {
			t.Fatal(err)
		}

		for _, v := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
			_, _, err := w.Append(v)
			if !errors.Is(err, ErrInvalidValue) {
				t.Fatalf("Append(%v) err = %v, want ErrInvalidValue", v, err)
			}
		}
		if w.Len() != 1 || w.Moves() != 0 {
			t.Fatalf("state changed after rejected append: len=%d moves=%d", w.Len(), w.Moves())
		}
		assertMax(t, w, 1)
	})

	t.Run("evict and max on empty window", func(t *testing.T) {
		w := mustWindow(t, 2)
		if _, err := w.Evict(); !errors.Is(err, ErrEmptyWindow) {
			t.Fatalf("Evict err = %v, want ErrEmptyWindow", err)
		}
		if _, err := w.Max(); !errors.Is(err, ErrEmptyWindow) {
			t.Fatalf("Max err = %v, want ErrEmptyWindow", err)
		}
		if _, _, err := w.Snapshot(); !errors.Is(err, ErrEmptyWindow) {
			t.Fatalf("Snapshot err = %v, want ErrEmptyWindow", err)
		}
	})
}

func TestBatchAppendAtomicity(t *testing.T) {
	t.Run("all valid", func(t *testing.T) {
		w := mustWindow(t, 10)
		if err := w.BatchAppend([]float64{3, 1, 4, 1, 5}); err != nil {
			t.Fatalf("BatchAppend: %v", err)
		}
		assertMax(t, w, 5)
		if w.Len() != 5 {
			t.Fatalf("Len = %d, want 5", w.Len())
		}
	})

	t.Run("one invalid rejects the whole batch", func(t *testing.T) {
		w := mustWindow(t, 10)
		if err := w.BatchAppend([]float64{7, math.NaN(), 2}); !errors.Is(err, ErrInvalidValue) {
			t.Fatalf("BatchAppend err = %v, want ErrInvalidValue", err)
		}
		if w.Len() != 0 || w.Moves() != 0 {
			t.Fatalf("rejected batch changed state: len=%d moves=%d", w.Len(), w.Moves())
		}
	})

	t.Run("empty batch is a no-op", func(t *testing.T) {
		w := mustWindow(t, 2)
		if err := w.BatchAppend(nil); err != nil {
			t.Fatalf("BatchAppend(nil): %v", err)
		}
	})
}

func TestSnapshotConsistencyAndDeterminism(t *testing.T) {
	sequence := []float64{9, 2, 7, 3, 7, 8, 1, 8, 6, 2, 10}

	var first [][]float64
	for _, cap := range []int{1, 2, 3, 4, 5} {
		w := mustWindow(t, cap)
		for _, v := range sequence {
			if _, _, err := w.Append(v); err != nil {
				t.Fatal(err)
			}
			values, max, err := w.Snapshot()
			if err != nil {
				t.Fatal(err)
			}
			var scanMax float64 = values[0]
			for _, x := range values[1:] {
				if x > scanMax {
					scanMax = x
				}
			}
			if scanMax != max {
				t.Fatalf("snapshot max %v != scan of %v", max, values)
			}
			if len(values) == 0 {
				t.Fatal("empty snapshot in non-empty window")
			}
			first = append(first, append([]float64(nil), append(values, max)...))
		}
	}

	// Replaying the exact sequence must yield byte-identical outputs.
	var second [][]float64
	for _, cap := range []int{1, 2, 3, 4, 5} {
		w := mustWindow(t, cap)
		for _, v := range sequence {
			if _, _, err := w.Append(v); err != nil {
				t.Fatal(err)
			}
			values, max, _ := w.Snapshot()
			second = append(second, append([]float64(nil), append(values, max)...))
		}
	}
	if len(first) != len(second) {
		t.Fatalf("run length differs: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if len(first[i]) != len(second[i]) {
			t.Fatalf("record %d length differs", i)
		}
		for j := range first[i] {
			if first[i][j] != second[i][j] {
				t.Fatalf("record %d position %d: %v vs %v", i, j, first[i][j], second[i][j])
			}
		}
	}
}

func TestConcurrentReads(t *testing.T) {
	w := mustWindow(t, 100)

	var writers, readers sync.WaitGroup
	stop := make(chan struct{})
	var stopOnce sync.Once

	writers.Add(1)
	go func() {
		defer writers.Done()
		n := 0
		for {
			select {
			case <-stop:
				return
			default:
				v := float64(n)
				if _, _, err := w.Append(v); err == nil {
					if n%37 == 0 {
						_, _ = w.Evict()
					}
				}
				n++
			}
		}
	}()

	for i := 0; i < 8; i++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for range 5000 {
				select {
				case <-stop:
					return
				default:
				}
				if values, max, err := w.Snapshot(); err == nil {
					scanMax := values[0]
					for _, x := range values[1:] {
						if x > scanMax {
							scanMax = x
						}
					}
					if scanMax != max {
						t.Errorf("concurrent snapshot max %v != scan of %v", max, values)
						stopOnce.Do(func() { close(stop) })
						return
					}
				}
				if _, err := w.Max(); err != nil && !errors.Is(err, ErrEmptyWindow) {
					t.Errorf("unexpected Max error: %v", err)
					stopOnce.Do(func() { close(stop) })
					return
				}
			}
		}()
	}

	// Bound the run by append progress instead of wall time.
	threshold := w.appendedCount() + 3000
	for w.appendedCount() < threshold {
		select {
		case <-stop:
			break
		default:
		}
		if w.appendedCount() >= threshold {
			break
		}
	}
	stopOnce.Do(func() { close(stop) })
	writers.Wait()
	readers.Wait()
}

func TestEvictOrdering(t *testing.T) {
	w := mustWindow(t, 3)
	for _, v := range []float64{10, 20, 30} {
		_, _, _ = w.Append(v)
	}
	for _, want := range []float64{10, 20, 30} {
		got, err := w.Evict()
		if err != nil || got != want {
			t.Fatalf("Evict = (%v,%v), want %v", got, err, want)
		}
	}
	if _, err := w.Evict(); !errors.Is(err, ErrEmptyWindow) {
		t.Fatalf("Evict after drain err = %v, want ErrEmptyWindow", err)
	}
}

// Every JSON log record must expose the input, eviction information and the
// current maximum together with the basis used to derive it.
func assertLogShape(t *testing.T, logs *bytes.Buffer) {
	t.Helper()
	dec := json.NewDecoder(logs)
	records := 0
	for dec.More() {
		var rec map[string]any
		if err := dec.Decode(&rec); err != nil {
			t.Fatalf("decoding log record: %v", err)
		}
		if _, ok := rec["basis"]; !ok {
			t.Fatalf("log record missing basis: %v", rec)
		}
		if _, ok := rec["max"]; !ok {
			if rec["window_len"].(float64) != 0 {
				t.Fatalf("non-empty log record missing max: %v", rec)
			}
		}
		if _, ok := rec["evicted_value"]; !ok {
			t.Fatalf("log record missing evicted_value: %v", rec)
		}
		records++
	}
	if records == 0 {
		t.Fatal("no log records produced")
	}
}
