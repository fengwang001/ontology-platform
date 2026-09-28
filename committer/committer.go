// Package committer 实现变更流消费端的位点提交器：
// 多分区乱序确认、跨分区共享在途上限、原子推进可安全提交的位点。
//
// 位点语义：已提交位点表示“下一条要读的位点”，而非最后处理完的位点。
// 小于已提交位点的消息均已确认；崩溃重启后从已提交位点重新投递，
// 既不丢消息（未确认的必然 >= 已提交位点）也不多重复（已确认的必然 < 已提交位点）。
package committer

import "sync"

// PartitionState 是单个分区位点状态的只读快照。
// 快照在锁内一次性拷贝，逐字段一致。
type PartitionState struct {
	// Committed 已提交位点：下一条要读的位点，单调不减。
	Committed uint64
	// NextDeliver 期望的下一条投递位点。
	NextDeliver uint64
	// InFlight 该分区当前在途（已投递未确认）消息数。
	InFlight int
}

// Committer 是多分区位点提交器。所有方法并发安全。
type Committer struct {
	mu          sync.RWMutex
	maxInFlight int // 跨分区共享的在途总上限
	inFlight    int // 跨分区当前在途总数
	parts       map[string]*partition

	// advanceScans 推进已提交位点时“向前探一步”的总次数，
	// 仅供同包测试验证扫描次数与在途规模无关，不对外暴露。
	advanceScans int
}

// partition 单分区内部状态。
type partition struct {
	committed   uint64              // 已提交位点（下一条要读）
	nextDeliver uint64              // 期望的下一条投递位点
	inFlight    int                 // 分区内已投递未确认数
	pending     map[uint64]struct{} // 已确认但尚未被已提交位点越过的位点
}

// NewCommitter 创建提交器，maxInFlight 为跨分区共享的在途总上限。
func NewCommitter(maxInFlight int) *Committer {
	return &Committer{
		maxInFlight: maxInFlight,
		parts:       make(map[string]*partition),
	}
}

// DeclarePartition 声明分区及其起点位点；已提交位点初始化为 start。
func (c *Committer) DeclarePartition(key string, start uint64) error {
	if key == "" {
		return ErrEmptyPartitionKey
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.parts[key]; ok {
		return ErrPartitionAlreadyDeclared
	}
	c.parts[key] = &partition{
		committed:   start,
		nextDeliver: start,
		pending:     make(map[uint64]struct{}),
	}
	return nil
}

// Deliver 逐条登记一批已投递位点。
// 位点必须从该分区期望的下一条投递位点开始连续递增；
// 扣减共享在途上限是原子的：先校验后扣减，超限则整体拒绝，不改变任何状态。
func (c *Committer) Deliver(key string, offsets []uint64) error {
	if key == "" {
		return ErrEmptyPartitionKey
	}
	if len(offsets) == 0 {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	p, ok := c.parts[key]
	if !ok {
		return ErrPartitionNotDeclared
	}
	// 校验连续性：offsets[i] 必须等于 nextDeliver+i。
	for i, off := range offsets {
		if off != p.nextDeliver+uint64(i) {
			return ErrNonContiguousDelivery
		}
	}
	// 原子扣减共享上限：校验通过才扣，任一失败则整体不变。
	if c.inFlight+len(offsets) > c.maxInFlight {
		return ErrInFlightLimitExceeded
	}
	p.nextDeliver += uint64(len(offsets))
	p.inFlight += len(offsets)
	c.inFlight += len(offsets)
	return nil
}

// Ack 登记一个确认位点，并尽可能推进已提交位点。
// 越界（未投递）、回退（小于已提交位点）拒绝；重复确认幂等。
func (c *Committer) Ack(key string, offset uint64) error {
	if key == "" {
		return ErrEmptyPartitionKey
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	p, ok := c.parts[key]
	if !ok {
		return ErrPartitionNotDeclared
	}
	if offset < p.committed {
		return ErrAckRegression
	}
	if offset >= p.nextDeliver {
		return ErrAckOutOfRange
	}
	if _, dup := p.pending[offset]; dup {
		return nil // 重复确认：幂等，不改变任何状态
	}
	p.pending[offset] = struct{}{}
	p.inFlight--
	c.inFlight--
	// 推进已提交位点：每步消耗一个 pending 条目，
	// 全生命周期总步数等于已提交位点累计推进量，与在途规模无关。
	for {
		if _, ok := p.pending[p.committed]; !ok {
			break
		}
		delete(p.pending, p.committed)
		p.committed++
		c.advanceScans++
	}
	return nil
}

// Snapshot 返回指定分区状态的只读快照。
func (c *Committer) Snapshot(key string) (PartitionState, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	p, ok := c.parts[key]
	if !ok {
		return PartitionState{}, false
	}
	return PartitionState{
		Committed:   p.committed,
		NextDeliver: p.nextDeliver,
		InFlight:    p.inFlight,
	}, true
}

// SnapshotAll 返回所有分区状态的一致快照（同一读锁内拷贝）。
func (c *Committer) SnapshotAll() map[string]PartitionState {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make(map[string]PartitionState, len(c.parts))
	for k, p := range c.parts {
		out[k] = PartitionState{
			Committed:   p.committed,
			NextDeliver: p.nextDeliver,
			InFlight:    p.inFlight,
		}
	}
	return out
}

// InFlight 返回跨分区当前在途总数。
func (c *Committer) InFlight() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.inFlight
}
