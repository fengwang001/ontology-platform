package check

import (
	"errors"
	"math/bits"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/aging"
	"ontology/clock"
)

func newPair(t *testing.T, clk *clock.Clock, k, m int) (*aging.Queue, *Ref) {
	t.Helper()
	q, e1 := aging.New(clk, k, m)
	r, e2 := New(clk, k, m)
	if (e1 == nil) != (e2 == nil) {
		t.Fatalf("new err diverge %v %v", e1, e2)
	}
	return q, r
}

// run 在 q 与朴素参照 r 上执行同一步骤，结果不同即失败。
func run(t *testing.T, q *aging.Queue, r *Ref, k byte, id, b int, clk *clock.Clock,
	adv int64, want error) {
	t.Helper()
	var e1, e2 error
	switch k {
	case 'p':
		e1, e2 = q.Push(id, b), r.Push(id, b)
	case 'x':
		e1, e2 = q.Remove(id), r.Remove(id)
	case 'o':
		i1, ok1 := q.Pop()
		i2, ok2 := r.Pop()
		if i1 != id || ok1 != true || i2 != id || ok2 != true {
			t.Fatalf("pop want %d got (%d,%v)(%d,%v)", id, i1, ok1, i2, ok2)
		}
	case 'e': // 空 Pop
		if _, ok := q.Pop(); ok {
			t.Fatal("empty pop must fail")
		}
		if _, ok := r.Pop(); ok {
			t.Fatal("ref empty pop must fail")
		}
	case 'a':
		_, e1 = clk.Advance(adv)
		_, e2 = r.Advance(adv)
	}
	ok := func(e, w error) bool { return (w == nil && e == nil) || (w != nil && errors.Is(e, w)) }
	if !ok(e1, want) || !ok(e2, want) {
		t.Fatalf("step %c err q=%v r=%v want=%v", k, e1, e2, want)
	}
	if q.Len() != r.Len() {
		t.Fatalf("len diverge %d %d", q.Len(), r.Len())
	}
}

// TestSemantics 表驱动钉住第二节六条语义与第五节故障注入（逐步与参照一致）。
func TestSemantics(t *testing.T) {
	type step struct {
		k          byte
		id, b      int
		adv        int64
		newK, newM int
		want       error
	}
	cases := []struct {
		name  string
		first step
		steps []step
	}{
		{"order aging tie id", step{}, []step{
			{'p', 1, 0, 0, 1, 0, nil}, {'p', 2, 0, 0, 0, 0, nil},
			{'p', 3, 5, 0, 0, 0, nil}, {'a', 0, 0, 10, 0, 0, nil},
			{'o', 3, 0, 0, 0, 0, nil}, {'o', 1, 0, 0, 0, 0, nil},
			{'o', 2, 0, 0, 0, 0, nil}, {'e', 0, 0, 0, 0, 0, nil}}},
		{"duplicate", step{}, []step{
			{'p', 1, 0, 0, 0, 0, nil}, {'p', 1, 0, 0, 0, 0, aging.ErrDuplicate}}},
		{"remove gone/popped", step{}, []step{
			{'p', 1, 0, 0, 0, 0, nil}, {'o', 1, 0, 0, 0, 0, nil},
			{'x', 1, 0, 0, 0, 0, aging.ErrNotFound},
			{'x', 9, 0, 0, 0, 0, aging.ErrNotFound}}},
		{"full zero side effect", step{0, 0, 0, 0, 1, 1, nil}, []step{
			{'p', 1, 0, 0, 0, 0, nil}, {'p', 2, 0, 0, 0, 0, aging.ErrFull},
			{'x', 2, 0, 0, 0, 0, aging.ErrNotFound}, {'o', 1, 0, 0, 0, 0, nil}}},
		{"clock backward keeps state", step{}, []step{
			{'p', 1, 0, 0, 0, 0, nil}, {'a', 0, 0, -1, 0, 0, clock.ErrClockBackward},
			{'o', 1, 0, 0, 0, 0, nil}}},
		{"K<=0 at construct", step{'n', 0, 0, 0, 0, 0, aging.ErrRange}, nil},
		{"base out of range", step{}, []step{
			{'p', 1, 1 << 62, 0, 0, 0, aging.ErrRange}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clk := clock.New(0)
			q, r := newPair(t, clk, 1, 0)
			if tc.first.k == 'n' {
				if _, err := aging.New(clk, 0, 0); !errors.Is(err, tc.first.want) {
					t.Fatalf("want %v got %v", tc.first.want, err)
				}
				return
			}
			if tc.first.newM == 1 {
				q, r = newPair(t, clk, 1, 1)
			}
			for _, s := range tc.steps {
				run(t, q, r, s.k, s.id, s.b, clk, s.adv, s.want)
			}
		})
	}
}

// TestDifferential：固定种子 2000 步混合操作，每步与朴素参照相同（语义2）。
func TestDifferential(t *testing.T) {
	clk := clock.New(0)
	q, r := newPair(t, clk, 3, 0)
	rng := rand.New(rand.NewPCG(20260924, 7))
	active, next := map[int]bool{}, 0
	for step := 0; step < 2000; step++ {
		switch rng.IntN(5) {
		case 0, 1:
			id := next
			next++
			base := rng.IntN(9) - 4
			if e1, e2 := q.Push(id, base), r.Push(id, base); (e1 == nil) != (e2 == nil) {
				t.Fatalf("push diverge %v %v", e1, e2)
			} else if e1 == nil {
				active[id] = true
			}
		case 2:
			i1, ok1 := q.Pop()
			i2, ok2 := r.Pop()
			if i1 != i2 || ok1 != ok2 {
				t.Fatalf("step %d pop diverge (%d,%v)(%d,%v)", step, i1, ok1, i2, ok2)
			}
			if ok1 {
				delete(active, i1)
			}
		case 3:
			for id := range active {
				if e1, e2 := q.Remove(id), r.Remove(id); (e1 == nil) != (e2 == nil) {
					t.Fatal("remove diverge")
				}
				delete(active, id)
				break
			}
		case 4:
			if _, err := clk.Advance(int64(rng.IntN(5))); err != nil {
				t.Fatal(err)
			}
		}
		if q.Len() != r.Len() {
			t.Fatalf("step %d len diverge", step)
		}
	}
}

