// Package consistency 提供带滞后上限的一致性读取器。
package consistency

import (
	"errors"
	"sync"
)

// Mode 表示读取模式。
type Mode int

const (
	// ModeDegraded 降级模式：立即返回当前已应用位点，并报告是否降级。
	ModeDegraded Mode = iota
	// ModeBlocking 阻塞模式：冻结目标位点并阻塞直到已应用位点推进到位。
	ModeBlocking
)

var (
	// ErrNonContiguousCommit 提交位点不连续（必须严格 +1 推进）。
	ErrNonContiguousCommit = errors.New("consistency: non-contiguous commit index")
	// ErrAppliedOutOfRange 已应用位点越界（必须单调且不超过提交位点）。
	ErrAppliedOutOfRange = errors.New("consistency: applied index out of range")
	// ErrNegativeLag 滞后上限为负。
	ErrNegativeLag = errors.New("consistency: negative max lag")
)

// Positions 是两个位点的一致性快照。
type Positions struct {
	Commit  uint64
	Applied uint64
}

// Result 是一次读取的结果。
type Result struct {
	// Positions 是读取返回时的位点快照。
	Positions Positions
	// Target 是本次读取冻结的目标位点。
	Target uint64
	// Degraded 表示是否发生了降级（仅降级模式可能为 true）。
	Degraded bool
}

// Reader 是带滞后上限的一致性读取器。
type Reader struct {
	mu      sync.Mutex
	cond    *sync.Cond
	commit  uint64
	applied uint64
}

// New 创建一个读取器，两个位点均从 0 开始。
func New() *Reader {
	r := &Reader{}
	r.cond = sync.NewCond(&r.mu)
	return r
}

// AdvanceCommit 将提交位点推进到 to。
// 提交必须连续：to 必须恰好等于当前提交位点 +1，
// 否则整体拒绝并返回 ErrNonContiguousCommit，两个位点与等待者状态均不变。
func (r *Reader) AdvanceCommit(to uint64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if to != r.commit+1 {
		return ErrNonContiguousCommit
	}
	r.commit = to
	return nil
}

// MarkApplied 将已应用位点推进到 to。
// to 必须大于当前已应用位点且不超过提交位点，
// 否则整体拒绝并返回 ErrAppliedOutOfRange，两个位点与等待者状态均不变。
// 推进成功后会唤醒全部阻塞中的等待者重新检查各自冻结的目标。
func (r *Reader) MarkApplied(to uint64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if to <= r.applied || to > r.commit {
		return ErrAppliedOutOfRange
	}
	r.applied = to
	r.cond.Broadcast()
	return nil
}

// Snapshot 返回两个位点的一致性快照，可并发调用。
// 快照在同一把锁内读取，保证逐字段来自同一时刻。
func (r *Reader) Snapshot() Positions {
	r.mu.Lock()
	defer r.mu.Unlock()
	return Positions{Commit: r.commit, Applied: r.applied}
}

// Check 自检不变量：已应用位点恒不超过提交位点。可并发调用。
func (r *Reader) Check() error {
	p := r.Snapshot()
	if p.Applied > p.Commit {
		return errors.New("consistency: applied index exceeds commit index")
	}
	return nil
}

// targetFor 由提交位点与滞后上限计算目标位点（下界为 0）。
func targetFor(commit uint64, maxLag int64) uint64 {
	lag := uint64(maxLag)
	if lag >= commit {
		return 0
	}
	return commit - lag
}

// Read 按最大滞后 maxLag 以指定模式读取一个可复现的位点。
//
// maxLag 为负时整体拒绝并返回 ErrNegativeLag，位点与等待者状态不变。
//
// 降级模式：在调用时刻冻结目标位点 target = commit - maxLag（下界 0），
// 立即返回当前已应用位点；当已应用位点未达到 target 时 Degraded 为 true。
//
// 阻塞模式：同样在调用时刻冻结 target，若已应用位点未达 target 则阻塞，
// 直到已应用位点推进到位后返回；此后提交位点再推进也不改变本次已冻结的
// target，因此返回的位点对本次调用而言始终可复现。
func (r *Reader) Read(maxLag int64, mode Mode) (Result, error) {
	if maxLag < 0 {
		return Result{}, ErrNegativeLag
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	target := targetFor(r.commit, maxLag)
	switch mode {
	case ModeDegraded:
		return Result{
			Positions: Positions{Commit: r.commit, Applied: r.applied},
			Target:    target,
			Degraded:  r.applied < target,
		}, nil
	case ModeBlocking:
		for r.applied < target {
			r.cond.Wait()
		}
		return Result{
			Positions: Positions{Commit: r.commit, Applied: r.applied},
			Target:    target,
			Degraded:  false,
		}, nil
	default:
		return Result{}, errors.New("consistency: unknown read mode")
	}
}
