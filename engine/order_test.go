package engine

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"

	"ontology/model"
)

// stateKey 完整刻画朴素模拟器状态，用于全顺序 DFS 去重。
func (m *naive) stateKey() string {
	s := string(m.status()) + fmt.Sprint(m.act, m.endCount)
	for v := 1; v <= m.n; v++ {
		if m.arr[v] != nil {
			s += fmt.Sprint(v, m.arr[v], m.fires[v])
		}
	}
	return s
}

// allTerminalKeys 从初始状态枚举所有合法完成顺序，返回各叶子终态 key 的集合。
func allTerminalKeys(g *model.Graph, ch model.Choice) map[string]int {
	root := newNaive(g, ch)
	seen := map[string]bool{}
	ends := map[string]int{}
	var dfs func(m *naive)
	dfs = func(m *naive) {
		key := m.stateKey()
		if seen[key] {
			return
		}
		seen[key] = true
		var active []int
		for v := 1; v <= m.n; v++ {
			if m.kinds[v] == model.Task && m.act[v] > 0 {
				active = append(active, v)
			}
		}
		if len(active) == 0 {
			ends[m.terminalKey()]++
			return
		}
		for _, t := range active {
			next := cloneNaive(m)
			next.complete(t)
			dfs(next)
		}
	}
	dfs(root)
	return ends
}

func cloneNaive(m *naive) *naive {
	c := &naive{
		n: m.n, kinds: m.kinds, out: m.out, in: m.in,
		act:      append([]int(nil), m.act...),
		arr:      make([][]int, m.n+1),
		fires:    append([]int(nil), m.fires...),
		endCount: m.endCount,
	}
	for v := range m.arr {
		if m.arr[v] != nil {
			c.arr[v] = append([]int(nil), m.arr[v]...)
		}
	}
	return c
}

// 任务数 <=5 的随机图：枚举全部完成顺序，终态唯一；引擎重放同一终态。
func TestOrderIndependent(t *testing.T) {
	rng := rand.New(rand.NewSource(77))
	cases := 0
	for cases < 120 {
		g := genGraph(rng)
		if len(tasks(g)) > 5 {
			continue
		}
		cases++
		ch := randChoice(rng, g)
		ends := allTerminalKeys(g, ch)
		if len(ends) != 1 {
			t.Fatalf("order-dependent terminal (%d distinct)\n%s\nch=%v", len(ends), dump(g), ch)
		}
		// 引擎按一个随机合法顺序重放，终态必须一致。
		nav := newNaive(g, ch)
		eng := New(g, ch)
		for {
			var active []int
			for _, x := range tasks(g) {
				if nav.act[x] > 0 {
					active = append(active, x)
				}
			}
			if len(active) == 0 {
				break
			}
			x := active[rng.Intn(len(active))]
			nav.complete(x)
			if err := eng.Complete(x); err != nil {
				t.Fatal(err)
			}
		}
		if !equalTerminal(eng, nav) {
			t.Fatalf("engine replay mismatch\n%s\nch=%v", dump(g), ch)
		}
		t.Logf("case=%d n=%d tasks=%d terminal=%s end=%d basis=exhaustive DFS over all task orders",
			cases, g.N, len(tasks(g)), eng.Status().Status, eng.Status().EndCount)
	}
}

// OrJoin 判定考察节点数与有令牌节点数同阶：n=10 与 n=64 两档同图骨架同结果。
func TestConcurrentComplete(t *testing.T) {
	g := mkGraph(6,
		[]model.NodeType{0, model.Start, model.AndSplit, model.Task, model.Task, model.AndJoin, model.End},
		model.Edge{U: 1, V: 2}, model.Edge{U: 2, V: 3}, model.Edge{U: 2, V: 4},
		model.Edge{U: 3, V: 5}, model.Edge{U: 4, V: 5}, model.Edge{U: 5, V: 6})
	g2 := g
	for trial := 0; trial < 20; trial++ {
		in := mustStart(t, g2, model.Choice{})
		var wg sync.WaitGroup
		errs := make(chan error, 8)
		for w := 0; w < 4; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for _, task := range []int{3, 4} {
					if err := in.Complete(task); err != nil && err != ErrNoActive {
						errs <- err
					}
				}
			}()
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Fatal(err)
		}
		st := in.Status()
		if st.Status != Completed || st.EndCount != 1 || firesAt(st, 5) != 1 {
			t.Fatalf("concurrent terminal: %+v", st)
		}
	}
}
