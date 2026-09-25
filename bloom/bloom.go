// Package bloom 实现基于位数组与双重哈希的布隆过滤器。
package bloom

import (
	"encoding/binary"
	"errors"
	"hash/fnv"
	"math"
	"sync/atomic"

	"ontology/bits"
)

// ErrBadParam 表示 New 的参数非法（n==0 或 p 不在 (0,1)）。
var ErrBadParam = errors.New("bloom: bad parameter")

// Filter 是布隆过滤器，Add 后并发 MaybeContains 只读安全。
type Filter struct {
	bits  *bits.Array
	m     uint64
	k     uint64
	seed  uint64
	reads atomic.Uint64 // 最近一次 MaybeContains 的位读取次数
}

// New 按期望元素数 n 与目标假阳性率 p 推导 m、k 并构造过滤器。
func New(n uint64, p float64, seed uint64) (*Filter, error) {
	if n == 0 || p <= 0 || p >= 1 {
		return nil, ErrBadParam
	}
	ln2 := math.Log(2)
	m := uint64(math.Ceil(-float64(n) * math.Log(p) / (ln2 * ln2)))
	k := uint64(math.Round(float64(m) / float64(n) * ln2))
	if k == 0 {
		k = 1
	}
	return &Filter{bits: bits.New(m), m: m, k: k, seed: seed}, nil
}

// K 返回推导出的哈希函数个数。
func (f *Filter) K() uint64 { return f.k }

// Reads 返回最近一次 MaybeContains 读取的位数（恰好为 k）。
func (f *Filter) Reads() uint64 { return f.reads.Load() }

// hashes 用同一 seed 派生两个独立的 64 位哈希，供双重哈希使用。
func (f *Filter) hashes(b []byte) (uint64, uint64) {
	var sb [8]byte
	h := fnv.New64a()
	binary.LittleEndian.PutUint64(sb[:], f.seed)
	h.Write(sb[:])
	h.Write(b)
	h1 := h.Sum64()
	h.Reset()
	binary.LittleEndian.PutUint64(sb[:], f.seed^0x9E3779B97F4A7C15)
	h.Write(sb[:])
	h.Write(b)
	return h1, h.Sum64()
}

// Add 把值加入过滤器。
func (f *Filter) Add(b []byte) {
	if f.m == 0 || f.k == 0 {
		return
	}
	h1, h2 := f.hashes(b)
	for i := uint64(0); i < f.k; i++ {
		f.bits.Set((h1 + i*h2) % f.m)
	}
}

// MaybeContains 报告值是否大概率见过；无假阴性。每次恰好读取 k 位。
func (f *Filter) MaybeContains(b []byte) bool {
	if f.m == 0 || f.k == 0 {
		return false
	}
	h1, h2 := f.hashes(b)
	ok := true
	f.reads.Store(0)
	for i := uint64(0); i < f.k; i++ {
		f.reads.Add(1)
		if !f.bits.Get((h1 + i*h2) % f.m) {
			ok = false
		}
	}
	return ok
}
