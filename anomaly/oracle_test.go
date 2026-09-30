package anomaly

import (
	"bytes"
	"math/rand"
	"reflect"
	"sync"
	"testing"
)

// enumerateSimpleCycles 穷举图中全部带方向的简单环，每个环归一化为
// 「环内最小编号起头、按出现顺序排列」，并记录每条边实际可用的类型。
// 这是测试用参照实现，只跑小规模随机历史。
func enumerateSimpleCycles(g *graph) []struct {
	txns  []int
	edges []EdgeMask
} {
	var out []struct {
		txns  []int
		edges []EdgeMask
	}
	var dfs func(start, u int, path []int, masks []EdgeMask, seen map[int]bool)
	dfs = func(start, u int, path []int, masks []EdgeMask, seen map[int]bool) {
		for _, v := range g.sortedNeighbors(u) {
			if v < start {
				continue
			}
			m := g.adj[u][v]
			if v == start && len(path) >= 2 {
				// 闭合边：穷举每种可用边类型。
				for _, em := range edgeVariants(m) {
					cp := struct {
						txns  []int
						edges []EdgeMask
					}{append([]int(nil), path...), append(append([]EdgeMask(nil), masks...), em)}
					out = append(out, cp)
				}
				continue
			}
			if seen[v] {
				continue
			}
			seen[v] = true
			for _, em := range edgeVariants(m) {
				dfs(start, v, append(path, v), append(masks, em), seen)
			}
			delete(seen, v)
		}
	}
	for _, s := range g.nodes {
		dfs(s, s, []int{s}, nil, map[int]bool{s: true})
	}
	return out
}

func edgeVariants(m EdgeMask) []EdgeMask {
	var res []EdgeMask
	if m&EdgeWW != 0 {
		res = append(res, EdgeWW)
	}
	if m&EdgeWR != 0 {
		res = append(res, EdgeWR)
	}
	if m&EdgeRW != 0 {
		res = append(res, EdgeRW)
	}
	return res
}

func oracleClassify(g *graph, hasG1a, hasG1b bool) (string, *Cycle) {
	cycles := enumerateSimpleCycles(g)
	best := func(cat string, filter func([]EdgeMask) bool) *Cycle {
		var best *Cycle
		for _, cy := range cycles {
			if !filter(cy.edges) {
				continue
			}
			if best == nil || len(cy.txns) < len(best.Txns) ||
				len(cy.txns) == len(best.Txns) && lessSeq(cy.txns, best.Txns) {
				best = &Cycle{Txns: cy.txns, Edges: cy.edges}
			}
		}
		return best
	}
	if w := best("G0", func(es []EdgeMask) bool {
		for _, e := range es {
			if e != EdgeWW {
				return false
			}
		}
		return true
	}); w != nil {
		return CatG0, w
	}
	if hasG1a {
		return CatG1a, nil
	}
	if hasG1b {
		return CatG1b, nil
	}
	if w := best("G1c", func(es []EdgeMask) bool {
		for _, e := range es {
			if e == EdgeRW {
				return false
			}
		}
		return true
	}); w != nil {
		return CatG1c, w
	}
	if w := best("G-single", func(es []EdgeMask) bool {
		n := 0
		for _, e := range es {
			if e == EdgeRW {
				n++
			}
		}
		return n == 1
	}); w != nil {
		return CatGSingle, w
	}
	if w := best("G2", func(es []EdgeMask) bool {
		n := 0
		for _, e := range es {
			if e == EdgeRW {
				n++
			}
		}
		return n >= 2
	}); w != nil {
		return CatG2, w
	}
	return CatNoAnomaly, nil
}

func lessSeq(a, b []int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return len(a) < len(b)
}

// randomValidHistory 生成语义合法的小规模随机历史（3-6 个事务，
// 2-3 个键），并保证版本次序与最后一次写一致。
func randomValidHistory(rng *rand.Rand) History {
	n := 3 + rng.Intn(4)
	keys := []string{"x", "y", "z"}[:2+rng.Intn(2)]
	status := make([]Status, n+1)
	for i := 1; i <= n; i++ {
		if rng.Intn(5) == 0 {
			status[i] = Aborted
		} else {
			status[i] = Committed
		}
	}

	// writeCount[k][t] = 该事务对 k 的写次数。
	writeCount := map[string]map[int]int{}
	// versions[k] 收集所有写版本（含未提交与中间版本）。
	type wv struct{ t, seq int }
	allVers := map[string][]wv{}

	var txns []Txn
	for i := 1; i <= n; i++ {
		t := Txn{ID: i, Status: status[i]}
		nops := 1 + rng.Intn(3)
		for j := 0; j < nops; j++ {
			k := keys[rng.Intn(len(keys))]
			if rng.Intn(2) == 0 {
				t.Ops = append(t.Ops, ww(k))
				if writeCount[k] == nil {
					writeCount[k] = map[int]int{}
				}
				writeCount[k][i]++
				allVers[k] = append(allVers[k], wv{i, writeCount[k][i]})
			} else {
				// 读：从该键已产生的版本（含初始版本）中挑一个。
				opts := [][2]int{v(0, 0)}
				for _, w := range allVers[k] {
					opts = append(opts, v(w.t, w.seq))
				}
				chosen := opts[rng.Intn(len(opts))]
				t.Ops = append(t.Ops, wr(k, chosen[0], chosen[1]))
			}
		}
		txns = append(txns, t)
	}

	order := map[string][][2]int{}
	for _, k := range keys {
		var lasts []int
		for t := 1; t <= n; t++ {
			if status[t] == Committed && writeCount[k][t] > 0 {
				lasts = append(lasts, t)
			}
		}
		rng.Shuffle(len(lasts), func(i, j int) { lasts[i], lasts[j] = lasts[j], lasts[i] })
		seq := [][2]int{v(0, 0)}
		for _, t := range lasts {
			seq = append(seq, v(t, writeCount[k][t]))
		}
		order[k] = seq
	}
	return History{Txns: txns, Order: order}
}

