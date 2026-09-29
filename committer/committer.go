package committer

import (
	"fmt"
	"sort"
	"sync"
)

// Committer 变更流消费位点与外部副作用的两阶段原子提交器。
// 阶段一 WriteEffect 先持久化副作用，阶段二 Commit 再推进位点。
type Committer struct {
	mu       sync.Mutex
	store    *fileStore
	position int64
	effects  map[int64]string
}

// Open 打开（或创建）位于 path 的提交器，崩溃重启后恢复已持久化状态。
func Open(path string) (*Committer, error) {
	store := newFileStore(path)
	st, err := store.load()
	if err != nil {
		return nil, err
	}
	c := &Committer{store: store, position: st.Position, effects: st.Effects}
	if err := c.checkLocked(); err != nil {
		return nil, fmt.Errorf("持久化状态自检失败: %w", err)
	}
	return c, nil
}

// WriteEffect 阶段一：仅当 seq == 已提交位点+1 时持久化副作用；重复写入幂等。
// 任何拒绝都发生在状态变更之前，失败不改变副作用存储与位点。
func (c *Committer) WriteEffect(seq int64, effect string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if seq <= 0 || seq <= c.position {
		return &Error{Kind: ErrInvalidSeq, Seq: seq, Position: c.position,
			Detail: "序号必须为正且大于已提交位点"}
	}
	if seq > c.position+1 {
		return &Error{Kind: ErrOutOfOrder, Seq: seq, Position: c.position,
			Detail: "副作用写入序号必须等于已提交位点加一"}
	}
	if _, ok := c.effects[seq]; ok {
		return nil // 同一在途序号重复写入：幂等成功
	}
	c.effects[seq] = effect
	if err := c.store.save(c.snapshotLocked()); err != nil {
		delete(c.effects, seq) // 持久化失败，回滚内存状态
		return fmt.Errorf("持久化副作用失败: %w", err)
	}
	return nil
}

// Commit 阶段二：仅当 seq == 已提交位点+1 且其副作用已写时推进位点。
func (c *Committer) Commit(seq int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if seq <= 0 || seq <= c.position {
		return &Error{Kind: ErrInvalidSeq, Seq: seq, Position: c.position,
			Detail: "序号必须为正且大于已提交位点"}
	}
	if seq > c.position+1 {
		return &Error{Kind: ErrPositionJump, Seq: seq, Position: c.position,
			Detail: "提交序号必须等于已提交位点加一"}
	}
	if _, ok := c.effects[seq]; !ok {
		return &Error{Kind: ErrMissingEffect, Seq: seq, Position: c.position,
			Detail: "副作用尚未写入，拒绝提交"}
	}
	prev := c.position
	c.position = seq
	if err := c.store.save(c.snapshotLocked()); err != nil {
		c.position = prev // 持久化失败，回滚内存状态
		return fmt.Errorf("持久化位点失败: %w", err)
	}
	return nil
}

// Position 返回已提交位点，单调不减。
func (c *Committer) Position() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.position
}

// Effect 查询某序号已持久化的副作用。
func (c *Committer) Effect(seq int64) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	effect, ok := c.effects[seq]
	return effect, ok
}

// Pending 返回已写副作用但位点未提交的序号，供崩溃重启后重复处理。
func (c *Committer) Pending() []int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	var pending []int64
	for seq := range c.effects {
		if seq > c.position {
			pending = append(pending, seq)
		}
	}
	sort.Slice(pending, func(i, j int) bool { return pending[i] < pending[j] })
	return pending
}

// Check 自检位点连续性与副作用完整性不变量。
func (c *Committer) Check() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.checkLocked()
}

// checkLocked 校验：1..position 的副作用齐全；不存在超过 position+1 的副作用。
func (c *Committer) checkLocked() error {
	for seq := int64(1); seq <= c.position; seq++ {
		if _, ok := c.effects[seq]; !ok {
			return fmt.Errorf("位点连续性破坏: 已提交序号 %d 缺少副作用", seq)
		}
	}
	for seq := range c.effects {
		if seq > c.position+1 {
			return fmt.Errorf("位点跳跃: 存在越序副作用 seq=%d position=%d", seq, c.position)
		}
	}
	return nil
}

func (c *Committer) snapshotLocked() state {
	effects := make(map[int64]string, len(c.effects))
	for seq, effect := range c.effects {
		effects[seq] = effect
	}
	return state{Position: c.position, Effects: effects}
}
