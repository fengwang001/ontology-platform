package ontology

import (
	"encoding/binary"
	"fmt"
	"math"
	"sync"
)

// Filter 是并发安全的布隆过滤器，状态全部在进程内存中。
// 零值不可用，请通过 New 构造。
type Filter struct {
	mu    sync.RWMutex
	m     uint64   // 位数组长度（比特）
	k     uint64   // 每个元素使用的哈希个数
	words []uint64 // 位数组，按 64 位字存储，共 ceil(m/64) 个字
}

// New 按目标元素数 n 与目标假阳性率 p 构造过滤器。
// m 与 k 由公式 m=ceil(-n*ln(p)/(ln2)^2)、k=max(1,round(m/n*ln2))
// 推出。n<=0 时返回 ErrInvalidN，p<=0 或 p>=1 时返回 ErrInvalidP。
func New(n int, p float64) (*Filter, error) {
	if n <= 0 {
		return nil, fmt.Errorf("%w, got n=%d", ErrInvalidN, n)
	}
	if p <= 0 || p >= 1 {
		return nil, fmt.Errorf("%w, got p=%g", ErrInvalidP, p)
	}
	ln2 := math.Ln2
	m := uint64(math.Ceil(-float64(n) * math.Log(p) / (ln2 * ln2)))
	k := uint64(math.Round(float64(m) / float64(n) * ln2))
	if k < 1 {
		k = 1
	}
	return &Filter{
		m:     m,
		k:     k,
		words: make([]uint64, (m+63)/64),
	}, nil
}

// M 返回位数组长度（比特数）。
func (f *Filter) M() uint64 {
	return f.m
}

// K 返回每个元素使用的哈希个数。
func (f *Filter) K() uint64 {
	return f.k
}

// Add 把元素加入过滤器。幂等：重复加入同一元素不改变位数组。
// nil 与空切片视为同一个元素。
func (f *Filter) Add(elem []byte) {
	pos := positions(elem, f.m, f.k)
	f.mu.Lock()
	for _, p := range pos {
		f.words[p>>6] |= 1 << (p & 63)
	}
	f.mu.Unlock()
}

// MayContain 报告元素是否可能存在。已 Add 的元素一定返回 true
// （零假阴性）；未 Add 的元素有不超过目标假阳性率的概率返回 true。
func (f *Filter) MayContain(elem []byte) bool {
	pos := positions(elem, f.m, f.k)
	f.mu.RLock()
	defer f.mu.RUnlock()
	for _, p := range pos {
		if f.words[p>>6]&(1<<(p&63)) == 0 {
			return false
		}
	}
	return true
}

// Bytes 返回位数组在某个时刻的完整快照拷贝，按大端序编码
// 每个 64 位字。参数相同的过滤器比较 Bytes 即可判断内容是否
// 逐字节一致。返回的切片与内部状态无关，调用方可自由修改。
func (f *Filter) Bytes() []byte {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]byte, len(f.words)*8)
	for i, w := range f.words {
		binary.BigEndian.PutUint64(out[i*8:], w)
	}
	return out
}

// setBits 返回当前置位的比特数，供 EstimateCount 使用。
func (f *Filter) setBits() uint64 {
	f.mu.RLock()
	defer f.mu.RUnlock()
	var count uint64
	for _, w := range f.words {
		for w != 0 {
			w &= w - 1
			count++
		}
	}
	return count
}
