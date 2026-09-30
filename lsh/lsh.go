// Package lsh 实现基于随机超平面签名的近似最近邻检索器。
//
// 向量维度固定，相似度为余弦。索引由 L 张哈希表组成，每张表使用 b 个
// 由种子确定性生成的超平面。签名第 p 位为 1 当且仅当向量与第 p 个超平面
// 法向量的点积不小于零。表与位均满足嵌套关系：表数为 L 时所用的表恰为
// 更大表数下的前 L 张，位数为 b 时所用的超平面恰为更多位数下的前 b 个。
package lsh

import (
	"errors"
	"sync"
)

// MaxBits 为单张表签名位数的上限（签名以 uint64 位图存储）。
const MaxBits = 64

// 各类非法输入的可区分错误原因，可用 errors.Is 判定。
var (
	ErrInvalidDim        = errors.New("lsh: 维度必须为正整数")
	ErrDimMismatch       = errors.New("lsh: 向量维度与索引维度不符")
	ErrZeroVector        = errors.New("lsh: 零向量不允许插入或查询")
	ErrDuplicateID       = errors.New("lsh: 编号已存在")
	ErrIDNotFound        = errors.New("lsh: 编号不存在")
	ErrNonPositiveTables = errors.New("lsh: 表数必须为正整数")
	ErrNonPositiveBits   = errors.New("lsh: 位数必须为正整数")
	ErrBitsTooLarge      = errors.New("lsh: 位数超过上限")
	ErrNonPositiveK      = errors.New("lsh: K 必须为正整数")
)

// Result 为查询返回的单项结果。
type Result struct {
	ID     int
	Cosine float64
}

// Stats 报告一次查询的候选规模与精排计算次数。
type Stats struct {
	Candidates  int // 去重后的候选集合大小
	RerankCount int // 精确余弦计算次数
}

// Index 为并发安全的随机超平面 LSH 索引。
type Index struct {
	dim       int
	numTables int
	numBits   int
	seed      int64

	mu      sync.RWMutex
	entries map[int]*entry
	tables  []map[uint64]map[int]struct{}

	// normals[t][p] 为第 t 张表第 p 个超平面的法向量，构造时一次性
	// 确定性生成，之后只读。
	normals [][][]int64
}

type entry struct {
	vec  []int64
	sigs []uint64 // 每张表一个签名
}

// New 创建索引。dim、numTables 必须为正，numBits 必须在 [1, MaxBits] 内，
// 否则整体拒绝并返回对应错误。
func New(dim, numTables, numBits int, seed int64) (*Index, error) {
	if dim <= 0 {
		return nil, ErrInvalidDim
	}
	if numTables <= 0 {
		return nil, ErrNonPositiveTables
	}
	if numBits <= 0 {
		return nil, ErrNonPositiveBits
	}
	if numBits > MaxBits {
		return nil, ErrBitsTooLarge
	}
	idx := &Index{
		dim:       dim,
		numTables: numTables,
		numBits:   numBits,
		seed:      seed,
		entries:   make(map[int]*entry),
		tables:    make([]map[uint64]map[int]struct{}, numTables),
	}
	for t := range idx.tables {
		idx.tables[t] = make(map[uint64]map[int]struct{})
	}
	idx.normals = make([][][]int64, numTables)
	for t := 0; t < numTables; t++ {
		idx.normals[t] = make([][]int64, numBits)
		for p := 0; p < numBits; p++ {
			idx.normals[t][p] = normalAt(seed, t, p, dim)
		}
	}
	return idx, nil
}

// Dim 返回索引维度。
func (idx *Index) Dim() int { return idx.dim }

// NumTables 返回表数 L。
func (idx *Index) NumTables() int { return idx.numTables }

// NumBits 返回每张表的签名位数 b。
func (idx *Index) NumBits() int { return idx.numBits }

// Len 返回当前索引中的向量个数。
func (idx *Index) Len() int {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return len(idx.entries)
}
