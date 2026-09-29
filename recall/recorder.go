package recall

import (
	"context"
	"errors"
	"log/slog"
	"sync"
)

// ErrReplayOutOfBound 表示重放序号超过快照水位（该快照永远读不到这条记录）。
var ErrReplayOutOfBound = errors.New("recall: replay sequence exceeds snapshot watermark")

// ErrReplayReclaimed 表示重放位点已被回收，记录永久不可再重放。
var ErrReplayReclaimed = errors.New("recall: replay sequence has been reclaimed")

// ErrSnapshotClosed 表示重放使用了不存在（未打开或已关闭）的快照。
var ErrSnapshotClosed = errors.New("recall: snapshot does not exist or is closed")

// ErrCloseUnknownSnapshot 表示关闭了一个不存在的快照。
var ErrCloseUnknownSnapshot = errors.New("recall: cannot close unknown snapshot")

// ErrTooManySnapshots 表示打开快照数超过上限。
var ErrTooManySnapshots = errors.New("recall: snapshot limit exceeded")

// Record 是一条按序号递增追加的撤回记录。
type Record struct {
	Seq     int64
	Payload []byte
}

// Recorder 是按水位回收的撤回日志回收器。零值不可用，须用 New 构造。
type Recorder struct {
	mu           sync.RWMutex
	log          *slog.Logger
	maxSnapshots int

	nextSeq int64            // 当前最大序号，也是下一条记录的序号
	nextID  uint64           // 下一个快照句柄
	snaps   map[uint64]int64 // 活跃快照：句柄 -> 打开时水位
	records map[int64][]byte // 未回收记录：序号 -> 负载
	gcSeq   int64            // 已回收位点上界（含等号），单调不减
}

// New 创建回收器。maxSnapshots<=0 表示不限制快照数量；logger 为 nil 时使用默认 logger。
func New(maxSnapshots int, logger *slog.Logger) *Recorder {
	if logger == nil {
		logger = slog.Default()
	}
	return &Recorder{
		log:          logger,
		maxSnapshots: maxSnapshots,
		snaps:        make(map[uint64]int64),
		records:      make(map[int64][]byte),
	}
}

// Append 追加一条撤回记录，返回其分配到的单调递增序号（从 1 开始）。
func (r *Recorder) Append(ctx context.Context, payload []byte) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.nextSeq++
	seq := r.nextSeq
	r.records[seq] = append([]byte(nil), payload...)
	r.log.LogAttrs(ctx, slog.LevelInfo, "recall.append",
		slog.Int64("seq", seq),
		slog.Int("payload_bytes", len(payload)),
		slog.String("result", "appended"),
		slog.String("decision", "assigned monotonically increasing seq"),
		slog.Int64("result_bound", r.boundLocked()),
	)
	return seq, nil
}

// OpenSnapshot 在当前序号水位打开一个读快照，返回快照句柄与其水位。
func (r *Recorder) OpenSnapshot(ctx context.Context) (uint64, int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.maxSnapshots > 0 && len(r.snaps) >= r.maxSnapshots {
		err := ErrTooManySnapshots
		r.log.LogAttrs(ctx, slog.LevelWarn, "recall.open_snapshot",
			slog.Int("active", len(r.snaps)),
			slog.Int("max", r.maxSnapshots),
			slog.String("result", "rejected"),
			slog.String("reason", err.Error()),
			slog.String("decision", "state unchanged"),
		)
		return 0, 0, err
	}

	r.nextID++
	id := r.nextID
	watermark := r.nextSeq
	r.snaps[id] = watermark
	r.log.LogAttrs(ctx, slog.LevelInfo, "recall.open_snapshot",
		slog.Uint64("snapshot_id", id),
		slog.Int64("watermark", watermark),
		slog.Int("active", len(r.snaps)),
		slog.String("result", "opened"),
		slog.String("decision", "watermark pinned to current max seq"),
		slog.Int64("result_bound", r.boundLocked()),
	)
	return id, watermark, nil
}

// CloseSnapshot 关闭快照并将其移出活跃集合，随后重新评估最小水位。
func (r *Recorder) CloseSnapshot(ctx context.Context, id uint64) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.snaps[id]; !ok {
		err := ErrCloseUnknownSnapshot
		r.log.LogAttrs(ctx, slog.LevelWarn, "recall.close_snapshot",
			slog.Uint64("snapshot_id", id),
			slog.String("result", "rejected"),
			slog.String("reason", err.Error()),
			slog.String("decision", "state unchanged"),
		)
		return err
	}

	delete(r.snaps, id)
	r.log.LogAttrs(ctx, slog.LevelInfo, "recall.close_snapshot",
		slog.Uint64("snapshot_id", id),
		slog.Int("active", len(r.snaps)),
		slog.String("result", "closed"),
		slog.String("decision", "min watermark recomputed after removal"),
		slog.Int64("result_bound", r.boundLocked()),
	)
	return nil
}

