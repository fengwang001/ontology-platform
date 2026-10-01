// Package lsh 实现基于随机超平面签名的近似最近邻检索器。
//
// 向量维度固定，相似度为余弦。索引在创建时按种子确定性地生成
// maxTables x maxBits 个超平面法向量；使用 L 张表、每张表 b 位时，
// 实际使用的恰为前 L 张表、每张表的前 b 个超平面（嵌套关系）。
// 签名位规则：向量与法向量点积 >= 0 取 1，否则取 0。
package lsh

import (
	"errors"
	"math"
	"math/rand"
	"sort"
	"sync"
)

// 可区分的拒绝原因，调用方可用 errors.Is 判定。
var (
	ErrDimensionMismatch = errors.New("lsh: 向量维度不符")
	ErrZeroVector        = errors.New("lsh: 零向量")
	ErrDuplicateID       = errors.New("lsh: 编号重复")
	ErrNotFound          = errors.New("lsh: 编号不存在")
	ErrInvalidTables     = errors.New("lsh: 表数非正或超上限")
	ErrInvalidBits       = errors.New("lsh: 位数非正或超上限")
	ErrInvalidK          = errors.New("lsh: K 非正")
	ErrInvalidDim        = errors.New("lsh: 维度非正")
)

// MaxBits 为签名位数上限（uint64 位宽）。
const MaxBits = 64

// Result 为一次查询返回的单项结果。
type Result struct {
	ID    int
	Score float64 // 精确余弦相似度
}

// Stats 报告一次查询的候选规模与精排开销。
type Stats struct {
	Candidates int // 去重后的候选数
	Reranked   int // 精确余弦计算次数
}

// Index 为并发安全的 LSH 索引。
type Index struct {
	mu        sync.RWMutex
	dim       int
	maxTables int
	maxBits   int
	planes    [][][]float64                 // planes[表][位] = 法向量
	vectors   map[int][]int64               // 编号 -> 向量
	buckets   []map[uint64]map[int]struct{} // buckets[表][完整签名] = 编号集合
}

// NewIndex 创建索引。dim、maxTables 必须为正，maxBits 须在 [1, MaxBits]。
// 相同 seed 生成逐字节相同的超平面。
func NewIndex(dim, maxTables, maxBits int, seed int64) (*Index, error) {
	if dim <= 0 {
		return nil, ErrInvalidDim
	}
	if maxTables <= 0 {
		return nil, ErrInvalidTables
	}
	if maxBits <= 0 || maxBits > MaxBits {
		return nil, ErrInvalidBits
	}
	rng := rand.New(rand.NewSource(seed))
	planes := make([][][]float64, maxTables)
	for t := range planes {
		row := make([][]float64, maxBits)
		for b := range row {
			normal := make([]float64, dim)
			for d := range normal {
				normal[d] = rng.NormFloat64()
			}
			row[b] = normal
		}
		planes[t] = row
	}
	x := &Index{
		dim:       dim,
		maxTables: maxTables,
		maxBits:   maxBits,
		planes:    planes,
		vectors:   make(map[int][]int64),
		buckets:   make([]map[uint64]map[int]struct{}, maxTables),
	}
	for t := range x.buckets {
		x.buckets[t] = make(map[uint64]map[int]struct{})
	}
	return x, nil
}

// checkVector 校验维度与零向量。
func (x *Index) checkVector(vec []int64) error {
	if len(vec) != x.dim {
		return ErrDimensionMismatch
	}
	zero := true
	for _, v := range vec {
		if v != 0 {
			zero = false
			break
		}
	}
	if zero {
		return ErrZeroVector
	}
	return nil
}

// signatureLocked 计算向量在第 table 张表上前 bits 位的签名，bit j 对应第 j 个超平面。
func (x *Index) signatureLocked(vec []int64, table, bits int) uint64 {
	var sig uint64
	for b := 0; b < bits; b++ {
		normal := x.planes[table][b]
		var dot float64
		for d, v := range vec {
			dot += float64(v) * normal[d]
		}
		if dot >= 0 {
			sig |= 1 << uint(b)
		}
	}
	return sig
}

