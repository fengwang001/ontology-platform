package api_test

import (
	"errors"
	"math/rand"
	"ontology/api"
	"slices"
	"sync"
	"testing"
)

// oracle 朴素批量模型（线性扫窗口），独立实现用于对拍。
type oracle struct {
	vals map[string]map[int64]int64
	win  map[string][]int64
}

func (o *oracle) apply(k string, s, v int64, w int) {
	if o.vals[k] == nil {
		o.vals[k] = map[int64]int64{}
	}
	m := o.vals[k]
	if c, seen := m[s]; seen {
		if c != v && slices.Contains(o.win[k], s) {
			m[s] = v
		}
		return
	}
	m[s] = v
	o.win[k] = append(o.win[k], s)
	if len(o.win[k]) > w {
		o.win[k] = o.win[k][1:]
	}
}

func failIf(t *testing.T, bad bool, f string, a ...any) {
	t.Helper()
	if bad {
		t.Errorf(f, a...) // 用 Errorf：可在并发 reader goroutine 中安全调用
	}
}

// TestBatchEquivalence 不变量 1：多档规模、随机乱序与纠正，View 对拍朴素批量。
func TestBatchEquivalence(t *testing.T) {
	for _, c := range []struct{ n, w, nk, sp int }{
		{50, 3, 2, 20}, {500, 5, 4, 100}, {2000, 8, 8, 300}, {5000, 16, 4, 1000}} {
		a, _ := api.New(c.w)
		o := &oracle{map[string]map[int64]int64{}, map[string][]int64{}}
		rng := rand.New(rand.NewSource(int64(c.n)))
		for i := 0; i < c.n; i++ {
			k := string(rune('a' + rng.Intn(c.nk)))
			s, v := int64(1+rng.Intn(c.sp)), int64(rng.Intn(2001)-1000)
			a.Apply(k, s, v)
			o.apply(k, s, v, c.w)
		}
		for k, m := range o.vals {
			var want int64
			for _, v := range m {
				want += v
			}
			failIf(t, a.View()[k] != want, "c=%+v key=%q %d!=%d", c, k, a.View()[k], want)
		}
	}
}

// TestChangelogPrefix 不变量 2：每次产出立即重放，每个前缀撤回必恰为当前值。
func TestChangelogPrefix(t *testing.T) {
	a, _ := api.New(3)
	rng := rand.New(rand.NewSource(7))
	cur := map[string]int64{}
	for i := 0; i < 300; i++ {
		cs, _, _ := a.Apply("k", int64(1+rng.Intn(50)), int64(rng.Intn(100)))
		for _, c := range cs {
			v, ok := cur[c.Key]
			bad := c.Plus && ok || !c.Plus && (!ok || v != c.Sum)
			failIf(t, bad, "prefix %d 不自洽 plus=%v ok=%v v=%d", i, c.Plus, ok, v)
			if c.Plus {
				cur[c.Key] = c.Sum
			} else {
				delete(cur, c.Key)
			}
		}
	}
}

// TestIdempotent 不变量 3：重复投递同 Seq 同 Val，一切不变。
func TestIdempotent(t *testing.T) {
	a, _ := api.New(4)
	for _, e := range [][2]int64{{1, 10}, {2, 20}, {3, 30}, {2, 25}} {
		a.Apply("k", e[0], e[1])
	}
	v0, s0 := a.View()["k"], a.Stale()
	for _, e := range [][2]int64{{1, 10}, {2, 25}, {3, 30}, {1, 10}} {
		cs, st, _ := a.Apply("k", e[0], e[1])
		failIf(t, st != api.StatusDuplicate || len(cs) != 0, "dup %v st=%v n=%d", e, st, len(cs))
	}
	failIf(t, a.View()["k"] != v0 || a.Stale() != s0, "重复投递改变了状态")
}

// TestRejectNoTrace 不变量 4：三类错误互不相同且被拒后不留痕、可继续用。
func TestRejectNoTrace(t *testing.T) {
	a, _ := api.New(2)
	a.Apply("k", 1, 5)
	v0, s0 := a.View()["k"], a.Stale()
	wants := []error{api.ErrEmptyKey, api.ErrBadSeq, api.ErrBadWindow}
	fs := []func() error{
		func() error { _, _, e := a.Apply("", 1, 1); return e },
		func() error { _, _, e := a.Apply("k", 0, 1); return e },
		func() error { _, e := api.New(0); return e },
	}
	for i, f := range fs {
		failIf(t, !errors.Is(f(), wants[i]), "第 %d 类错误未判定", i+1)
	}
	diff := errors.Is(wants[0], wants[1]) || errors.Is(wants[1], wants[2]) || errors.Is(wants[0], wants[2])
	failIf(t, diff, "三类错误必须互不相同")
	failIf(t, a.View()["k"] != v0 || a.Stale() != s0, "被拒操作留下了痕迹")
	cs, st, _ := a.Apply("k", 2, 7) // 被拒后仍正常：普通新事件，-5,+12
	failIf(t, st != api.StatusNew || a.View()["k"] != 12 || len(cs) != 2 || cs[0].Plus,
		"被拒后无法继续正常使用")
}

// TestConcurrentApply N goroutine 各发不同 Seq，和精确；并发读可解释，-race 干净。
func TestConcurrentApply(t *testing.T) {
	const n = 128
	a, _ := api.New(n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(n + 1)
	for i := 1; i <= n; i++ {
		go func(i int) { defer wg.Done(); <-start; a.Apply("k", int64(i), int64(i)) }(i)
	}
	go func() { // 并发只读：部分和必为已应用子集之和，落在 [0,total]，不用 sleep
		defer wg.Done()
		<-start
		total := int64(n * (n + 1) / 2)
		for i := 0; i < 20000; i++ {
			s := a.View()["k"]
			failIf(t, s < 0 || s > total, "读到不可解释的和 %d", s)
			a.Stale()
			a.SelfCheck()
		}
	}()
	close(start)
	wg.Wait()
	failIf(t, a.View()["k"] != int64(n*(n+1)/2), "got %d want %d", a.View()["k"], n*(n+1)/2)
}
