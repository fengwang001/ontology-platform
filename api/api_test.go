package api_test

import (
	"errors"
	"math/rand"
	"testing"

	"ontology/api"
)

func mustAcquire(t *testing.T, p *api.Pool) api.Block {
	t.Helper()
	b, err := p.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// 第三节八步序列：逐步钉住返回块、Idle/Total、LIFO、满池驱逐。
func TestEightStepSequence(t *testing.T) {
	p, err := api.New(2)
	if err != nil {
		t.Fatal(err)
	}
	var b [3]api.Block
	for i := range b {
		b[i] = mustAcquire(t, p)
		if p.Idle() != 0 || p.Total() != i+1 {
			t.Fatalf("step %d: Idle/Total=%d/%d", i+1, p.Idle(), p.Total())
		}
	}
	want := [][2]int{{1, 3}, {2, 3}, {2, 2}} // 第 6 步满池驱逐 b2
	for i, bb := range b {
		if err := p.Release(bb); err != nil {
			t.Fatal(err)
		}
		if p.Idle() != want[i][0] || p.Total() != want[i][1] {
			t.Fatalf("step %d: Idle/Total=%d/%d, want %v", i+4, p.Idle(), p.Total(), want[i])
		}
	}
	a7, a8 := mustAcquire(t, p), mustAcquire(t, p)
	if a7 != b[1] || a8 != b[0] { // LIFO：7=b1, 8=b0（FIFO 则为 b0,b1）
		t.Fatalf("LIFO violated: got %v,%v", a7, a8)
	}
	if p.Idle() != 0 || p.Total() != 2 {
		t.Fatalf("step 8: Idle/Total=%d/%d", p.Idle(), p.Total())
	}
}

// 不变量2+3：随机操作序列与朴素 LIFO 参照逐步比对。
func TestNaiveModelRandom(t *testing.T) {
	for _, tc := range []struct{ maxIdle, ops, seed int }{
		{1, 200, 1}, {2, 500, 7}, {5, 2000, 42}, {17, 5000, 99},
	} {
		p, _ := api.New(tc.maxIdle)
		r := rand.New(rand.NewSource(int64(tc.seed)))
		var stack, held []api.Block // stack 是朴素 LIFO 参照
		total := 0
		checkInv := func(i int) {
			t.Helper()
			if p.Idle() != len(stack) || p.Total() != total ||
				len(held)+p.Idle() != p.Total() || p.Idle() > tc.maxIdle {
				t.Fatalf("tc=%+v op %d: 不变量破坏 Idle=%d Total=%d", tc, i, p.Idle(), p.Total())
			}
		}
		for i := 0; i < tc.ops; i++ {
			if len(held) == 0 || r.Intn(2) == 0 {
				got := mustAcquire(t, p)
				if len(stack) > 0 { // 参照非空：必须与栈顶同为一块
					if exp := stack[len(stack)-1]; got != exp {
						t.Fatalf("tc=%+v op %d: LIFO 顺序不一致", tc, i)
					}
					stack = stack[:len(stack)-1]
				} else {
					total++
				}
				held = append(held, got)
			} else {
				j := r.Intn(len(held))
				b := held[j]
				held = append(held[:j], held[j+1:]...)
				if err := p.Release(b); err != nil {
					t.Fatal(err)
				}
				if len(stack) < tc.maxIdle {
					stack = append(stack, b)
				} else {
					total-- // 满池驱逐
				}
			}
			checkInv(i)
		}
	}
}

// 不变量4：三类哨兵错误互不相同，被拒后状态不变且可继续正常使用。
func TestErrorsDistinctAndAtomic(t *testing.T) {
	if _, err := api.New(0); !errors.Is(err, api.ErrInvalidMaxIdle) {
		t.Fatalf("New(0) err=%v", err)
	}
	p, _ := api.New(1)
	a := mustAcquire(t, p)
	_ = p.Release(a)
	i0, t0 := p.Idle(), p.Total()
	cases := []struct {
		op   func() error
		want error
	}{
		{func() error { return p.Release(a) }, api.ErrDoubleRelease},
		{func() error { return p.Release(api.Block{}) }, api.ErrUnknownBlock},
		{func() error { _, e := api.New(-3); return e }, api.ErrInvalidMaxIdle},
	}
	seen := map[error]bool{}
	for _, c := range cases {
		if err := c.op(); !errors.Is(err, c.want) || seen[err] {
			t.Fatalf("err=%v 不可判定或不唯一 (want %v)", err, c.want)
		}
		seen[c.want] = true
		if p.Idle() != i0 || p.Total() != t0 {
			t.Fatal("被拒操作改变了状态")
		}
	}
	if got := mustAcquire(t, p); got != a || p.Idle() != 0 { // 拒绝后仍可用
		t.Fatal("拒绝后池不可用")
	}
}

func TestSelfCheck(t *testing.T) {
	p, err := api.New(2)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}
