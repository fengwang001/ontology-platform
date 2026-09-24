// Package hyper 实现由显式种子可复现生成的随机超平面族与 b 位签名。
package hyper

import (
	"errors"
	"fmt"
	"math"
	"math/rand/v2"

	"ontology/vec"
)

// ErrDegeneratePlane 表示某条超平面法向量为全零，无法划分空间。
var ErrDegeneratePlane = errors.New("hyper: degenerate plane (zero normal)")

// DegenerateError 指出退化超平面所在的表号与位号（均从 0 起）。
type DegenerateError struct {
	Table int
	Bit   int
}

func (e *DegenerateError) Error() string {
	return fmt.Sprintf("%v: table %d bit %d", ErrDegeneratePlane, e.Table, e.Bit)
}

// Family 是 L 张表、每张表 b 个超平面的随机超平面族。
type Family struct {
	dim    int
	bits   int
	tables int
	seed   int64
	planes [][][]float64 // [table][bit] 法向量
}

// NewFamily 由显式种子生成超平面；同种子必得逐字节相同的法向量。
func NewFamily(dim, bits, tables int, seed int64) (*Family, error) {
	if dim <= 0 || bits <= 0 || tables <= 0 {
		return nil, fmt.Errorf("hyper: dim, bits and tables must be positive")
	}
	f := &Family{dim: dim, bits: bits, tables: tables, seed: seed,
		planes: make([][][]float64, tables)}
	for l := 0; l < tables; l++ {
		f.planes[l] = make([][]float64, bits)
		for j := 0; j < bits; j++ {
			// 每个 (表,位) 独立子流：显式、确定、跨位/跨表无关。
			s := uint64(seed) + uint64(l*1000003+j)
			r := rand.New(rand.NewPCG(s, s^0x9E3779B97F4A7C15))
			w := make([]float64, dim)
			var norm float64
			for k := range w {
				x := r.NormFloat64()
				w[k] = x
				norm += x * x
			}
			norm = math.Sqrt(norm)
			for k := range w {
				w[k] /= norm
			}
			f.planes[l][j] = w
		}
	}
	return f, nil
}

// FromPlanes 从显式法向量重建族（落盘读回用），并校验退化法向量。
func FromPlanes(dim, bits int, seed int64, planes [][][]float64) (*Family, error) {
	for l := range planes {
		for j := range planes[l] {
			zero := true
			for _, x := range planes[l][j] {
				if x != 0 {
					zero = false
					break
				}
			}
			if zero {
				return nil, &DegenerateError{Table: l, Bit: j}
			}
		}
	}
	return &Family{dim: dim, bits: bits, tables: len(planes), seed: seed, planes: planes}, nil
}

func (f *Family) Dim() int                 { return f.dim }
func (f *Family) Bits() int                { return f.bits }
func (f *Family) Tables() int              { return f.tables }
func (f *Family) Seed() int64              { return f.seed }
func (f *Family) Plane(l, j int) []float64 { return f.planes[l][j] }

// HashCounter 精确统计逐位哈希（内积）计算次数。
type HashCounter struct{ n int64 }

// NewHashCounter 创建一个从零开始的哈希计数器。
func NewHashCounter() *HashCounter { return &HashCounter{} }
func (c *HashCounter) add(n int)   { c.n += int64(n) }

// Value 返回累计哈希次数。
func (c *HashCounter) Value() int64 { return c.n }

func (f *Family) sign(l int, v vec.Vector, c *HashCounter) []byte {
	sig := make([]byte, (f.bits+7)/8)
	for j := 0; j < f.bits; j++ {
		// 内积 >= 0（含恰为 0 的零向量约定）置 1，<0 置 0。
		if vec.Dot(v, f.planes[l][j]) >= 0 {
			sig[j/8] |= 1 << (j % 8)
		}
		if c != nil {
			c.add(1)
		}
	}
	return sig
}

// Sign 返回第 l 张表上的签名，不计入哈希计数。
func (f *Family) Sign(l int, v vec.Vector) []byte { return f.sign(l, v, nil) }

// SignCounted 返回签名并逐位累计哈希次数。
func (f *Family) SignCounted(l int, v vec.Vector, c *HashCounter) []byte {
	return f.sign(l, v, c)
}
