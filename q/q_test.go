package q

import (
	"errors"
	"math/rand"
	"testing"
)

// naive 是朴素参照实现：切片队列，语义与规格一致。
type naive struct {
	s   []int
	max int
}

func (n *naive) enq(v int) error {
	if len(n.s) >= n.max {
		return ErrFull
	}
	n.s = append(n.s, v)
	return nil
}

func (n *naive) deq() (int, bool) {
	if len(n.s) == 0 {
		return 0, false
	}
	v := n.s[0]
	n.s = n.s[1:]
	return v, true
}

// 不变量 1：任意交错序列下与朴素参照逐次一致。
func TestMatchesNaiveReference(t *testing.T) {
	cases := []struct {
		seed int64
		max  int
		ops  int
	}{{1, 1, 200}, {2, 3, 500}, {3, 17, 1000}, {4, 100, 2000}}
	for _, c := range cases {
		qu, _ := New(c.max)
		ref := &naive{max: c.max}
		r := rand.New(rand.NewSource(c.seed))
		for i := 0; i < c.ops; i++ {
			if r.Intn(2) == 0 {
				v := r.Intn(1000)
				got, want := qu.Enqueue(v), ref.enq(v)
				if (got == nil) != (want == nil) {
					t.Fatalf("%+v op%d enq(%d): got %v want %v", c, i, v, got, want)
				}
			} else {
				gv, gok := qu.Dequeue()
				wv, wok := ref.deq()
				if gv != wv || gok != wok {
					t.Fatalf("%+v op%d deq: got (%d,%v) want (%d,%v)", c, i, gv, gok, wv, wok)
				}
			}
			if qu.Len() != len(ref.s) {
				t.Fatalf("%+v op%d: Len=%d want %d", c, i, qu.Len(), len(ref.s))
			}
		}
	}
}

// 不变量 2：FIFO 且 Len 恒等于已入队数减已出队数。
func TestFIFOAndLenConservation(t *testing.T) {
	for _, max := range []int{1, 2, 7, 1000} {
		qu, _ := New(max)
		for i := 0; i < max; i++ {
			if err := qu.Enqueue(i); err != nil || qu.Len() != i+1 {
				t.Fatalf("max=%d enq %d: err=%v Len=%d", max, i, err, qu.Len())
			}
		}
		for i := 0; i < max; i++ {
			v, ok := qu.Dequeue()
			if !ok || v != i || qu.Len() != max-i-1 {
				t.Fatalf("max=%d deq %d: got (%d,%v) Len=%d", max, i, v, ok, qu.Len())
			}
		}
	}
}

// 不变量 3：空/满精确，O(1) 判定。
func TestEmptyFullExact(t *testing.T) {
	qu, _ := New(2)
	if v, ok := qu.Dequeue(); ok || v != 0 {
		t.Fatalf("empty deq = (%d,%v)", v, ok)
	}
	_ = qu.Enqueue(1)
	_ = qu.Enqueue(2)
	if err := qu.Enqueue(3); !errors.Is(err, ErrFull) || qu.Len() != 2 {
		t.Fatalf("full enq = %v Len=%d", err, qu.Len())
	}
}

// 不变量 4：失败不留痕；错误互不相同；Close 后排空不丢、Enqueue 恒拒。
func TestFailureLeavesNoTrace(t *testing.T) {
	for _, bad := range []int{0, -5} {
		if _, err := New(bad); !errors.Is(err, ErrBadMaxLen) {
			t.Fatalf("New(%d) = %v", bad, err)
		}
	}
	qu, _ := New(1)
	_ = qu.Enqueue(42)
	if err := qu.Enqueue(1); !errors.Is(err, ErrFull) || qu.Len() != 1 {
		t.Fatalf("rejected enq changed state: %v Len=%d", err, qu.Len())
	}
	_ = qu.Close()
	if err := qu.Enqueue(2); !errors.Is(err, ErrClosed) || qu.Len() != 1 {
		t.Fatalf("closed enq: %v Len=%d", err, qu.Len())
	}
	if v, ok := qu.Dequeue(); !ok || v != 42 {
		t.Fatalf("close must drain: (%d,%v)", v, ok)
	}
	if _, ok := qu.Dequeue(); ok {
		t.Fatal("drained queue must stay empty")
	}
	if errors.Is(ErrFull, ErrClosed) || errors.Is(ErrFull, ErrBadMaxLen) || errors.Is(ErrClosed, ErrBadMaxLen) {
		t.Fatal("sentinel errors must be distinct")
	}
}
