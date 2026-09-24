package search_test

import (
	"errors"
	"math"
	"math/rand"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/search"
	"ontology/vec"
)

const dim, nVec, nQry, seed, topK, bSeed = 32, 5000, 100, 2024, 10, 1000

// genData 生成 64 簇聚类数据与查询（固定种子，可复现）。
func genData() ([]vec.Vec, []vec.Vec) {
	r := rand.New(rand.NewSource(seed))
	centers := make([]vec.Vec, 64)
	for i := range centers {
		centers[i] = make(vec.Vec, dim)
		for j := range centers[i] {
			centers[i][j] = 4 * r.NormFloat64()
		}
	}
	jitter := func() vec.Vec {
		c := centers[r.Intn(len(centers))]
		v := make(vec.Vec, dim)
		for j := range v {
			v[j] = c[j] + 0.6*r.NormFloat64()
		}
		return v
	}
	vs, qs := make([]vec.Vec, nVec), make([]vec.Vec, nQry)
	for i := range vs {
		vs[i] = jitter()
	}
	for i := range qs {
		qs[i] = jitter()
	}
	return vs, qs
}

// eval 建索引并对全部查询求平均 top10 召回率与平均候选数（精排距离计算数）。
func eval(t *testing.T, vs, qs []vec.Vec, bits, tables int) (rec, cand float64) {
	t.Helper()
	ix, _ := search.Build(vs, dim, bits, tables, bSeed) // 合法输入不会失败
	for _, q := range qs {
		got, _ := ix.Query(q, topK)
		rec += search.Recall(got, search.BruteForce(vs, q, topK))
	}
	return rec / float64(len(qs)), float64(ix.DistCount()) / float64(len(qs))
}

func TestRecallMonotonic(t *testing.T) {
	vs, qs := genData()
	var prev float64
	for _, l := range []int{1, 2, 4, 8} {
		r, _ := eval(t, vs, qs, 8, l)
		t.Logf("recall@10 b=8 L=%d: %.4f", l, r)
		if r < prev {
			t.Errorf("召回率随表数下降: L=%d %.4f < 前档 %.4f", l, r, prev)
		}
		prev = r
	}
	if prev < 0.6 {
		t.Errorf("L=8 召回率 %.4f < 0.6", prev)
	}
}

func TestBitsSweep(t *testing.T) {
	vs, qs := genData()
	for _, l := range []int{1, 2, 4, 8} {
		var prev float64 = math.MaxFloat64
		for _, b := range []int{4, 8, 12} {
			r, c := eval(t, vs, qs, b, l)
			t.Logf("MATRIX b=%2d L=%d cand=%8.1f recall=%.4f", b, l, c, r)
			if c > prev {
				t.Errorf("候选数随位数上升: L=%d b档候选 %.1f > 前档 %.1f", l, c, prev)
			}
			prev = c
		}
	}
}

func TestCounters(t *testing.T) {
	vs, qs := genData()
	ix, _ := search.Build(vs, dim, 8, 8, bSeed)
	if got, want := ix.HashCount(), int64(nVec*8*8); got != want {
		t.Errorf("哈希计算次数 = %d, 应精确等于 N*L*b = %d", got, want)
	}
	for _, q := range qs {
		if _, err := ix.Query(q, topK); err != nil {
			t.Fatal(err)
		}
	}
	if avg := float64(ix.DistCount()) / nQry; avg > 0.1*nVec {
		t.Errorf("精排距离计算数均值 %.1f 超过总量 10%% (%d)", avg, nVec/10)
	}
}

