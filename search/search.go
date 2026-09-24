// Package search 在 LSH 桶表上做候选生成与精排，返回 TopK 近似最近邻。
package search

import (
	"errors"
	"sort"
	"sync"
	"sync/atomic"

	"ontology/bucket"
	"ontology/hyper"
	"ontology/vec"
)

// view 是某一时刻索引的完整只读视图，整体原子发布，查询不会看到半成品表。
type view struct {
	fams []*hyper.Family
	set  *bucket.Set
}

// Index 是固定维度的近似最近邻索引。查询只读可并发；AddTables 与查询并发安全。
type Index struct {
	dim, bits int
	seed      int64
	vecs      []vec.Vec
	skipped   int
	v         atomic.Pointer[view]
	dists     atomic.Int64 // 精排阶段算过距离的向量数
	buildMu   sync.Mutex
}

// Build 用 tables 张表、每张 bits 位签名为 vecs 建索引。
// 维度不符的向量报错；含 NaN/±Inf 的向量被拒绝并计入 Skipped。
func Build(vecs []vec.Vec, dim, bits, tables int, seed int64) (*Index, error) {
	ix := &Index{dim: dim, bits: bits, seed: seed}
	for _, v := range vecs {
		if err := vec.Check(v, dim); err != nil {
			if errors.Is(err, vec.ErrDim) {
				return nil, err
			}
			ix.skipped++
			continue
		}
		ix.vecs = append(ix.vecs, v)
	}
	w, err := ix.makeView(0, tables)
	if err != nil {
		return nil, err
	}
	ix.v.Store(w)
	return ix, nil
}

// makeView 构建 [from, to) 号表并返回包含前 to 张表的新视图。
func (ix *Index) makeView(from, to int) (*view, error) {
	w := &view{}
	if old := ix.v.Load(); old != nil {
		w.fams = append(w.fams, old.fams...)
		w.set = &bucket.Set{Tables: append([]*bucket.Table(nil), old.set.Tables...)}
	} else {
		w.set = &bucket.Set{}
	}
	for t := from; t < to; t++ {
		f := hyper.New(ix.dim, ix.bits, ix.seed+int64(t))
		tab := bucket.NewTable()
		for id, v := range ix.vecs {
			tab.Add(f.Signature(v), id)
		}
		w.fams = append(w.fams, f)
		w.set.Tables = append(w.set.Tables, tab)
	}
	return w, nil
}

// AddTables 追加 n 张表，构建完成后原子发布：并发的查询要么看到旧视图，
// 要么看到完整的新视图，永远不会用到半成品表。
func (ix *Index) AddTables(n int) {
	ix.buildMu.Lock()
	defer ix.buildMu.Unlock()
	cur := len(ix.v.Load().fams)
	w, _ := ix.makeView(cur, cur+n)
	ix.v.Store(w)
}

// Query 返回 q 的 TopK 近似最近邻的向量 ID（按距离升序，距离相同按 ID 升序）。
// k 大于向量总数时返回全部；k<=0 返回空。查询向量维度/合法性不符时报错。
func (ix *Index) Query(q vec.Vec, k int) ([]int, error) {
	if err := vec.Check(q, ix.dim); err != nil {
		return nil, err
	}
	w := ix.v.Load()
	sigs := make([]uint64, len(w.fams))
	for i, f := range w.fams {
		sigs[i] = f.Signature(q)
	}
	cand := w.set.Candidates(sigs)
	type scored struct {
		id int
		d  float64
	}
	ss := make([]scored, len(cand))
	for i, id := range cand {
		ix.dists.Add(1)
		ss[i] = scored{id, vec.Dist(ix.vecs[id], q)}
	}
	sort.Slice(ss, func(i, j int) bool {
		if ss[i].d != ss[j].d {
			return ss[i].d < ss[j].d
		}
		return ss[i].id < ss[j].id
	})
	if k > len(ss) {
		k = len(ss)
	}
	if k < 0 {
		k = 0
	}
	out := make([]int, k)
	for i := range out {
		out[i] = ss[i].id
	}
	return out, nil
}

// DistCount 返回精排阶段累计的距离计算次数（去重后的候选数）。
func (ix *Index) DistCount() int64 { return ix.dists.Load() }

// HashCount 返回全部表累计的哈希（内积）计算次数。
func (ix *Index) HashCount() int64 {
	var n int64
	for _, f := range ix.v.Load().fams {
		n += f.Hashes()
	}
	return n
}

// Skipped 返回建索引时因 NaN/±Inf 被拒绝的向量数。
func (ix *Index) Skipped() int { return ix.skipped }

// Dim、Bits、NumVecs、Seed 返回索引的固定参数。
func (ix *Index) Dim() int     { return ix.dim }
func (ix *Index) Bits() int    { return ix.bits }
func (ix *Index) NumVecs() int { return len(ix.vecs) }
func (ix *Index) Seed() int64  { return ix.seed }

// Snapshot 返回当前视图的族与桶表（只读使用，供持久化）。
func (ix *Index) Snapshot() ([]*hyper.Family, *bucket.Set) {
	w := ix.v.Load()
	return w.fams, w.set
}

// BruteForce 是精确最近邻基准：全量扫描返回 TopK 向量 ID。
func BruteForce(vecs []vec.Vec, q vec.Vec, k int) []int {
	type scored struct {
		id int
		d  float64
	}
	ss := make([]scored, len(vecs))
	for i, v := range vecs {
		ss[i] = scored{i, vec.Dist(v, q)}
	}
	sort.Slice(ss, func(i, j int) bool {
		if ss[i].d != ss[j].d {
			return ss[i].d < ss[j].d
		}
		return ss[i].id < ss[j].id
	})
	if k > len(ss) {
		k = len(ss)
	}
	out := make([]int, k)
	for i := range out {
		out[i] = ss[i].id
	}
	return out
}

// Recall 返回近似结果相对精确结果的召回率：|approx ∩ exact| / |exact|。
func Recall(approx, exact []int) float64 {
	if len(exact) == 0 {
		return 1
	}
	set := make(map[int]struct{}, len(approx))
	for _, id := range approx {
		set[id] = struct{}{}
	}
	var hit int
	for _, id := range exact {
		if _, ok := set[id]; ok {
			hit++
		}
	}
	return float64(hit) / float64(len(exact))
}
