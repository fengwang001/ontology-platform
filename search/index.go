// Package search 在随机超平面 LSH 桶表上做近似最近邻检索并给出召回度量。
package search

import (
	"sync"
	"sync/atomic"

	"ontology/bucket"
	"ontology/hyper"
	"ontology/vec"
)

// Index 是只读发布的 LSH 索引快照。
type Index struct {
	family    *hyper.Family
	tables    bucket.Tables
	vectors   []vec.Vector
	ids       []bucket.ID
	hashCount int64 // 建索引期内积（哈希位）计算次数
}

// Builder 负责累积向量并在 Build 时一次性发布不可变索引。
type Builder struct {
	dim    int
	bits   int
	tables int
	seed   int64

	mu      sync.Mutex
	vectors []vec.Vector
	skipped int
	built   bool
}

// NewBuilder 创建维度 dim、位数 bits、表数 tables、显式种子 seed 的构建器。
func NewBuilder(dim, bits, tables int, seed int64) *Builder {
	return &Builder{dim: dim, bits: bits, tables: tables, seed: seed}
}

// Skipped 返回因 NaN/Inf 被拒绝的向量数。
func (b *Builder) Skipped() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.skipped
}

// Add 加入一个向量；维度不符或含 NaN/Inf 时拒绝，NaN/Inf 计入 Skipped。
func (b *Builder) Add(v vec.Vector) error {
	if err := vec.CheckDim(b.dim, len(v)); err != nil {
		return err
	}
	if err := vec.Validate(v); err != nil {
		b.mu.Lock()
		b.skipped++
		b.mu.Unlock()
		return err
	}
	cp := make(vec.Vector, len(v))
	copy(cp, v)
	b.mu.Lock()
	b.vectors = append(b.vectors, cp)
	b.mu.Unlock()
	return nil
}

// Build 构建全部桶表并原子发布；进行中查询看不到半成品表。
func (b *Builder) Build() *Index {
	b.mu.Lock()
	vecs := make([]vec.Vector, len(b.vectors))
	copy(vecs, b.vectors)
	b.built = true
	b.mu.Unlock()

	fam := hyper.NewFamily(b.dim, b.bits, b.tables, b.seed)
	tables := bucket.New(b.tables)
	ids := make([]bucket.ID, len(vecs))
	var hc int64

	for i, v := range vecs {
		id := bucket.ID(i)
		ids[i] = id
		for t := 0; t < b.tables; t++ {
			var sig hyper.Signature
			for q := 0; q < b.bits; q++ {
				// 每位恰好一次内积，随后就地组装签名：总数恰为 N*L*b。
				dot, _ := vec.Dot(v, fam.Plane(t, q))
				atomic.AddInt64(&hc, 1)
				if dot >= 0 {
					sig |= hyper.Signature(1) << uint(q)
				}
			}
			tables.Add(t, sig, id)
		}
	}
	idx := &Index{
		family: fam, tables: tables.Snapshot(),
		vectors: vecs, ids: ids, hashCount: hc,
	}
	return idx
}

// Family 暴露超平面族（供持久化使用）。
func (ix *Index) Family() *hyper.Family { return ix.family }

// Tables 暴露桶表（供持久化使用）。
func (ix *Index) Tables() bucket.Tables { return ix.tables }

// Vectors 返回索引向量（只读）。
func (ix *Index) Vectors() []vec.Vector { return ix.vectors }

// HashCount 返回建索引期内积（哈希位）计算总次数。
func (ix *Index) HashCount() int64 { return ix.hashCount }

// Dim/Bits/Tables/N 返回索引参数。
func (ix *Index) Dim() int    { return ix.family.Dim }
func (ix *Index) Bits() int   { return ix.family.Bits }
func (ix *Index) TableN() int { return ix.family.Tables }
func (ix *Index) Seed() int64 { return ix.family.Seed }
func (ix *Index) N() int      { return len(ix.vectors) }
