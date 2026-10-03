package cfs

import (
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
)

// applyScript 把一段操作脚本应用到控制器。
func applyScript(c *Controller, script []func(*Controller)) {
	for _, op := range script {
		op(c)
	}
}

// observable 收集控制器的全部可观察状态。
func observable(c *Controller, ncpu int) (int64, int64, int64, int64, []cpu, []int) {
	p, nth, tt := c.Stats()
	g := c.Pool()
	cpus := make([]cpu, ncpu)
	for i := 0; i < ncpu; i++ {
		st, l, since := c.State(i)
		cpus[i] = cpu{state: st, l: l, since: since}
	}
	return p, nth, tt, g, cpus, c.Queue()
}

// TestReplayDeterminism 相同的操作序列重放得到完全相同的状态与统计。
func TestReplayDeterminism(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	const ncpu = 4
	var script []func(*Controller)
	now := int64(0)
	for i := 0; i < 500; i++ {
		now += rng.Int63n(15)
		n, id, d := now, rng.Intn(ncpu), 1+rng.Int63n(20)
		switch rng.Intn(3) {
		case 0:
			script = append(script, func(c *Controller) { c.Wake(n, id) })
		case 1:
			script = append(script, func(c *Controller) { c.Run(n, id, d) })
		default:
			script = append(script, func(c *Controller) { c.Idle(n, id) })
		}
	}
	c1 := mustNew(t, 15, 10, 4, 3, ncpu)
	c2 := mustNew(t, 15, 10, 4, 3, ncpu)
	applyScript(c1, script)
	applyScript(c2, script)
	p1, n1, t1, g1, cpus1, q1 := observable(c1, ncpu)
	p2, n2, t2, g2, cpus2, q2 := observable(c2, ncpu)
	if p1 != p2 || n1 != n2 || t1 != t2 || g1 != g2 {
		t.Fatalf("重放统计不一致: (%d,%d,%d,%d) vs (%d,%d,%d,%d)", p1, n1, t1, g1, p2, n2, t2, g2)
	}
	for i := range cpus1 {
		if cpus1[i] != cpus2[i] {
			t.Fatalf("重放 cpu%d 状态不一致: %+v vs %+v", i, cpus1[i], cpus2[i])
		}
	}
	if len(q1) != len(q2) {
		t.Fatalf("重放队列长度不一致: %v vs %v", q1, q2)
	}
	for i := range q1 {
		if q1[i] != q2[i] {
			t.Fatalf("重放队列不一致: %v vs %v", q1, q2)
		}
	}
}

// TestConcurrent 并发调用所有操作与查询：结果等价于某个串行顺序，
// 不变量在任何交错下都成立（配合 -race 使用）。
func TestConcurrent(t *testing.T) {
	const ncpu = 8
	c := mustNew(t, 50, 10, 8, 5, ncpu)
	var now atomic.Int64
	var wg sync.WaitGroup
	for g := 0; g < ncpu; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(id)))
			for i := 0; i < 300; i++ {
				n := now.Add(1) - 1 // 全局单调，但加锁顺序不定，允许回退拒绝
				switch rng.Intn(6) {
				case 0:
					c.Wake(n, id)
				case 1, 2, 3:
					c.Run(n, id, 1+rng.Int63n(20))
				case 4:
					c.Idle(n, id)
				default:
					c.Stats()
					c.State(id)
					c.Pool()
					c.Queue()
				}
			}
		}(g)
	}
	wg.Wait()

	// 不变量：G 在 [0, Q+B]；队列与节流集合一致且无重复
	if g := c.Pool(); g < 0 || g > 55 {
		t.Fatalf("G=%d 越出 [0,55]", g)
	}
	inQueue := make(map[int]bool)
	for _, id := range c.Queue() {
		if inQueue[id] {
			t.Fatalf("cpu%d 在队列中重复", id)
		}
		inQueue[id] = true
		st, l, _ := c.State(id)
		if st != Throttled {
			t.Fatalf("队列中 cpu%d 状态为 %v", id, st)
		}
		if l > 0 {
			t.Fatalf("队列中 cpu%d l=%d>0", id, l)
		}
	}
	for i := 0; i < ncpu; i++ {
		st, l, _ := c.State(i)
		if st == Throttled && !inQueue[i] {
			t.Fatalf("节流 cpu%d 不在队列", i)
		}
		if l > 8 {
			t.Fatalf("cpu%d l=%d 超过 S=8", i, l)
		}
	}
}
