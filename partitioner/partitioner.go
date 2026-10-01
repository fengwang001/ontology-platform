// Package partitioner implements a sticky partitioner for keyless messages.
package partitioner

import (
	"errors"
	"sync"
)

var (
	ErrInvalidPartitionCount = errors.New("partitioner: partition count must be >= 1")
	ErrInvalidBatchSize      = errors.New("partitioner: batch size must be >= 1")
	ErrInvalidMessageSize    = errors.New("partitioner: message size must be >= 1")
	ErrPartitionOutOfRange   = errors.New("partitioner: partition index out of range")
	ErrNoAvailablePartition  = errors.New("partitioner: no available partition")
)

const (
	fnvOffsetBasis uint32 = 2166136261
	fnvPrime       uint32 = 16777619
)

// Partitioner 的所有公开方法均可并发调用，
// 结果等价于某个串行顺序。
type Partitioner struct {
	mu          sync.Mutex
	n           int
	batchBytes  int64
	sticky      int
	accumulated int64
	available   []bool
}

// New 创建分区器：N 个分区、批阈值 B 字节。
// 初始全部分区可用，粘性分区为 0。
func New(n int, batchBytes int64) (*Partitioner, error) {
	if n < 1 {
		return nil, ErrInvalidPartitionCount
	}
	if batchBytes < 1 {
		return nil, ErrInvalidBatchSize
	}
	available := make([]bool, n)
	for i := range available {
		available[i] = true
	}
	return &Partitioner{
		n:          n,
		batchBytes: batchBytes,
		available:  available,
	}, nil
}

// PartitionForKey 返回带键消息的分区：
// 键字节串的 32 位 FNV-1a 哈希（无符号）对 N 取余。
// 与分区可用性无关，且不影响粘性累计。
func (p *Partitioner) PartitionForKey(key []byte) int {
	var h uint32 = fnvOffsetBasis
	for _, b := range key {
		h ^= uint32(b)
		h *= fnvPrime
	}
	p.mu.Lock()
	n := p.n
	p.mu.Unlock()
	return int(h % uint32(n))
}

// StickyPartition 为一条大小为 size 的无键消息选择分区，
// 并按规则推进粘性状态。
func (p *Partitioner) StickyPartition(size int64) (int, error) {
	if size < 1 {
		return 0, ErrInvalidMessageSize
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.available[p.sticky] {
		next, ok := p.firstAvailableLocked()
		if !ok {
			return 0, ErrNoAvailablePartition
		}
		p.sticky = next
		p.accumulated = 0
	}

	partition := p.sticky
	p.accumulated += size
	if p.accumulated >= p.batchBytes {
		if next, ok := p.nextAvailableAfterLocked(p.sticky); ok {
			p.sticky = next
		}
		p.accumulated = 0
	}
	return partition, nil
}

// SetAvailable 设置分区可用性。
func (p *Partitioner) SetAvailable(partition int, available bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if partition < 0 || partition >= p.n {
		return ErrPartitionOutOfRange
	}
	p.available[partition] = available
	return nil
}

// Available 查询分区可用性。
func (p *Partitioner) Available(partition int) (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if partition < 0 || partition >= p.n {
		return false, ErrPartitionOutOfRange
	}
	return p.available[partition], nil
}

// State 查询当前粘性分区与累计字节。
func (p *Partitioner) State() (sticky int, accumulated int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.sticky, p.accumulated
}

// NumPartitions 返回分区数 N。
func (p *Partitioner) NumPartitions() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.n
}

// BatchBytes 返回批阈值 B。
func (p *Partitioner) BatchBytes() int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.batchBytes
}

// firstAvailableLocked 返回环形顺序下第一个可用分区（从 0 起扫描全环）。
func (p *Partitioner) firstAvailableLocked() (int, bool) {
	for i := 0; i < p.n; i++ {
		if p.available[i] {
			return i, true
		}
	}
	return 0, false
}

// nextAvailableAfterLocked 返回 current 之后（环形）第一个可用分区。
// 若只有 current 自身可用，返回 (current, false) 表示留在原地。
func (p *Partitioner) nextAvailableAfterLocked(current int) (int, bool) {
	for step := 1; step < p.n; step++ {
		next := (current + step) % p.n
		if p.available[next] {
			return next, true
		}
	}
	return current, false
}
