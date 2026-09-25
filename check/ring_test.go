package check

import (
	"errors"
	"math/rand"
	"sync"
	"testing"

	"ontology/ring"
)

func ok(t *testing.T, c bool, f string, a ...any) {
	t.Helper()
	if !c {
		t.Fatalf(f, a...)
	}
}

func TestBadCap(t *testing.T) {
	for _, c := range []struct {
		n    int
		want error
	}{{0, ErrBadCap}, {-1, ErrBadCap}, {4, nil}} {
		b, err := ring.New[int](c.n)
		ok(t, errors.Is(err, c.want), "cap=%d err=%v", c.n, err)
		ok(t, c.want != nil || b != nil && b.Cap() == c.n, "Cap mismatch")
	}
}

func TestFillAndFree(t *testing.T) {
	for _, n := range []int{1, 4, 8} {
		b, _ := ring.New[int](n)
		ok(t, b.Empty() && !b.Full(), "new not empty")
		for i := 1; i <= n; i++ {
			ok(t, b.Enqueue(i) == nil, "enqueue #%d", i)
		}
		ok(t, b.Full() && !b.Empty() && b.Len() == n, "not full len=%d", b.Len())
		ok(t, errors.Is(b.Enqueue(99), ErrFull), "extra not ErrFull")
		ok(t, b.Len() == n, "failed enqueue mutated state")
		v, got := b.Dequeue()
		ok(t, got && v == 1, "dequeue=(%d,%v)", v, got)
		ok(t, b.Enqueue(100) == nil, "freed slot unusable")
	}
}

func TestSequentialSemantics(t *testing.T) {
	const n = 8
	b, _ := ring.New[int](n)
	ref, _ := NewNaive[int](n)
	rng := rand.New(rand.NewSource(7))
	seq := 0
	if v, got := b.Dequeue(); got || v != 0 {
		t.Fatalf("empty dequeue=(%d,%v)", v, got)
	}
	for i := 0; i < 10000; i++ {
		if rng.Intn(2) == 0 && b.Len() < n {
			seq++
			_ = ref.Enqueue(seq)
			ok(t, b.Enqueue(seq) == nil, "step %d enqueue", i)
		} else {
			gv, gok := b.Dequeue()
			rv, rok := ref.Dequeue()
			ok(t, gok == rok && gv == rv, "step %d: %d,%v vs %d,%v", i, gv, gok, rv, rok)
		}
		ok(t, b.Len() == ref.Len() && b.Len() <= n && b.PeakLen() <= n, "step %d bounds", i)
		ok(t, (b.Len() == 0) == b.Empty() && (b.Len() == n) == b.Full() && !(b.Empty() && b.Full()), "step %d collision", i)
	}
	ok(t, b.PeakLen() == n, "peak=%d want %d", b.PeakLen(), n)
}

func TestBrokenSacrificeSlotFailsAtFourth(t *testing.T) {
	// 内联方案①「牺牲一槽位」：(w+1)%cap==r 判满，最多存 cap-1。
	r, w := 0, 0
	for i := 1; i <= 4; i++ {
		full := (w+1)%4 == r // 第 4 次：w=0、r=0，误判为满
		ok(t, full == (i == 4), "step %d full=%v", i, full)
		if !full {
			w = (w + 1) % 4
		}
	}
}

func TestConcurrent(t *testing.T) {
	for _, c := range []struct {
		name string
		pn   int
	}{{"SPSC", 1}, {"MPSC", 4}} {
		t.Run(c.name, func(t *testing.T) {
			const per, n = 1000, 8
			b, _ := ring.New[int](n)
			seen := make([]int, c.pn)
			var wg sync.WaitGroup
			wg.Add(c.pn + 1)
			for p := 0; p < c.pn; p++ {
				go func(p int) {
					defer wg.Done()
					for i := 0; i < per; i++ {
						for b.Enqueue(p*per+i) != nil {
						}
					}
				}(p)
			}
			go func() {
				defer wg.Done()
				for k := 0; k < c.pn*per; k++ {
					v, has := b.Dequeue()
					for !has {
						v, has = b.Dequeue()
					}
					p, i := v/per, v%per
					if i != seen[p] {
						t.Errorf("p%d reorder %d->%d", p, seen[p], i)
					}
					seen[p]++
				}
			}()
			wg.Wait()
			ok(t, b.Empty() && b.Len() == 0, "len=%d want empty", b.Len())
		})
	}
}