// Insert 插入整数向量。维度不符、零向量、编号重复时整体拒绝且不改变任何桶。
func (x *Index) Insert(id int, vec []int64) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	if err := x.checkVector(vec); err != nil {
		return err
	}
	if _, ok := x.vectors[id]; ok {
		return ErrDuplicateID
	}
	stored := make([]int64, len(vec))
	copy(stored, vec)
	x.vectors[id] = stored
	for t := 0; t < x.maxTables; t++ {
		sig := x.signatureLocked(stored, t, x.maxBits)
		bucket := x.buckets[t][sig]
		if bucket == nil {
			bucket = make(map[int]struct{})
			x.buckets[t][sig] = bucket
		}
		bucket[id] = struct{}{}
	}
	return nil
}

// Delete 删除编号。编号不存在时拒绝且不改变任何桶。
func (x *Index) Delete(id int) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	vec, ok := x.vectors[id]
	if !ok {
		return ErrNotFound
	}
	for t := 0; t < x.maxTables; t++ {
		sig := x.signatureLocked(vec, t, x.maxBits)
		bucket := x.buckets[t][sig]
		delete(bucket, id)
		if len(bucket) == 0 {
			delete(x.buckets[t], sig)
		}
	}
	delete(x.vectors, id)
	return nil
}

// Len 返回当前向量数。
func (x *Index) Len() int {
	x.mu.RLock()
	defer x.mu.RUnlock()
	return len(x.vectors)
}

// Signature 计算向量在第 table 张表上取前 bits 位的签名（供测试与调试）。
func (x *Index) Signature(vec []int64, table, bits int) (uint64, error) {
	x.mu.RLock()
	defer x.mu.RUnlock()
	if err := x.checkVector(vec); err != nil {
		return 0, err
	}
	if table < 0 || table >= x.maxTables {
		return 0, ErrInvalidTables
	}
	if bits <= 0 || bits > x.maxBits {
		return 0, ErrInvalidBits
	}
	return x.signatureLocked(vec, table, bits), nil
}

// cosine 计算两向量的余弦相似度；调用方保证均非零向量。
func cosine(a, b []int64) float64 {
	var dot, na, nb float64
	for i := range a {
		av, bv := float64(a[i]), float64(b[i])
		dot += av * bv
		na += av * av
		nb += bv * bv
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

// Query 返回候选集合按精确余弦降序的前 k 项，并列按编号升序。
// 候选为前 tables 张表中与查询同桶（按前 bits 位签名）向量的去重并集；
// 候选为空时返回空结果。任何参数非法时整体拒绝且不产生副作用。
func (x *Index) Query(vec []int64, tables, bits, k int) ([]Result, Stats, error) {
	x.mu.RLock()
	defer x.mu.RUnlock()
	if err := x.checkVector(vec); err != nil {
		return nil, Stats{}, err
	}
	if tables <= 0 || tables > x.maxTables {
		return nil, Stats{}, ErrInvalidTables
	}
	if bits <= 0 || bits > x.maxBits {
		return nil, Stats{}, ErrInvalidBits
	}
	if k <= 0 {
		return nil, Stats{}, ErrInvalidK
	}
	mask := uint64(math.MaxUint64) >> uint(MaxBits-bits)
	cand := make(map[int]struct{})
	for t := 0; t < tables; t++ {
		qsig := x.signatureLocked(vec, t, bits)
		for sig, bucket := range x.buckets[t] {
			if sig&mask != qsig {
				continue
			}
			for id := range bucket {
				cand[id] = struct{}{}
			}
		}
	}
	ids := make([]int, 0, len(cand))
	for id := range cand {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	results := make([]Result, 0, len(ids))
	for _, id := range ids {
		results = append(results, Result{ID: id, Score: cosine(vec, x.vectors[id])})
	}
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		return results[i].ID < results[j].ID
	})
	stats := Stats{Candidates: len(ids), Reranked: len(ids)}
	if len(results) > k {
		results = results[:k]
	}
	return results, stats, nil
}
