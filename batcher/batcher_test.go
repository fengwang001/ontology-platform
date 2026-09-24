package batcher

import (
	"testing"
	"time"
)

type fakeTimer struct {
	ch chan time.Time
}

func (f *fakeTimer) C() <-chan time.Time { return f.ch }
func (f *fakeTimer) Stop() bool          { return true }

type fakeClock struct{ timer *fakeTimer }

func (f *fakeClock) NewTimer(time.Duration) Timer {
	f.timer = &fakeTimer{ch: make(chan time.Time, 1)}
	return f.timer
}

func drain[T any](b *Batcher[T]) [][]T {
	var got [][]T
	for batch := range b.Batches() {
		got = append(got, batch)
	}
	return got
}

func TestTriggers(t *testing.T) {
	sizeInt := func(v int) int { return v }
	cases := []struct {
		name     string
		maxItems int
		maxBytes int
		maxWait  time.Duration
		inputs   []int
		fire     bool
		want     [][]int
	}{
		{"count", 3, 100, time.Second, []int{1, 1, 1, 1}, false, [][]int{{1, 1, 1}, {1}}},
		{"bytes", 5, 10, time.Second, []int{4, 4, 4}, false, [][]int{{4, 4}, {4}}},
		{"timeout", 5, 100, time.Second, []int{1}, true, [][]int{{1}}},
		{"zero-wait", 5, 100, 0, []int{1, 2}, false, [][]int{{1}, {2}}},
		{"oversized-singleton", 5, 10, time.Second, []int{4, 99, 4}, false, [][]int{{4}, {99}, {4}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clk := &fakeClock{}
			b := New[int](Config[int]{
				MaxItems: tc.maxItems,
				MaxBytes: tc.maxBytes,
				MaxWait:  tc.maxWait,
				Size:     sizeInt,
				Clock:    clk,
			})
			gotCh := make(chan [][]int, 1)
			go func() { gotCh <- drain(b) }()
			for _, v := range tc.inputs {
				if !b.Add(v) {
					t.Fatal("Add rejected before close")
				}
			}
			if tc.fire {
				clk.timer.ch <- time.Time{}
				time.Sleep(time.Millisecond)
			}
			b.Close()
			got := <-gotCh
			if len(got) != len(tc.want) {
				t.Fatalf("batches=%v want %v", got, tc.want)
			}
			for i := range got {
				if len(got[i]) != len(tc.want[i]) {
					t.Fatalf("batch %d = %v want %v", i, got[i], tc.want[i])
				}
				for j := range got[i] {
					if got[i][j] != tc.want[i][j] {
						t.Fatalf("batch %d = %v want %v", i, got, tc.want)
					}
				}
			}
		})
	}
}

func TestCloseRejectsAdd(t *testing.T) {
	b := New[int](Config[int]{MaxItems: 2, MaxBytes: 10, Size: func(int) int { return 1 }})
	go func() { <-b.Batches() }()
	b.Close()
	if b.Add(1) {
		t.Fatal("Add after close must fail")
	}
}
