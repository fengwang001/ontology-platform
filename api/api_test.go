package api_test

import (
	"math/rand"
	"sync"
	"testing"

	"ontology/api"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// 不变量1+2：确定性伪随机 Commit/Apply/Read 序列下，Downgrade 读恒等于朴素参照。
func TestDowngradeMatchesNaive(t *testing.T) {
	for seed := int64(0); seed < 20; seed++ {
		g := api.New()
		rng := rand.New(rand.NewSource(seed))
		h, a, next := int64(-1), int64(-1), int64(0) // 朴素参照
		for step := 0; step < 300; step++ {
			switch rng.Intn(3) {
			case 0:
				must(t, g.Commit(next))
				next++
				h++
			case 1:
				if h > a {
					n := a + 1 + rng.Int63n(h-a)
					must(t, g.Apply(n))
					a = n
				}
			default:
				lag := rng.Int63n(5)
				res, err := g.Read(lag, api.Downgrade)
				if err != nil || res.Pos != a || res.Downgraded != (a < h-lag) {
					t.Fatalf("seed=%d step=%d: got %+v err=%v, want pos=%d down=%v",
						seed, step, res, err, a, a < h-lag)
				}
			}
			if g.Head() != h || g.Applied() != a || a > h {
				t.Fatalf("seed=%d step=%d: 位点偏离参照 H=%d A=%d", seed, step, h, a)
			}
		}
	}
}

// 不变量2：H 严格 +1，A 单调不减且恒有 A<=H。
func TestMonotonic(t *testing.T) {
	g := api.New()
	prevH, prevA := g.Head(), g.Applied()
	for i := int64(0); i < 500; i++ {
		must(t, g.Commit(i))
		if g.Head() != prevH+1 {
			t.Fatalf("i=%d: Commit 未使 H 严格 +1", i)
		}
		prevH = g.Head()
		if i%2 == 0 {
			must(t, g.Apply(i))
		}
		if g.Applied() < prevA || g.Applied() > g.Head() {
			t.Fatalf("i=%d: A 非单调或越过 H", i)
		}
		prevA = g.Applied()
	}
}

// 不变量3：Block 返回后 Pos == Applied() 且 >= 调用时刻冻结的 T。
func TestBlockNotStale(t *testing.T) {
	for _, lag := range []int64{0, 1, 3} {
		g := api.New()
		for i := int64(0); i <= 5; i++ {
			must(t, g.Commit(i))
		}
		target := g.Head() - lag // 调用时刻冻结的 T
		done := make(chan api.Result, 1)
		go func() { res, _ := g.Read(lag, api.Block); done <- res }()
		for n := int64(0); n <= target; n++ {
			must(t, g.Apply(n))
		}
		if res := <-done; res.Pos != g.Applied() || res.Pos < target || res.Downgraded {
			t.Fatalf("lag=%d: got %+v, want Pos>=%d 且等于 Applied", lag, res, target)
		}
	}
}

// 不变量4：被拒操作不改变状态、给出可判定且互不相同的错误，之后仍可正常使用。
func TestFailureNoTrace(t *testing.T) {
	cases := []struct {
		name string
		op   func(g *api.Engine) error
		want error
	}{
		{"commit-gap", func(g *api.Engine) error { return g.Commit(7) }, api.ErrCommitGap},
		{"apply-low", func(g *api.Engine) error { return g.Apply(-1) }, api.ErrApplyRange},
		{"apply-high", func(g *api.Engine) error { return g.Apply(9) }, api.ErrApplyRange},
		{"neg-lag", func(g *api.Engine) error { _, e := g.Read(-1, api.Downgrade); return e }, api.ErrNegativeLag},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := api.New()
			must(t, g.Commit(0))
			if err := c.op(g); err != c.want {
				t.Fatalf("err=%v, want %v", err, c.want)
			}
			if g.Head() != 0 || g.Applied() != -1 {
				t.Fatal("被拒操作改变了状态")
			}
			if err := g.Commit(1); err != nil || g.Head() != 1 {
				t.Fatal("被拒后无法继续正常使用")
			}
		})
	}
	if api.ErrCommitGap == api.ErrApplyRange || api.ErrApplyRange == api.ErrNegativeLag ||
		api.ErrCommitGap == api.ErrNegativeLag {
		t.Fatal("三类哨兵错误不互不相同")
	}
}

// 并发只读：N 个 goroutine 读同一实例，Head/Applied 逐字段相同；SelfCheck 并发安全。
func TestConcurrentReadsConsistent(t *testing.T) {
	g := api.New()
	for i := int64(0); i < 50; i++ {
		must(t, g.Commit(i))
	}
	must(t, g.Apply(30))
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if g.Head() != 49 || g.Applied() != 30 {
				t.Error("并发只读结果不一致")
			}
			if err := g.SelfCheck(); err != nil {
				t.Error("并发 SelfCheck 失败:", err)
			}
		}()
	}
	wg.Wait()
}
