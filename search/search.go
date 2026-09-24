// Package search 在随机超平面 LSH 桶上做候选生成与精确距离精排的 TopK 查询。
package search

import (
	"errors"
	"sort"
	"sync/atomic"

	"ontology/bucket"
	"ontology/hyper"
	"ontology/vec"
)

// snapshot 是建索引完成后整体发布的不可变只读结构。
type snapshot struct {
	dim, bits, tables int
	fam               *hyper.Family
	buckets           *bucket.MultiTable
	vectors           []vec.Vector
	hashes            int64
}

// Index 是 LSH 近似最近邻检索器；可并发只读查询。
type Index struct {
	cur      atomic.Pointer[snapshot]
	skipped  atomic.Int64
	buildSeq int64
}

// New 返回空索引（快照尚未发布，查询在 Build 前返回 ErrNotBuilt）。
func New() *Index { return &Index{} }

// ErrNotBuilt 表示索引尚未建表，查询不可用。
var ErrNotBuilt = errors.New("search: index not built")

// Config 是建索引参数。
type Config struct {
	Dim    int
	Bits   int
	Tables int
	Seed   int64
}

// Build 用同一份配置原子地重建索引；返回前查询看到的仍是旧快照，
// 返回后新快照（含全部表）一次性整体可见，不存在半成品表。
func (ix *Index) Build(cfg Config, vectors []vec.Vector) error {
	fam, err := hyper.NewFamily(cfg.Dim, cfg.Bits, cfg.Tables, cfg.Seed)
	if err != nil {
		return err
	}
	var skipped int64
	valid := make([]vec.Vector, 0, len(vectors))
	for _, v := range vectors {
		if e := vec.Validate(v, cfg.Dim); e != nil {
			if errors.Is(e, vec.ErrInvalidValue) {
				skipped++
				continue
			}
			return e
		}
		valid = append(valid, v)
	}
	buckets := bucket.NewMultiTable(cfg.Tables)
	hc := hyper.NewHashCounter()
	for id, v := range valid {
		for l := 0; l < cfg.Tables; l++ {
			sig := fam.SignCounted(l, v, hc)
			buckets.Add(l, sig, id)
		}
	}
	snap := &snapshot{
		dim: cfg.Dim, bits: cfg.Bits, tables: cfg.Tables,
		fam: fam, buckets: buckets, vectors: valid, hashes: hc.Value(),
	}
	ix.cur.Store(snap)
	ix.skipped.Add(skipped)
	return nil
}

// Skipped 返回因 NaN/±Inf 被跳过的向量累计数。
func (ix *Index) Skipped() int64 { return ix.skipped.Load() }

// Result 是一个 TopK 命中。
type Result struct {
	ID  int
	Vec vec.Vector
}

func (ix *Index) snapshot() (*snapshot, error) {
	s := ix.cur.Load()
	if s == nil {
		return nil, ErrNotBuilt
	}
	return s, nil
}

// HashCount 返回建索引阶段逐位哈希计算的总次数。
func (ix *Index) HashCount() (int64, error) {
	s, err := ix.snapshot()
	if err != nil {
		return 0, err
	}
	return s.hashes, nil
}

// Len 返回已收录向量数。
func (ix *Index) Len() (int, error) {
	s, err := ix.snapshot()
	if err != nil {
		return 0, err
	}
	return len(s.vectors), nil
}

// Signature 返回某向量当前快照下每张表的签名（可复现性核对用）。
func (ix *Index) Signature(id int) ([][]byte, error) {
	s, err := ix.snapshot()
	if err != nil {
		return nil, err
	}
	if id < 0 || id >= len(s.vectors) {
		return nil, errors.New("search: vector id out of range")
	}
	out := make([][]byte, s.tables)
	for l := range out {
		out[l] = s.fam.Sign(l, s.vectors[id])
	}
	return out, nil
}

// search 是查询内核：限定前 firstTables 张表，返回精排结果与距离计算数。
func (s *snapshot) search(q vec.Vector, k, firstTables int) ([]Result, int, error) {
	if err := vec.Validate(q, s.dim); err != nil {
		return nil, 0, err
	}
	if firstTables <= 0 || firstTables > s.tables {
		firstTables = s.tables
	}
	sigs := make([][]byte, s.tables)
	use := make([]bool, s.tables)
	for l := 0; l < firstTables; l++ {
		sigs[l] = s.fam.Sign(l, q)
		use[l] = true
	}
	cand := s.buckets.Candidates(sigs, use)
	type scored struct {
		id int
		d  float64
	}
	all := make([]scored, len(cand))
	for i, id := range cand {
		all[i] = scored{id, vec.Euclidean(q, s.vectors[id])} // 精排距离计数
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].d != all[j].d {
			return all[i].d < all[j].d
		}
		return all[i].id < all[j].id
	})
	if k > len(all) {
		k = len(all)
	}
	res := make([]Result, k)
	for i := 0; i < k; i++ {
		id := all[i].id
		res[i] = Result{ID: id, Vec: s.vectors[id]}
	}
	return res, len(all), nil
}

// Search 返回 TopK 近邻与精排阶段实际计算距离的候选数。
func (ix *Index) Search(q vec.Vector, k int) ([]Result, int, error) {
	s, err := ix.snapshot()
	if err != nil {
		return nil, 0, err
	}
	return s.search(q, k, s.tables)
}

// SearchTables 仅用前 firstTables 张表查询（召回-表数关系测试用）。
func (ix *Index) SearchTables(q vec.Vector, k, firstTables int) ([]Result, int, error) {
	s, err := ix.snapshot()
	if err != nil {
		return nil, 0, err
	}
	return s.search(q, k, firstTables)
}

// Config 当前快照的建索引参数。
func (ix *Index) Config() (Config, error) {
	s, err := ix.snapshot()
	if err != nil {
		return Config{}, err
	}
	return Config{Dim: s.dim, Bits: s.bits, Tables: s.tables, Seed: s.fam.Seed()}, nil
}

// Family 返回当前超平面族（持久化用）。
func (ix *Index) Family() (*hyper.Family, error) {
	s, err := ix.snapshot()
	if err != nil {
		return nil, err
	}
	return s.fam, nil
}

// Vectors 返回已收录向量（持久化用）。
func (ix *Index) Vectors() ([]vec.Vector, error) {
	s, err := ix.snapshot()
	if err != nil {
		return nil, err
	}
	return s.vectors, nil
}

// Buckets 返回多桶表（持久化用）。
func (ix *Index) Buckets() (*bucket.MultiTable, error) {
	s, err := ix.snapshot()
	if err != nil {
		return nil, err
	}
	return s.buckets, nil
}

// BuildFromParts 用显式部件原子地发布索引（落盘读回/恢复用）。
func (ix *Index) BuildFromParts(fam *hyper.Family, vectors []vec.Vector,
	buckets *bucket.MultiTable) error {
	if fam.Tables() != buckets.Len() {
		return errors.New("search: family/bucket table count mismatch")
	}
	for _, v := range vectors {
		if err := vec.Validate(v, fam.Dim()); err != nil {
			return err
		}
	}
	snap := &snapshot{
		dim: fam.Dim(), bits: fam.Bits(), tables: fam.Tables(), fam: fam,
		buckets: buckets, vectors: vectors,
		hashes: int64(len(vectors)) * int64(fam.Tables()) * int64(fam.Bits()),
	}
	ix.cur.Store(snap)
	return nil
}
