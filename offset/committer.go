package offset

import (
	"fmt"
	"sync"
)

// Committer 是变更流消费端的位点提交器。
//
// 位点语义：已提交位点 = 下一条要读的位点（而非最后处理完的位点）。
// 小于已提交位点的消息均已确认；崩溃重启后从已提交位点继续读，
// 既不丢消息也不产生额外重复。
//
// 全部状态由一把互斥锁保护：任何一次 Deliver/Ack 的校验与生效
// 都在同一次临界区内完成，因此跨分区共享在途上限的扣减是原子的，
// 被拒绝的操作不会留下任何副作用。
type Committer struct {
	mu       sync.Mutex
	limit    int // 跨分区共享的在途总上限
	inflight int // 当前跨分区在途总数
	parts    map[string]*partition

	// scans 统计推进提交位点时的堆弹出次数，用于证明扫描次数
	// 与在途规模无关。不对外暴露，仅供同包测试断言。
	scans int64
}

// partition 是单个分区的提交状态。
type partition struct {
	committed   uint64              // 已提交位点（下一条要读的位点）
	nextDeliver uint64              // 期望的下一次投递位点
	acked       *offsetHeap         // 已确认但未提交的位点（最小堆）
	ackedSet    map[uint64]struct{} // 与 acked 同内容，用于 O(1) 幂等判定
	inflight    int                 // 本分区在途数（已投递未确认）
}

// New 创建一个共享在途上限为 limit 的提交器。
func New(limit int) (*Committer, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("%w: got %d", ErrInvalidLimit, limit)
	}
	return &Committer{
		limit: limit,
		parts: make(map[string]*partition),
	}, nil
}

// Restore 用崩溃前持久化的各分区已提交位点重建提交器，
// 重启后消费端从这些位点重新投递。
func Restore(limit int, committed map[string]uint64) (*Committer, error) {
	c, err := New(limit)
	if err != nil {
		return nil, err
	}
	for key, start := range committed {
		if err := c.Declare(key, start); err != nil {
			return nil, err
		}
	}
	return c, nil
}

// Declare 声明一个分区及其起点位点。起点即初始已提交位点。
func (c *Committer) Declare(key string, start uint64) error {
	if key == "" {
		return ErrEmptyPartitionKey
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.parts[key]; ok {
		return fmt.Errorf("%w: %q", ErrPartitionExists, key)
	}
	c.parts[key] = &partition{
		committed:   start,
		nextDeliver: start,
		acked:       &offsetHeap{},
		ackedSet:    make(map[uint64]struct{}),
	}
	return nil
}

// Deliver 逐条登记已投递位点，占用一份共享在途额度。
// 投递必须连续（offset 必须等于期望的下一条位点）；
// 在途总额达到共享上限时整体拒绝，不改变任何状态。
func (c *Committer) Deliver(key string, offset uint64) error {
	if key == "" {
		return ErrEmptyPartitionKey
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	p, ok := c.parts[key]
	if !ok {
		return fmt.Errorf("%w: %q", ErrPartitionNotFound, key)
	}
	if offset != p.nextDeliver {
		return fmt.Errorf("%w: partition %q expects %d, got %d",
			ErrNonContiguousDelivery, key, p.nextDeliver, offset)
	}
	if c.inflight+1 > c.limit {
		return fmt.Errorf("%w: limit %d, inflight %d",
			ErrInflightLimitExceeded, c.limit, c.inflight)
	}
	p.nextDeliver++
	p.inflight++
	c.inflight++
	return nil
}

// Ack 登记一个确认位点，并尽可能推进该分区的已提交位点。
//
// 校验规则：
//   - offset < committed：回退，拒绝；
//   - offset >= nextDeliver：确认了未投递的位点，拒绝；
//   - offset 已在确认集合中：重复确认，幂等成功，不改变状态。
func (c *Committer) Ack(key string, offset uint64) error {
	if key == "" {
		return ErrEmptyPartitionKey
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	p, ok := c.parts[key]
	if !ok {
		return fmt.Errorf("%w: %q", ErrPartitionNotFound, key)
	}
	if offset < p.committed {
		return fmt.Errorf("%w: partition %q committed %d, got %d",
			ErrOffsetRegression, key, p.committed, offset)
	}
	if offset >= p.nextDeliver {
		return fmt.Errorf("%w: partition %q delivered up to %d, got %d",
			ErrOffsetOutOfRange, key, p.nextDeliver, offset)
	}
	if _, dup := p.ackedSet[offset]; dup {
		return nil // 重复确认：幂等
	}
	p.ackedSet[offset] = struct{}{}
	p.acked.push(offset)
	p.inflight--
	c.inflight--

	// 推进已提交位点：只弹出恰好等于 committed 的堆顶。
	// 每个位点在整个生命周期内至多被弹出一次，
	// 故弹出总数 <= 确认总数，与在途规模无关。
	for {
		top, ok := p.acked.peek()
		if !ok || top != p.committed {
			break
		}
		p.acked.pop()
		delete(p.ackedSet, top)
		p.committed++
		c.scans++
	}
	return nil
}

// Committed 返回某分区当前的已提交位点。
func (c *Committer) Committed(key string) (uint64, error) {
	if key == "" {
		return 0, ErrEmptyPartitionKey
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	p, ok := c.parts[key]
	if !ok {
		return 0, fmt.Errorf("%w: %q", ErrPartitionNotFound, key)
	}
	return p.committed, nil
}

// Snapshot 返回所有分区已提交位点的一致性快照：
// 在同一次临界区内逐字段拷贝，各分区位点来自同一时间点。
func (c *Committer) Snapshot() map[string]uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	snap := make(map[string]uint64, len(c.parts))
	for key, p := range c.parts {
		snap[key] = p.committed
	}
	return snap
}

// Inflight 返回当前跨分区在途总数。
func (c *Committer) Inflight() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.inflight
}
