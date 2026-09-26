package bq

import "testing"

// Table-driven core semantics at the bq layer.
func TestProduceConsumeTable(t *testing.T) {
	cases := []struct {
		name string
		cap  int64
		ops  []struct {
			prod bool
			n    int64
		}
		wantOK    []bool
		wantCount int64
		wantFree  int64
	}{
		{
			name: "fill exactly then backpressure",
			cap:  3,
			ops: []struct {
				prod bool
				n    int64
			}{{true, 3}, {true, 1}, {false, 3}, {false, 1}},
			wantOK:    []bool{true, false, true, false}, // last: underflow on empty
			wantCount: 0,
			wantFree:  3,
		},
		{
			name: "all-or-nothing batches",
			cap:  5,
			ops: []struct {
				prod bool
				n    int64
			}{{true, 2}, {true, 4}, {false, 1}, {true, 3}},
			wantOK:    []bool{true, false, true, true}, // P4 rejected (2+4>5); then P3 fills
			wantCount: 4,
			wantFree:  1,
		},
		{
			name: "underflow changes nothing",
			cap:  4,
			ops: []struct {
				prod bool
				n    int64
			}{{true, 2}, {false, 3}, {false, 2}},
			wantOK:    []bool{true, false, true},
			wantCount: 0,
			wantFree:  4,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q := New(tc.cap)
			for i, op := range tc.ops {
				var got bool
				if op.prod {
					got = q.Produce(op.n)
				} else {
					got = q.Consume(op.n)
				}
				if got != tc.wantOK[i] {
					t.Fatalf("op %d: got ok=%v want %v", i, got, tc.wantOK[i])
				}
			}
			if q.Count() != tc.wantCount || q.Free() != tc.wantFree {
				t.Fatalf("count=%d free=%d want %d/%d", q.Count(), q.Free(), tc.wantCount, tc.wantFree)
			}
		})
	}
}

// The head pointer must advance instead of shifting the surviving slice: the
// number of elements moved/copied by Consume must stay a small constant (here
// 0) and must not grow with the number m of queued elements.
func TestConsumeDoesNotMoveElements(t *testing.T) {
	for _, m := range []int64{100, 1000, 10000} {
		q := New(m)
		if !q.Produce(m) {
			t.Fatalf("m=%d: fill failed", m)
		}
		if !q.Consume(1) {
			t.Fatalf("m=%d: consume failed", m)
		}
		if q.lastConsumeMoved != 0 {
			t.Fatalf("m=%d: moved=%d, want 0 (no linear shifting)", m, q.lastConsumeMoved)
		}
		if q.Count() != m-1 {
			t.Fatalf("m=%d: count=%d want %d", m, q.Count(), m-1)
		}
	}
}

// Interleaved produce/consume must stay correct when head wraps the ring.
func TestRingWraparound(t *testing.T) {
	const cap = 3
	q := New(cap)
	var naive int64
	for round := 0; round < 10; round++ {
		if !q.Produce(2) {
			t.Fatalf("round %d: produce failed, count=%d", round, q.Count())
		}
		naive += 2
		if !q.Consume(2) {
			t.Fatalf("round %d: consume failed, count=%d", round, q.Count())
		}
		naive -= 2
		if q.Count() != naive || q.Free() != cap-naive {
			t.Fatalf("round %d: count=%d free=%d naive=%d", round, q.Count(), q.Free(), naive)
		}
	}
	// After many wraps the ring must still fill to full capacity.
	if !q.Produce(cap) || q.Count() != cap || q.Free() != 0 {
		t.Fatalf("post-wrap fill broken: count=%d free=%d", q.Count(), q.Free())
	}
}
