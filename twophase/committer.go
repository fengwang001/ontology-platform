package twophase

import (
	"fmt"
	"sort"
	"sync"
)

// Committer 两阶段原子提交器：先写副作用（prepare），后进位点（commit）。
// 并发模型：
//   - 写入与提交共享一把锁，保证对同一在途序号的重复写入与提交串行判定；
//   - 查询（Position/Pending/快照）与自检使用同一把 RWMutex 的读锁，可彼此并发；
//   - 位点单调不减，且每次 Commit 至多推进一个序号。
type Committer struct {
	mu       sync.RWMutex
	store    Store
	position int64
	pending  map[int64]string
}

// New 构造提交器并从持久化存储恢复：
// 已提交位点 position 之前的副作用视为已提交；其后的副作用进入 pending，
// 调用方可通过 Pending() 拿到这些“效果已写、位点未进”的序号做重复处理。
func New(store Store) (*Committer, error) {
	position, err := store.LoadPosition()
	if err != nil {
		return nil, fmt.Errorf("twophase: recover position: %w", err)
	}
	seqs, err := store.ListPending(position)
	if err != nil {
		return nil, fmt.Errorf("twophase: recover pending: %w", err)
	}
	c := &Committer{
		store:    store,
		position: position,
		pending:  make(map[int64]string, len(seqs)),
	}
	for _, seq := range seqs {
		effect, err := store.LoadEffect(seq)
		if err != nil {
			return nil, fmt.Errorf("twophase: recover effect %d: %w", seq, err)
		}
		if effect == nil {
			continue
		}
		c.pending[seq] = effect.Action
	}
	return c, nil
}

// Write 阶段一：写入副作用（prepare）。
// 规则：
//   - seq 必须 > 0 且严格大于已提交位点，否则 RejectInvalidSeq；
//   - 只有 seq == 已提交位点 + 1 允许写入，更大序号按越序 RejectOutOfOrder 拒绝；
//   - 对同一在途序号的重复写入是幂等的：相同 action 直接返回，
//     不同 action 以 RejectEffectConflict 整体拒绝（不覆盖已写副作用）；
//   - 任何拒绝都不改变副作用存储与位点。
func (c *Committer) Write(seq int64, action string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if seq <= 0 {
		return &RejectError{Kind: RejectInvalidSeq, Seq: seq, Position: c.position,
			Detail: "seq must be positive"}
	}
	if seq <= c.position {
		return &RejectError{Kind: RejectInvalidSeq, Seq: seq, Position: c.position,
			Detail: "seq already committed"}
	}
	expected := c.position + 1
	if seq != expected {
		return &RejectError{Kind: RejectOutOfOrder, Seq: seq, Position: c.position,
			Detail: fmt.Sprintf("expect seq %d, write arrives out of order", expected)}
	}
	if existing, ok := c.pending[seq]; ok {
		if existing != action {
			return &RejectError{Kind: RejectEffectConflict, Seq: seq, Position: c.position,
				Detail: fmt.Sprintf("duplicate write with different effect: stored=%q incoming=%q",
					existing, action)}
		}
		return nil
	}

	if err := c.store.SaveEffect(Effect{Seq: seq, Action: action}); err != nil {
		return fmt.Errorf("twophase: persist effect %d: %w", seq, err)
	}
	c.pending[seq] = action
	return nil
}

// Commit 阶段二：推进位点（commit）。
// 规则：
//   - seq 必须 > 0，否则 RejectInvalidSeq；
//   - 只有 seq == 已提交位点 + 1 可提交，否则按位点跳跃 RejectGap 拒绝；
//   - 该序号的副作用必须已经写入，否则 RejectEffectMissing；
//   - 满足全部条件时位点恰好推进一格；并发重复提交只有第一次生效，
//     后续提交因 seq <= position 被 RejectInvalidSeq 拒绝。
func (c *Committer) Commit(seq int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if seq <= 0 {
		return &RejectError{Kind: RejectInvalidSeq, Seq: seq, Position: c.position,
			Detail: "seq must be positive"}
	}
	expected := c.position + 1
	if seq != expected {
		if seq <= c.position {
			return &RejectError{Kind: RejectInvalidSeq, Seq: seq, Position: c.position,
				Detail: "seq already committed"}
		}
		return &RejectError{Kind: RejectGap, Seq: seq, Position: c.position,
			Detail: fmt.Sprintf("expect seq %d, commit skips a position", expected)}
	}
	_, ok := c.pending[seq]
	if !ok {
		return &RejectError{Kind: RejectEffectMissing, Seq: seq, Position: c.position,
			Detail: "no persisted effect for seq, prepare phase missing"}
	}

	if err := c.store.SavePosition(seq); err != nil {
		return fmt.Errorf("twophase: persist position %d: %w", seq, err)
	}
	c.position = seq
	delete(c.pending, seq)
	return nil
}

// Position 返回已提交位点。
func (c *Committer) Position() int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.position
}

// Pending 返回已写副作用但位点尚未提交的在途序号。
// 按序号升序，崩溃重启后用于重复处理（幂等重放）。
func (c *Committer) Pending() []int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	seqs := make([]int64, 0, len(c.pending))
	for seq := range c.pending {
		seqs = append(seqs, seq)
	}
	sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })
	return seqs
}

// PendingEffect 返回某在途序号已写入的副作用；不存在时返回 ("", false)。
func (c *Committer) PendingEffect(seq int64) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	action, ok := c.pending[seq]
	return action, ok
}

// Snapshot 是查询自检用的不可变状态快照。
type Snapshot struct {
	Position int64
	Pending  map[int64]string
}

// State 返回当前状态副本，可与查询/自检并发调用。
func (c *Committer) State() Snapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	pending := make(map[int64]string, len(c.pending))
	for seq, action := range c.pending {
		pending[seq] = action
	}
	return Snapshot{Position: c.position, Pending: pending}
}

// Verify 自检副作用存储与位点的连续性。
// 检查：在途序号必须恰好是 position+1..position+k，不允许有缺口或越界副本。
func (c *Committer) Verify() error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	seqs := make([]int64, 0, len(c.pending))
	for seq := range c.pending {
		if seq <= c.position {
			return fmt.Errorf("twophase: verify failed: effect %d below position %d",
				seq, c.position)
		}
		seqs = append(seqs, seq)
	}
	sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })
	for i, seq := range seqs {
		want := c.position + int64(i) + 1
		if seq != want {
			return fmt.Errorf("twophase: verify failed: expect pending seq %d, got %d (gap in effect store)",
				want, seq)
		}
	}

	// 与底层存储交叉核对：position 已持久化，且每个在途效果在存储中真实存在。
	storedPos, err := c.store.LoadPosition()
	if err != nil {
		return fmt.Errorf("twophase: verify load position: %w", err)
	}
	if storedPos != c.position {
		return fmt.Errorf("twophase: verify failed: in-memory position %d != persisted %d",
			c.position, storedPos)
	}
	for _, seq := range seqs {
		effect, err := c.store.LoadEffect(seq)
		if err != nil {
			return fmt.Errorf("twophase: verify load effect %d: %w", seq, err)
		}
		if effect == nil {
			return fmt.Errorf("twophase: verify failed: pending effect %d missing in store", seq)
		}
		if effect.Action != c.pending[seq] {
			return fmt.Errorf("twophase: verify failed: effect %d content mismatch", seq)
		}
	}
	return nil
}