func TestOracleFuzz(t *testing.T) {
	rng := rand.New(rngSeed())
	for iter := 0; iter < 400; iter++ {
		h := randomValidHistory(rng)
		r := Analyze(h)
		m, rej := validate(h)
		if rej != "" {
			t.Fatalf("generator produced invalid history: %s", rej)
		}
		g, bad := buildGraph(h, m)
		hasG1a, hasG1b := false, false
		for _, b := range bad {
			hasG1a = hasG1a || b.category == CatG1a
			hasG1b = hasG1b || b.category == CatG1b
		}
		cat, w := oracleClassify(g, hasG1a, hasG1b)
		if r.Category != cat {
			t.Fatalf("iter %d category: got %s want %s\nhistory=%+v\nreason=%s",
				iter, r.Category, cat, h, r.Reason)
		}
		if w == nil {
			if r.Witness != nil {
				t.Fatalf("iter %d unexpected witness %+v", iter, r.Witness)
			}
		} else {
			if r.Witness == nil || !reflect.DeepEqual(r.Witness.Txns, w.Txns) {
				t.Fatalf("iter %d witness: got %+v want %+v", iter, r.Witness, w)
			}
		}
	}
}

// shuffleHistory 在保持每事务写次序语义的前提下打乱事务顺序，
// 并保持版本次序序列本身不变；结果必须逐字段一致。
func shuffleHistory(h History, rng *rand.Rand) History {
	txns := append([]Txn(nil), h.Txns...)
	rng.Shuffle(len(txns), func(i, j int) { txns[i], txns[j] = txns[j], txns[i] })
	return History{Txns: txns, Order: h.Order}
}

func TestPermutationIndependent(t *testing.T) {
	rng := rand.New(rngSeed())
	for iter := 0; iter < 200; iter++ {
		h := randomValidHistory(rng)
		base := Analyze(h)
		for s := 0; s < 5; s++ {
			got := Analyze(shuffleHistory(h, rng))
			if !reflect.DeepEqual(got, base) {
				t.Fatalf("iter %d shuffle %d:\n%+v\nvs\n%+v", iter, s, got, base)
			}
		}
	}
}

func TestRepeatedDeterministic(t *testing.T) {
	rng := rand.New(rngSeed())
	h := randomValidHistory(rng)
	first := Analyze(h)
	for i := 0; i < 50; i++ {
		if got := Analyze(h); !reflect.DeepEqual(got, first) {
			t.Fatalf("nondeterministic: %+v vs %+v", got, first)
		}
	}
}

func TestConcurrent(t *testing.T) {
	rng := rand.New(rngSeed())
	hs := make([]History, 8)
	for i := range hs {
		hs[i] = randomValidHistory(rng)
	}
	want := make([]Result, len(hs))
	for i, h := range hs {
		want[i] = Analyze(h)
	}
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		for i, h := range hs {
			wg.Add(1)
			i, h := i, h
			go func() {
				defer wg.Done()
				for k := 0; k < 30; k++ {
					if got := Analyze(h); !reflect.DeepEqual(got, want[i]) {
						t.Errorf("concurrent mismatch: %+v vs %+v", got, want[i])
						return
					}
				}
			}()
		}
	}
	wg.Wait()
}

func TestLoggerPrintsIOAndReason(t *testing.T) {
	var buf bytes.Buffer
	l := NewLogger(&buf)
	h := History{
		Txns: []Txn{
			{ID: 2, Status: Committed, Ops: []Op{wr("x", 0, 0), ww("x")}},
			{ID: 1, Status: Committed, Ops: []Op{wr("x", 0, 0), ww("x")}},
		},
		Order: map[string][][2]int{"x": {v(0, 0), v(2, 1), v(1, 1)}},
	}
	r := l.Analyze(h)
	if r.Category != CatGSingle {
		t.Fatalf("got %+v", r)
	}
	log := buf.String()
	if !bytes.Contains(buf.Bytes(), []byte(`input=`)) ||
		!bytes.Contains(buf.Bytes(), []byte(`output=`)) ||
		!bytes.Contains(buf.Bytes(), []byte("reason=")) {
		t.Fatalf("log missing sections:\n%s", log)
	}
	// 规范化输入按编号排序：T1 在 T2 之前。
	id1 := bytes.Index(buf.Bytes(), []byte(`"id":1`))
	id2 := bytes.Index(buf.Bytes(), []byte(`"id":2`))
	if id1 < 0 || id2 < 0 || id1 > id2 {
		t.Fatalf("input not canonical:\n%s", log)
	}
}
