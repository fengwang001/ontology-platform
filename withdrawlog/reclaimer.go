// Package withdrawlog 实现按水位回收的撤回日志回收器。
package withdrawlog

import (
	"errors"
	"sync"
)

// SnapshotID 标识一个活跃读快照。
type SnapshotID uint64

// Record 是一条按序号追加的撤回记录。
type Record struct {
	Seq     int64
	Payload string
}

// Reclaimer 按活跃读快照水位回收撤回记录。
//
// 所有方法均可被并发调用：追加 / 打开 / 关闭 / 回收互斥串行化，
// 重放与回收上界查询使用读锁可并发执行。
type Reclaimer struct {
	mu sync.RWMutex

	maxSnapshots int

	// records 仅保留尚未回收的记录，键为序号。
	records map[int64]Record
	// nextSeq 是下一条追加记录的序号，序号从 1 开始单调递增。
	nextSeq int64
	// lastSeq 是最近一次追加的序号（无记录时为 0）。
	lastSeq int64
	// watermark 是已永久回收的上界：所有 seq <= watermark 的记录均不可再重放。
	// 它只会单调不减。
	watermark int64

	// snapshots 记录每个活跃快照打开时的水位（可读记录的最大序号）。
	snapshots map[SnapshotID]int64
	nextID    SnapshotID
}

// 四类可判定错误，互不相同，调用方可通过 errors.Is 判定。
var (
	// ErrReplayOutOfRange 重放位点越界（<=0 或大于调用方快照水位）。
	ErrReplayOutOfRange = errors.New("withdrawlog: replay position out of range")
	// ErrReplayReclaimed 重放位点已被永久回收。
	// ErrReplayReclaimed 重放位点已被永久回收。
	ErrReplayReclaimed = errors.New("withdrawlog: replay position already reclaimed")
	// ErrSnapshotNotFound 关闭了不存在（或已关闭）的快照。
	ErrSnapshotNotFound = errors.New("withdrawlog: snapshot not found")
	// ErrTooManySnapshots 并发活跃快照数超过上限。
	ErrTooManySnapshots = errors.New("withdrawlog: too many active snapshots")
)

// DefaultMaxSnapshots 是 New 默认允许的最大并发活跃快照数。
const DefaultMaxSnapshots = 64

// New 创建回收器；maxSnapshots<=0 时使用 DefaultMaxSnapshots。
func New(maxSnapshots int) *Reclaimer {
	if maxSnapshots <= 0 {
		maxSnapshots = DefaultMaxSnapshots
	}
	return &Reclaimer{
		maxSnapshots: maxSnapshots,
		records:      make(map[int64]Record),
		nextSeq:      1,
		snapshots:    make(map[SnapshotID]int64),
	}
}

// Append 追加一条撤回记录，返回其单调递增序号（从 1 开始）。
func (r *Reclaimer) Append(payload string) int64 {
	r.mu.Lock()
	defer r.mu.Unlock()

	seq := r.nextSeq
	r.nextSeq++
	r.lastSeq = seq
	r.records[seq] = Record{Seq: seq, Payload: payload}
	return seq
}

// OpenSnapshot 在当前水位打开快照，返回快照标识。
// 快照水位等于打开时的当前最大序号，可重放所有 seq <= 水位且未被回收的记录。
func (r *Reclaimer) OpenSnapshot() (SnapshotID, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if len(r.snapshots) >= r.maxSnapshots {
		return 0, ErrTooManySnapshots
	}

	id := r.nextID
	r.nextID++
	// 快照在 append 之后打开，故其水位 >= watermark，不会看到已回收位点。
	r.snapshots[id] = r.lastSeq
	return id, nil
}

// CloseSnapshot 关闭快照并立即重估回收上界。
func (r *Reclaimer) CloseSnapshot(id SnapshotID) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.snapshots[id]; !ok {
		return ErrSnapshotNotFound
	}
	delete(r.snapshots, id)
	return nil
}

// Reclaim 回收所有序号不超过回收上界（含等号）的记录，返回实际回收的最大序号。
// 无活跃快照时回收上界为当前最大序号，即全部回收。回收后的记录永久不可再重放。
func (r *Reclaimer) Reclaim() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()

	bound := r.upperBoundLocked()
	for seq := range r.records {
		if seq <= bound {
			delete(r.records, seq)
		}
	}
	// upperBoundLocked 保证 bound >= watermark，水位因此单调不回退。
	if bound > r.watermark {
		r.watermark = bound
	}
	return bound
}

// ReclaimUpperBound 返回当前回收上界：活跃快照水位最小值；无活跃快照时为当前最大序号。
func (r *Reclaimer) ReclaimUpperBound() int64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.upperBoundLocked()
}

// upperBoundLocked 计算回收上界，调用方须持有 r.mu（读锁或写锁）。
func (r *Reclaimer) upperBoundLocked() int64 {
	if len(r.snapshots) == 0 {
		// 无活跃快照：上界为当前最大序号（与 watermark 相同也无妨，取 max 保持单调）。
		return r.lastSeq
	}
	bound := r.lastSeq
	for _, w := range r.snapshots {
		if w < bound {
			bound = w
		}
	}
	return bound
}

// Replay 在指定快照上重放 seq 处的记录。
// seq<=0 或 seq 超过快照水位返回 ErrReplayOutOfRange；
// seq 已被回收返回 ErrReplayReclaimed；快照不存在返回 ErrSnapshotNotFound。
func (r *Reclaimer) Replay(snapshot SnapshotID, seq int64) (Record, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	water, ok := r.snapshots[snapshot]
	if !ok {
		return Record{}, ErrSnapshotNotFound
	}
	if seq <= 0 || seq > water {
		return Record{}, ErrReplayOutOfRange
	}
	if seq <= r.watermark {
		return Record{}, ErrReplayReclaimed
	}
	rec, ok := r.records[seq]
	if !ok {
		// 理论上不可达：水位之下、watermark 之上的记录必然存在。
		return Record{}, ErrReplayReclaimed
	}
	return rec, nil
}