// Replay 重放指定快照可见区间内某一位点的记录。
// 判定顺序：快照不存在 -> 位点已回收（含非正序号）-> 位点超过快照水位。
func (r *Recorder) Replay(ctx context.Context, id uint64, seq int64) (Record, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	watermark, ok := r.snaps[id]
	if !ok {
		err := ErrSnapshotClosed
		r.log.LogAttrs(ctx, slog.LevelWarn, "recall.replay",
			slog.Uint64("snapshot_id", id),
			slog.Int64("seq", seq),
			slog.String("result", "rejected"),
			slog.String("reason", err.Error()),
			slog.String("decision", "state unchanged"),
		)
		return Record{}, err
	}
	if seq <= r.gcSeq {
		err := ErrReplayReclaimed
		r.log.LogAttrs(ctx, slog.LevelWarn, "recall.replay",
			slog.Uint64("snapshot_id", id),
			slog.Int64("seq", seq),
			slog.Int64("gc_watermark", r.gcSeq),
			slog.Int64("snapshot_watermark", watermark),
			slog.String("result", "rejected"),
			slog.String("reason", err.Error()),
			slog.String("decision", "state unchanged"),
		)
		return Record{}, err
	}
	if seq > watermark {
		err := ErrReplayOutOfBound
		r.log.LogAttrs(ctx, slog.LevelWarn, "recall.replay",
			slog.Uint64("snapshot_id", id),
			slog.Int64("seq", seq),
			slog.Int64("gc_watermark", r.gcSeq),
			slog.Int64("snapshot_watermark", watermark),
			slog.String("result", "rejected"),
			slog.String("reason", err.Error()),
			slog.String("decision", "state unchanged"),
		)
		return Record{}, err
	}

	rec := Record{Seq: seq, Payload: append([]byte(nil), r.records[seq]...)}
	r.log.LogAttrs(ctx, slog.LevelInfo, "recall.replay",
		slog.Uint64("snapshot_id", id),
		slog.Int64("seq", seq),
		slog.Int64("snapshot_watermark", watermark),
		slog.String("result", "replayed"),
		slog.String("decision", "gc_watermark < seq <= snapshot watermark"),
	)
	return rec, nil
}

// ReclaimBound 返回当前回收上界：活跃快照水位的最小值；无活跃快照时为当前最大序号。
func (r *Recorder) ReclaimBound(ctx context.Context) int64 {
	r.mu.RLock()
	defer r.mu.RUnlock()

	bound := r.boundLocked()
	r.log.LogAttrs(ctx, slog.LevelDebug, "recall.reclaim_bound",
		slog.Int("active", len(r.snaps)),
		slog.Int64("bound", bound),
		slog.String("decision", "min active watermark, or max seq when no active snapshot"),
	)
	return bound
}

// Reclaim 回收所有序号不超过回收上界（含等号）的记录，返回回收条数与回收上界。
// 回收后的记录永久不可再重放；回收位点单调不回退。
func (r *Recorder) Reclaim(ctx context.Context) (reclaimed int, bound int64, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	bound = r.boundLocked()
	for seq := r.gcSeq + 1; seq <= bound; seq++ {
		if _, ok := r.records[seq]; ok {
			delete(r.records, seq)
			reclaimed++
		}
	}
	r.gcSeq = bound
	r.log.LogAttrs(ctx, slog.LevelInfo, "recall.reclaim",
		slog.Int("active", len(r.snaps)),
		slog.Int64("bound", bound),
		slog.Int("reclaimed", reclaimed),
		slog.Int("retained", len(r.records)),
		slog.String("decision", "delete all seq<=bound inclusively; gc watermark never regresses"),
	)
	return reclaimed, bound, nil
}

// boundLocked 计算回收上界。调用方须持有锁（读锁或写锁均可）。
func (r *Recorder) boundLocked() int64 {
	if len(r.snaps) == 0 {
		return r.nextSeq
	}
	bound := r.nextSeq
	for _, w := range r.snaps {
		if w < bound {
			bound = w
		}
	}
	return bound
}
