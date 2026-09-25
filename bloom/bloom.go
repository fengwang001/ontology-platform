// Package bloom 实现布隆过滤器：由 n 与 p 推导 m、k，双重哈希定位。
package bloom

import (
	"encoding/binary"
	"errors"
	"hash/fnv"
	"math"
	"sync/atomic"

	"ontology/bits"
)

// ErrBadParam 表示 n==0 或 p 不在 (0,1)。
var ErrBadParam = errors.New("bloom: bad parameter")

// Filter 是布隆过滤器。零值安全：MaybeContains 恒为 false。
// 并发只读（MaybeContains）安全；Add 与读并发需外部同步。
type Filter struct {
	b     *bits.BitSet
	m     uint64
	k     uint64
	seed  uint64
	reads atomic.Uint64 // 单次 MaybeContains 的位读取次数
}

// New 按期望元素数 n 与目标假阳性率 p 构造过滤器：
// m = -n·ln p/(ln2)^2，k = (m/n)·ln2（推导见 NOTES.md）。
func New(n uint64, p float64, seed uint64) (*Filter, error) {
	if n == 0 || p <= 0 || p >= 1 {
		return nil, ErrBadParam
	}
	m := uint64(math.Ceil(-float64(n) * math.Log(p) / (math.Ln2 * math.Ln2)))
	k := uint64(math.Round(float64(m) / float64(n) * math.Ln2))
	if k == 0 {
		k = 1
	}
	return &Filter{b: bits.New(m), m: m, k: k, seed: seed}, nil
}

// hashes 用带 seed 的 FNV-1a 派生两个哈希，供双重哈希组合，结果确定。
func (f *Filter) hashes(data []byte) (uint64, uint64) {
	h := fnv.New64a()
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], f.seed)
	_, _ = h.Write(buf[:])
	_, _ = h.Write(data)
	h1 := h.Sum64()
	_, _ = h.Write([]byte{0x9e})
	return h1, h.Sum64() | 1 // h2 取奇，保证步长遍历性好
}

// Add 将值加入过滤器。
func (f *Filter) Add(b []byte) {
	if f.m == 0 {
		return
	}
	h1, h2 := f.hashes(b)
	for i := uint64(0); i < f.k; i++ {
		_ = f.b.Set((h1 + i*h2) % f.m)
	}
}

// MaybeContains 报告值是否大概率见过；无假阴性。恰好读取 k 位。
func (f *Filter) MaybeContains(b []byte) bool {
	if f.m == 0 || f.k == 0 {
		return false
	}
	h1, h2 := f.hashes(b)
	found := true
	var n uint64
	for i := uint64(0); i < f.k; i++ {
		bit, _ := f.b.Get((h1 + i*h2) % f.m)
		n++
		found = found && bit
	}
	f.reads.Store(n)
	return found
}

// M 返回推导出的位数组大小。
func (f *Filter) M() uint64 { return f.m }

// K 返回推导出的哈希函数个数。
func (f *Filter) K() uint64 { return f.k }

// Reads 返回上一次 MaybeContains 的位读取次数。
func (f *Filter) Reads() uint64 { return f.reads.Load() }