func TestEdgeCases(t *testing.T) {
	bad := []vec.Vec{{1, math.NaN()}, {1, math.Inf(1)}, {1, math.Inf(-1)}, {1, 2}}
	ix, err := search.Build(bad, 2, 4, 2, 1)
	if err != nil || ix.Skipped() != 3 || ix.NumVecs() != 1 {
		t.Errorf("NaN/±Inf 拒绝: err=%v skipped=%d kept=%d, 应 0/3/1", err, ix.Skipped(), ix.NumVecs())
	}
	if _, err := search.Build([]vec.Vec{{1, 2, 3}}, 2, 4, 1, 1); !errors.Is(err, vec.ErrDim) {
		t.Errorf("建索引维度不符应报 ErrDim, got %v", err)
	}
	if _, err := ix.Query(vec.Vec{1, 2, 3}, 1); !errors.Is(err, vec.ErrDim) {
		t.Errorf("查询维度不符应报 ErrDim, got %v", err)
	}
	if _, err := ix.Query(vec.Vec{math.NaN(), 1}, 1); !errors.Is(err, vec.ErrNaN) {
		t.Errorf("查询含 NaN 应报 ErrNaN, got %v", err)
	}
	cases := []struct {
		name     string
		vs       []vec.Vec
		dim      int
		q        vec.Vec
		k        int
		wantN    int // 精确结果数，-1 表示不断言
		contains int // 结果必含该 ID，-1 表示不断言
	}{
		{name: "空集合", vs: nil, dim: 2, q: vec.Vec{0, 0}, k: 10, wantN: 0, contains: -1},
		{name: "单向量", vs: []vec.Vec{{3, 4}}, dim: 2, q: vec.Vec{3, 4}, k: 10, wantN: 1, contains: 0},
		{name: "K大于总数", vs: []vec.Vec{{1, 1}, {1, 1}, {1, 1}}, dim: 2, q: vec.Vec{1, 1}, k: 99, wantN: 3, contains: -1},
		{name: "K为0", vs: []vec.Vec{{0, 0}}, dim: 2, q: vec.Vec{0, 0}, k: 0, wantN: 0, contains: -1},
		{name: "维度为1", vs: []vec.Vec{{1}, {2}, {3}}, dim: 1, q: vec.Vec{2}, k: 2, wantN: -1, contains: 1},
		{name: "零向量合法", vs: []vec.Vec{{0, 0}, {1, 1}}, dim: 2, q: vec.Vec{0, 0}, k: 2, wantN: -1, contains: 0},
	}
	for _, c := range cases {
		ix, err := search.Build(c.vs, c.dim, 4, 2, 3)
		if err != nil {
			t.Errorf("%s: Build: %v", c.name, err)
			continue
		}
		got, err := ix.Query(c.q, c.k)
		if err != nil {
			t.Errorf("%s: Query: %v", c.name, err)
			continue
		}
		if c.wantN >= 0 && len(got) != c.wantN {
			t.Errorf("%s: 返回 %d 个, 应 %d 个", c.name, len(got), c.wantN)
		}
		if c.contains >= 0 && !slices.Contains(got, c.contains) {
			t.Errorf("%s: 结果 %v 应含 ID %d", c.name, got, c.contains)
		}
	}
	ident := make([]vec.Vec, 50)
	for i := range ident {
		ident[i] = vec.Vec{1, 2, 3}
	}
	xi, _ := search.Build(ident, 3, 8, 4, 5)
	got, _ := xi.Query(vec.Vec{1, 2, 3}, topK)
	if r := search.Recall(got, search.BruteForce(ident, vec.Vec{1, 2, 3}, topK)); r != 1 {
		t.Errorf("全同向量召回 = %v, 应为 1", r)
	}
	if xi.DistCount() != 50 {
		t.Errorf("全同向量候选数 = %d, 应等于总数 50", xi.DistCount())
	}
}

func TestConcurrentBuildQuery(t *testing.T) {
	vs, qs := genData()
	ix, err := search.Build(vs[:1000], dim, 8, 1, 9)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var stop atomic.Int32
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(q vec.Vec) {
			defer wg.Done()
			for stop.Load() == 0 {
				ids, err := ix.Query(q, topK)
				if err != nil {
					t.Errorf("并发查询出错: %v", err)
					return
				}
				for _, id := range ids {
					if id < 0 || id >= 1000 {
						t.Errorf("并发查询返回非法 ID %d", id)
						return
					}
				}
			}
		}(qs[g])
	}
	for i := 0; i < 7; i++ {
		ix.AddTables(1)
	}
	stop.Store(1)
	wg.Wait()
}