// TestKeyDerivation 钉死第三节：用 t-K*base 键；并列须 t 早者先出。
// 反例（now=1,K=1）：id1 base=2^53+1 在 t=0 入队；id2 base=2^53+2 在 t=1 入队。
// 真值 EP1=EP2=2^53+1，t1 更早应先出；float64 把 2^53+1 舍成 2^53，
// 误判 EP1=2^53 < EP2=2^53+2，让新任务 2 排前。错误实现必失败。
func TestKeyDerivation(t *testing.T) {
	clk := clock.New(0)
	q, r := newPair(t, clk, 1, 0)
	const p = 1 << 53
	if e1, e2 := q.Push(1, p+1), r.Push(1, p+1); e1 != nil || e2 != nil {
		t.Fatal(e1, e2)
	}
	clk.Advance(1)
	if e1, e2 := q.Push(2, p+2), r.Push(2, p+2); e1 != nil || e2 != nil {
		t.Fatal(e1, e2)
	}
	i1, ok1 := q.Pop()
	i2, ok2 := r.Pop()
	if !ok1 || !ok2 || i1 != 1 || i2 != 1 {
		t.Fatalf("true tie must favor older: q=%d r=%d", i1, i2)
	}
}

// TestStarvation 钉死语义3：高任务每刻涌入时 L 等待恰好 K*Δ 刻。
func TestStarvation(t *testing.T) {
	for _, c := range []struct{ K, D int }{{1, 1}, {2, 2}, {2, 3}, {3, 5}, {5, 1}} {
		clk := clock.New(0)
		q, r := newPair(t, clk, c.K, 0)
		run(t, q, r, 'p', 0, 0, clk, 0, nil)
		bound := c.K * c.D
		for s := 0; s < bound; s++ {
			run(t, q, r, 'p', s+1, c.D, clk, 0, nil)
			run(t, q, r, 'o', s+1, 0, clk, 0, nil)
			clk.Advance(1)
		}
		run(t, q, r, 'p', 99, c.D, clk, 0, nil)
		run(t, q, r, 'o', 0, 0, clk, 0, nil) // 并列时 L 必须此刻先出
	}
}

// TestComplexity 钉死第四节：两档规模计数器上界 + 推进时钟零重排。
func TestComplexity(t *testing.T) {
	for _, n := range []int{100, 100000} {
		clk := clock.New(0)
		q, _ := newPair(t, clk, 1, 0)
		for i := 0; i < n; i++ {
			if err := q.Push(i, (i*2654435761)%97); err != nil {
				t.Fatal(err)
			}
		}
		q.ResetCmpCount()
		if _, ok := q.Pop(); !ok {
			t.Fatal("pop")
		}
		got, bound := q.CmpCount(), uint64(2*bits.Len(uint(n-1))+2)
		if got > bound {
			t.Fatalf("n=%d cmps=%d > bound=%d", n, got, bound)
		}
		t.Logf("n=%d pop cmps=%d bound=%d", n, got, bound)
	}
	clk := clock.New(0)
	q, _ := newPair(t, clk, 7, 0)
	for i := 0; i < 1000; i++ {
		q.Push(i, i%11)
	}
	clk.Advance(1)
	q.ResetCmpCount()
	q.Pop()
	one := q.CmpCount()
	clk.Advance(999999)
	q.ResetCmpCount()
	q.Pop()
	if one != q.CmpCount() {
		t.Fatalf("cmps after +1=%d vs +1e6=%d", one, q.CmpCount())
	}
}

// TestConcurrent：16 goroutine + 时钟推进，-race 干净，id 恰好终结一次。
func TestConcurrent(t *testing.T) {
	clk := clock.New(0)
	q, _ := newPair(t, clk, 5, 0)
	const G, per = 16, 64
	var done [G * per]atomic.Bool
	mark := func(id int) {
		if !done[id].CompareAndSwap(false, true) {
			t.Errorf("id %d finalized twice", id)
		}
	}
	var wg sync.WaitGroup
	for g := 0; g < G; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			base := g * per
			for i := 0; i < per; i++ {
				if err := q.Push(base+i, g); err != nil {
					t.Error(err)
					return
				}
			}
			for i := 0; i < per; i++ {
				if g%2 == 0 {
					if id, ok := q.Pop(); ok {
						mark(id)
					}
				} else if err := q.Remove(base + i); err == nil {
					mark(base + i)
				}
			}
		}(g)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			clk.Advance(1)
		}
	}()
	wg.Wait()
	for {
		id, ok := q.Pop()
		if !ok {
			break
		}
		mark(id)
	}
	for i := range done {
		if !done[i].Load() {
			t.Fatalf("id %d never finalized", i)
		}
	}
	if q.Len() != 0 {
		t.Fatalf("leftover %d", q.Len())
	}
}
