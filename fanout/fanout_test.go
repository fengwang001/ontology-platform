package fanout

import (
	"errors"
	"testing"
)

// TestNewRejectsBadCapacity: construction failures leave nothing and use
// a decidable sentinel.
func TestNewRejectsBadCapacity(t *testing.T) {
	for _, c := range []int{0, -1, -100} {
		q, err := New(c)
		if q != nil || !errors.Is(err, ErrInvalidCapacity) {
			t.Fatalf("New(%d) = (%v,%v), want (nil,ErrInvalidCapacity)", c, q, err)
		}
	}
}

// TestFIFOOrder pins arrival order and the empty/ok boundary across
// several capacities and enqueue/consume interleavings (table-driven).
func TestFIFOOrder(t *testing.T) {
	cases := []struct {
		name string
		c    int
		seq  []int64 // enqueue these; values > c are tail-dropped
		want []int64 // expected dequeue order
		drop int
	}{
		{"cap1", 1, []int64{1, 2, 3}, []int64{1}, 2},
		{"cap2", 2, []int64{1, 2, 3}, []int64{1, 2}, 1},
		{"cap3", 3, []int64{1, 2, 3, 4}, []int64{1, 2, 3}, 1},
		{"wrap", 3, []int64{1, 2, 3}, []int64{1, 2, 3}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q, err := New(tc.c)
			if err != nil {
				t.Fatal(err)
			}
			for _, v := range tc.seq {
				q.Enqueue(v)
			}
			if q.Len() != len(tc.want) {
				t.Fatalf("Len=%d want %d", q.Len(), len(tc.want))
			}
			if q.DropCount() != tc.drop {
				t.Fatalf("DropCount=%d want %d", q.DropCount(), tc.drop)
			}
			var got []int64
			for {
				ev, ok := q.Dequeue()
				if !ok {
					break
				}
				got = append(got, ev)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("dequeued %v want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("dequeued %v want %v (FIFO violated)", got, tc.want)
				}
			}
			if _, ok := q.Dequeue(); ok {
				t.Fatal("empty queue returned ok=true")
			}
		})
	}
}

// TestRingWrapAfterConsume proves the ring head/n indexes stay correct
// after many enqueue/consume cycles (no stale slot reuse, no reorder).
func TestRingWrapAfterConsume(t *testing.T) {
	q, _ := New(3)
	for cycle := 0; cycle < 50; cycle++ {
		for _, v := range []int64{1, 2, 3} {
			q.Enqueue(v)
		}
		for _, want := range []int64{1, 2, 3} {
			ev, ok := q.Dequeue()
			if !ok || ev != want {
				t.Fatalf("cycle %d got (%v,%v) want %d", cycle, ev, ok, want)
			}
		}
	}
}

// TestTailDropKeepsOldEvents: when full the newcomer is dropped and the
// queued (older) events remain byte-for-byte in place.
func TestTailDropKeepsOldEvents(t *testing.T) {
	q, _ := New(2)
	q.Enqueue(7)
	q.Enqueue(8)
	q.Enqueue(9) // dropped
	if d := q.DropCount(); d != 1 {
		t.Fatalf("drop=%d want 1", d)
	}
	if e0, _ := q.Dequeue(); e0 != 7 {
		t.Fatalf("head=%d want 7", e0)
	}
	if e1, _ := q.Dequeue(); e1 != 8 {
		t.Fatalf("second=%d want 8", e1)
	}
}

// TestFullCheckSlotsConstant is the complexity proof: build a queue at
// several capacities m in [100,10000], publish, and assert the number of
// slots inspected for the fullness decision never exceeds a small
// constant and does not grow with m. It reads the unexported counter
// directly (same package); no exported path exposes the value.
func TestFullCheckSlotsConstant(t *testing.T) {
	const bound = 1
	for _, m := range []int{100, 316, 1000, 3162, 10000} {
		q, err := New(m)
		if err != nil {
			t.Fatal(err)
		}
		q.Enqueue(1) // empty -> enqueue path
		if q.slotsChecked > bound {
			t.Fatalf("m=%d empty enqueue inspected %d slots > %d", m, q.slotsChecked, bound)
		}
		for i := 1; i < m; i++ {
			q.Enqueue(int64(i + 1))
		}
		q.Enqueue(999) // full -> tail-drop path
		if q.slotsChecked > bound {
			t.Fatalf("m=%d full decision inspected %d slots, grows with m", m, q.slotsChecked)
		}
	}
}
