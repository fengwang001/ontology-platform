package engine

import (
	"math/rand"
	"testing"

	"ontology/model"
)

// genGraph 生成一张满足全部结构约束的随机无环图（边只从小号指向大号）。
func genGraph(rng *rand.Rand) *model.Graph {
	for {
		n := 4 + rng.Intn(8) // 4..11，保证任务数较小便于全排列
		kinds := make([]model.NodeType, n+1)
		kinds[1] = model.Start
		kinds[n] = model.End
		for v := 2; v < n; v++ {
			switch rng.Intn(10) {
			case 0, 1, 2, 3:
				kinds[v] = model.Task
			case 4:
				kinds[v] = model.AndSplit
			case 5, 6:
				kinds[v] = model.XorSplit
			case 7:
				kinds[v] = model.OrSplit
			case 8:
				kinds[v] = model.AndJoin
			case 9:
				kinds[v] = model.OrJoin
			}
		}
		if g := buildEdges(rng, n, kinds); g != nil {
			return g
		}
	}
}

// buildEdges 按各节点度约束前向连边；失败返回 nil。
func buildEdges(rng *rand.Rand, n int, kinds []model.NodeType) *model.Graph {
	indeg := make([]int, n+1)
	outdeg := make([]int, n+1)
	var edges []model.Edge
	add := func(u, v int) bool {
		for _, e := range edges {
			if e.U == u && e.V == v {
				return false
			}
		}
		edges = append(edges, model.Edge{U: u, V: v})
		indeg[v]++
		outdeg[u]++
		return true
	}
	capOf := func(u int) int {
		if kinds[u] == model.AndSplit || kinds[u] == model.XorSplit || kinds[u] == model.OrSplit {
			return 3
		}
		return 1
	}
	// 从后往前确定每个节点的出边。
	for u := 1; u < n; u++ {
		need := 1
		switch kinds[u] {
		case model.AndSplit, model.XorSplit, model.OrSplit:
			need = 2 + rng.Intn(2)
		case model.Task, model.Start:
			need = 1
		case model.AndJoin, model.OrJoin:
			need = 1
		}
		// 汇合不能作为另一汇合的目标来凑其多入边时保持简单：目标候选排除分叉。
		cand := []int{}
		for v := u + 1; v <= n; v++ {
			if kinds[v] == model.AndSplit || kinds[v] == model.XorSplit || kinds[v] == model.OrSplit {
				continue
			}
			cand = append(cand, v)
		}
		if len(cand) < need {
			return nil
		}
		rng.Shuffle(len(cand), func(i, j int) { cand[i], cand[j] = cand[j], cand[i] })
		for i := 0; i < need; i++ {
			if !add(u, cand[i]) {
				return nil
			}
		}
	}
	// 修补入度不足：Task/End>=1，汇合>=2；目标仍排除分叉（分叉恰 1 入边）。
	for v := 2; v <= n; v++ {
		want := 1
		if kinds[v] == model.AndJoin || kinds[v] == model.OrJoin {
			want = 2
		}
		try := 0
		for indeg[v] < want {
			try++
			if try > 100 {
				return nil
			}
			var pool []int
			for x := 1; x < v; x++ {
				if kinds[x] != model.End && outdeg[x] < capOf(x) {
					pool = append(pool, x)
				}
			}
			if len(pool) == 0 {
				return nil
			}
			u := pool[rng.Intn(len(pool))]
			if !add(u, v) {
				continue
			}
		}
	}
	g := &model.Graph{N: n, Kinds: kinds, Edges: edges}
	return g
}

func randChoice(rng *rand.Rand, g *model.Graph) model.Choice {
	ch := model.Choice{}
	out := g.Out()
	for v := 1; v <= g.N; v++ {
		switch g.Kinds[v] {
		case model.XorSplit:
			ch[v] = []int{rng.Intn(len(out[v]))}
		case model.OrSplit:
			m := len(out[v])
			size := 1 + rng.Intn(m)
			perm := rng.Perm(m)[:size]
			ch[v] = perm
		}
	}
	return ch
}

func TestRandomAgainstNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(20261003))
	for iter := 0; iter < 300; iter++ {
		g := genGraph(rng)
		if err := g.Validate(); err != nil {
			t.Fatalf("generator produced invalid graph: %v\n%s", err, dump(g))
		}
		ch := randChoice(rng, g)
		if err := g.ValidateChoice(ch); err != nil {
			t.Fatalf("bad choice: %v", err)
		}
		eng := New(g, ch)
		nav := newNaive(g, ch)
		if !equalTerminal(eng, nav) {
			t.Fatalf("initial mismatch\n%s\nchoice=%v", dump(g), ch)
		}
		ts := tasks(g)
		// 随机完成序列，逐步对照。
		for step := 0; step < 30; step++ {
			active := []int{}
			for _, x := range ts {
				if nav.act[x] > 0 {
					active = append(active, x)
				}
			}
			if len(active) == 0 {
				break
			}
			x := active[rng.Intn(len(active))]
			if !nav.complete(x) {
				t.Fatalf("naive complete %d failed", x)
			}
			if err := eng.Complete(x); err != nil {
				t.Fatalf("engine complete %d: %v", x, err)
			}
			if !equalTerminal(eng, nav) {
				t.Fatalf("diverged after %d\n%s\nchoice=%v\neng=%+v", x, dump(g), ch, eng.Status())
			}
		}
		t.Logf("iter=%d input=n=%d edges=%d choice=%v output=%s end=%d basis=naive recomputes reach each step",
			iter, g.N, len(g.Edges), ch, eng.Status().Status, eng.Status().EndCount)
	}
}
