package ontology

import (
	"math"
	"sync"
)

// Filter 是一个并发安全的布隆过滤器，状态全部在进程内存中。
//
// 参数推导（构造时由目标元素数 n 与目标假阳性率 p 推出）：
//
//	m = ceil( -n * ln(p) / (ln 2)^2 )   位数组长度（比特数）
//	k = max(1, round( (m/n) * ln 2 ))   每个元素的哈希个数
//
// 在此参数下，插入 n 个元素后的理论假阳性率约为
// (1 - e^(-k*n/m))^k ≈ p。实测值围绕 p 波动，因此测试采用 [0, 2p]
// 的宽松上界：既覆盖住随机涨落，又能抓住实现错误（如哈希退化）。
//
// 不支持删除：多个元素共享同一批位，清掉任一元素贡献的位可能同时
// 清掉其他元素依赖的位，从而引入假阴性，破坏布隆过滤器的核心保证。
// 因此本包不提供 Remove。
type Filter struct {
	mu   sync.RWMutex
	bits []byte // 位数组，第 i 位为 bits[i/8] 的第 i%8 位
	m    uint   // 位数组长度（比特数）
	k    uint   // 哈希个数
}

// New 按目标元素数 n 与目标假阳性率 p 构造过滤器。
// n <= 0 时返回 ErrInvalidN；p <= 0 或 p >= 1 时返回 ErrInvalidP。
func New(n uint, p float64) (*Filter, error) {
	if n == 0 {
		return nil, ErrInvalidN
	}
	if p <= 0 || p >= 1 || math.IsNaN(p) {
		return nil, ErrInvalidP
	}
	ln2 := math.Ln2
	m := uint(math.Ceil(-float64(n) * math.Log(p) / (ln2 * ln2)))
	k := uint(math.Round(float64(m) / float64(n) * ln2))
	if k < 1 {
		k = 1
	}
	return &Filter{
		bits: make([]byte, (m+7)/8),
		m:    m,
		k:    k,
	}, nil
}

// M 返回位数组长度（比特数）。
func (f *Filter) M() uint { return f.m }

// K 返回每个元素的哈希个数。
func (f *Filter) K() uint { return f.k }

// Add 把元素加入过滤器。空切片与 nil 是同一个合法元素。
func (f *Filter) Add(data []byte) {
	pos := positions(data, f.m, f.k)
	f.mu.Lock()
	for _, p := range pos {
		f.bits[p/8] |= 1 << (p % 8)
	}
	f.mu.Unlock()
}

// MayContain 报告元素是否可能在集合中。
// 假为确定的"不在"，真为"可能在"（存在假阳性，绝无假阴性）。
// 空过滤器对任何元素都返回假。
func (f *Filter) MayContain(data []byte) bool {
	pos := positions(data, f.m, f.k)
	f.mu.RLock()
	defer f.mu.RUnlock()
	for _, p := range pos {
		if f.bits[p/8]&(1<<(p%8)) == 0 {
			return false
		}
	}
	return true
}

// Bytes 返回位数组的完整快照拷贝。并发调用时取到的是某一时刻
// （读锁临界区内）的完整状态，不会是半更新的中间态。
func (f *Filter) Bytes() []byte {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]byte, len(f.bits))
	copy(out, f.bits)
	return out
}
