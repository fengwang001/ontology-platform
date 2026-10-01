package sticky

import (
	"errors"
	"io"
	"log"
	"sync"
)

// 32 位 FNV-1a 参数（无符号运算）。
const (
	fnvOffset32 = uint32(2166136261)
	fnvPrime32  = uint32(16777619)
)

var (
	ErrInvalidPartitionCount = errors.New("sticky: partition count N must be >= 1")
	ErrInvalidBatchThreshold = errors.New("sticky: batch threshold B must be >= 1")
	ErrInvalidMessageSize    = errors.New("sticky: message size s must be >= 1")
	ErrPartitionOutOfRange   = errors.New("sticky: partition id out of range")
	ErrNoAvailablePartition  = errors.New("sticky: no partition is available")
)

// Partitioner 是并发安全的无键消息粘性分区器。
//
// 带键消息按 32 位 FNV-1a 哈希对分区数取余，固定且与可用性无关；
// 无键消息粘在当前粘性分区，累计字节达到阈值 B 后才切换到
// 环形方向上的下一个可用分区。所有方法可被并发调用，
// 其结果等价于某个串行执行顺序。
type Partitioner struct {
	mu          sync.Mutex
	n           int
	b           int
	available   []bool
	sticky      int
	accumulated int
	logger      *log.Logger
}

// New 创建分区器：分区数 N、批阈值 B（字节）。
// 初始时全部分区可用，粘性分区为 0 号分区，累计字节为 0。
func New(N int, B int) (*Partitioner, error) {
	if N < 1 {
		return nil, ErrInvalidPartitionCount
	}
	if B < 1 {
		return nil, ErrInvalidBatchThreshold
	}
	available := make([]bool, N)
	for i := range available {
		available[i] = true
	}
	p := &Partitioner{
		n:         N,
		b:         B,
		available: available,
		sticky:    0,
		logger:    log.New(io.Discard, "", log.LstdFlags|log.Lmicroseconds),
	}
	p.logger.Printf("init: N=%d B=%d sticky=0 accumulated=0 all-partitions-available", N, B)
	return p, nil
}

// SendKeyed 返回带键消息的分区：FNV-1a(key) mod N。
// 与分区可用性无关，不计入粘性累计，也不会改变粘性状态。
func (p *Partitioner) SendKeyed(key []byte) int {
	p.mu.Lock()
	hash := fnv1a32(key)
	partition := int(hash % uint32(p.n))
	p.logger.Printf("send-keyed: key=%q hash=%08x N=%d -> partition=%d (availability ignored, accumulation unchanged=%d)",
		key, hash, p.n, partition, p.accumulated)
	p.mu.Unlock()
	return partition
}

// SendKeyless 发送一条大小为 size 字节的无键消息，返回所选分区。
func (p *Partitioner) SendKeyless(size int) (int, error) {
	if size < 1 {
		p.logReject("send-keyless", "size<1", size)
		return 0, ErrInvalidMessageSize
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.availableCount() == 0 {
		p.logger.Printf("send-keyless: REJECT size=%d reason=no-available-partition state-unchanged sticky=%d accumulated=%d",
			size, p.sticky, p.accumulated)
		return 0, ErrNoAvailablePartition
	}

	// 仅在发送时刻判定：粘性分区不可用则先切到环形第一个可用分区并清零。
	if !p.available[p.sticky] {
		target, ok := p.nextAvailableAfter(p.sticky)
		if !ok {
			p.logger.Printf("send-keyless: REJECT size=%d reason=no-available-partition state-unchanged sticky=%d",
				size, p.sticky)
			return 0, ErrNoAvailablePartition
		}
		p.logger.Printf("send-keyless: sticky=%d unavailable at send-time -> switch to partition=%d, reset accumulated %d->0",
			p.sticky, target, p.accumulated)
		p.sticky = target
		p.accumulated = 0
	}

	partition := p.sticky
	p.accumulated += size

	if p.accumulated >= p.b {
		if next, ok := p.nextAvailableAfter(partition); ok {
			p.logger.Printf("send-keyless: size=%d partition=%d accumulated=%d>=B=%d -> switch sticky %d->%d, reset accumulated->0",
				size, partition, p.accumulated, p.b, partition, next)
			p.sticky = next
		} else {
			p.logger.Printf("send-keyless: size=%d partition=%d accumulated=%d>=B=%d -> only current available, sticky stays=%d, reset accumulated->0",
				size, partition, p.accumulated, p.b, partition)
		}
		p.accumulated = 0
	} else {
		p.logger.Printf("send-keyless: size=%d partition=%d accumulated=%d<B=%d -> sticky stays=%d",
			size, partition, p.accumulated, p.b, p.sticky)
	}
	return partition, nil
}

// SetAvailable 变更分区可用性。该操作本身不触发切换；
// 是否切换只在下一条无键消息发送时判定，恢复可用也不回切。
func (p *Partitioner) SetAvailable(partition int, available bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if partition < 0 || partition >= p.n {
		p.logger.Printf("set-available: REJECT partition=%d out-of-range [0,%d) state-unchanged", partition, p.n)
		return ErrPartitionOutOfRange
	}
	old := p.available[partition]
	p.available[partition] = available
	p.logger.Printf("set-available: partition=%d %v->%v sticky=%d accumulated=%d (no switch triggered)",
		partition, old, available, p.sticky, p.accumulated)
	return nil
}

// StickyPartition 查询当前粘性分区号。
func (p *Partitioner) StickyPartition() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.sticky
}

// AccumulatedBytes 查询当前粘性分区已累计的字节数。
func (p *Partitioner) AccumulatedBytes() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.accumulated
}

// IsAvailable 查询指定分区是否可用；编号越界返回 false。
func (p *Partitioner) IsAvailable(partition int) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if partition < 0 || partition >= p.n {
		return false
	}
	return p.available[partition]
}

// SetLogger 设置判定日志输出，记录每次调用的输入、输出与判定依据；
// 传 nil 表示丢弃日志。
func (p *Partitioner) SetLogger(w io.Writer) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if w == nil {
		w = io.Discard
	}
	p.logger = log.New(w, "", log.LstdFlags|log.Lmicroseconds)
}

// fnv1a32 计算 32 位 FNV-1a 哈希：offset basis 2166136261，
// 每个字节先异或再乘以素数 16777619，全程 uint32 无符号回绕。
func fnv1a32(key []byte) uint32 {
	hash := fnvOffset32
	for _, b := range key {
		hash ^= uint32(b)
		hash *= fnvPrime32
	}
	return hash
}

// nextAvailableAfter 返回 from 之后（环形，不含 from）第一个可用分区。
// 调用方需持有 p.mu。
func (p *Partitioner) nextAvailableAfter(from int) (int, bool) {
	for step := 1; step <= p.n; step++ {
		candidate := (from + step) % p.n
		if p.available[candidate] {
			return candidate, true
		}
	}
	return 0, false
}

// 调用方需持有 p.mu。
func (p *Partitioner) availableCount() int {
	count := 0
	for _, ok := range p.available {
		if ok {
			count++
		}
	}
	return count
}

func (p *Partitioner) logReject(op, reason string, value int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.logger.Printf("%s: REJECT value=%d reason=%s state-unchanged", op, value, reason)
}
