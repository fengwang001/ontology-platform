package api_test

import (
	"errors"
	"sync/atomic"
	"testing"

	"ontology/api"
	"ontology/bp"
)

type op struct {
	p bool
	n int64
}

func do(q *api.Queue, o op) byte {
	if !o.p {
		if q.Consume(o.n) != nil {
			return 'E'
		}
		return 'O'
	}
	placed, err := q.Produce(o.n)
	switch {
	case err != nil:
		return 'E'
	case !placed:
		return 'B'
	default:
		return 'O'
	}
}

func genOps(seed int64, k int) []op {
	ops := make([]op, k)
	for i := range ops {
		seed = seed*6364136223846793005 + 1442695040888963407
		ops[i] = op{seed&1 == 0, seed%5 - 1}
	}
	return ops
}

// model drives a queue plus a naive slice, verifying invariants 1-3 each op.
func model(t *testing.T, cap int64, ops []op) {
	t.Helper()
	q, _ := api.New(cap)
	var ref []int
	var sp, sc int64
	for i, o := range ops {
		if do(q, o) == 'O' {
			if o.p {
				ref = append(ref, make([]int, o.n)...)
				sp += o.n
			} else {
				ref = ref[o.n:]
				sc += o.n
			}
		}
		c := q.Count()
		if int64(len(ref)) != c || c < 0 || c > cap || c != sp-sc {
			t.Fatalf("cap=%d step %d cnt=%d naive=%d sp=%d sc=%d", cap, i, c, len(ref), sp, sc)
		}
	}
}

func TestEightStepTable(t *testing.T) {
	q, _ := api.New(5)
	ops := [8]op{{true, 3}, {true, 2}, {true, 1}, {false, 2}, {true, 3}, {false, 1}, {true, 3}, {false, 6}}
	want := [8]byte{'O', 'O', 'B', 'O', 'B', 'O', 'O', 'E'}
	cnt := [8]int64{3, 5, 5, 3, 3, 2, 5, 5}
	for i := range ops {
		if do(q, ops[i]) != want[i] || q.Count() != cnt[i] {
			t.Fatalf("step %d cnt=%d want %c cnt=%d", i+1, q.Count(), want[i], cnt[i])
		}
	}
}

func TestNaiveReference(t *testing.T) {
	for _, cap := range []int64{1, 5, 17} {
		model(t, cap, genOps(cap+7, 120))
	}
}

func TestInvariants(t *testing.T) {
	for _, cap := range []int64{1, 3, 8, 50} {
		model(t, cap, genOps(cap*13+1, 150))
	}
}

func TestRejectedOpsNoTrace(t *testing.T) {
	for _, cap := range []int64{0, -3} {
		if _, e := api.New(cap); !errors.Is(e, bp.ErrBadCapacity) {
			t.Fatalf("New(%d): %v", cap, e)
		}
	}
	q, _ := api.New(5)
	q.Produce(2)
	p0 := func() error { _, e := q.Produce(0); return e }
	pn := func() error { _, e := q.Produce(-4); return e }
	c0 := func() error { return q.Consume(0) }
	cu := func() error { return q.Consume(3) }
	got := []error{p0(), pn(), c0(), cu()}
	want := []error{bp.ErrIllegalProduce, bp.ErrIllegalProduce, bp.ErrIllegalConsume, bp.ErrUnderflow}
	for i, e := range got {
		if !errors.Is(e, want[i]) {
			t.Fatalf("case %d: %v want %v", i, e, want[i])
		}
	}
	if q.Count() != 2 {
		t.Fatalf("rejected ops changed count to %d", q.Count())
	}
	if bp.ErrBadCapacity == bp.ErrIllegalProduce || bp.ErrIllegalProduce == bp.ErrIllegalConsume ||
		bp.ErrIllegalConsume == bp.ErrUnderflow || bp.ErrBadCapacity == bp.ErrUnderflow {
		t.Fatal("the four sentinel errors are not distinct")
	}
	if placed, _ := q.Produce(3); !placed || q.Count() != 5 {
		t.Fatal("queue not usable after rejections")
	}
}

func TestSelfCheck(t *testing.T) {
	q, _ := api.New(5)
	if err := q.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

// TestConcurrentProduce keeps reading Count while N producers each place 1.
func TestConcurrentProduce(t *testing.T) {
	const n = 300
	q, _ := api.New(n)
	var done atomic.Int32
	for i := 0; i < n; i++ {
		go func() {
			if placed, _ := q.Produce(1); !placed {
				t.Error("unexpected backpressure")
			}
			done.Add(1)
		}()
	}
	for done.Load() < n {
		if c := q.Count(); c < 0 || c > n {
			t.Fatalf("count %d out of [0,%d]", c, n)
		}
	}
	if q.Count() != n {
		t.Fatalf("count=%d want %d", q.Count(), n)
	}
}
