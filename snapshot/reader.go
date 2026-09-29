package snapshot

import (
	"errors"
	"fmt"
	"sync"
)

// 可区分的失败原因：调用方可用 errors.Is 判定。一次失败不会改动日志、快照点与快照内容。
var (
	// ErrEmptyKey 表示读或写使用了空键。
	ErrEmptyKey = errors.New("snapshot: key must not be empty")
	// ErrEmptyValue 表示写入了空值。
	ErrEmptyValue = errors.New("snapshot: value must not be empty")
	// ErrReadBeforeSnapshot 表示读位点早于当前快照点，该位点历史已无法据此双读还原。
	ErrReadBeforeSnapshot = errors.New("snapshot: read position is before the latest snapshot point")
	// ErrInvalidPosition 表示读位点为负数等非法值。
	ErrInvalidPosition = errors.New("snapshot: read position must be non-negative")
	// ErrSnapshotMismatch 表示自检发现快照内容与从头重放结果不一致。
	ErrSnapshotMismatch = errors.New("snapshot: immutable snapshot does not match replayed log")
)

// Record 是日志中的一条不可变记录；seq 单调递增，从 1 开始。
type Record struct {
	Seq   int64
	Key   string
	Value string
}

// ReadResult 是按位点读的结果，逐字段可复现。
type ReadResult struct {
	Key      string
	Value    string
	Exists   bool
	Position int64
	// BaseSeq 是本次读使用的快照点；BaseSeq 之前的值由快照覆盖。
	BaseSeq int64
}

// Reader 是快照加增量的双读一致性读取器。零值不可用，请用 New。
type Reader struct {
	mu sync.RWMutex
	// log 是追加写日志，永不清空；log[i].Seq == i+1。
	log []Record
	// snapSeq 为快照点；snap 固化了 seq in [1, snapSeq] 内每个键的最新值。
	snapSeq int64
	snap    map[string]string
}

// New 创建读取器。
func New() *Reader {
	return &Reader{snap: map[string]string{}}
}

// Write 追加一条写记录，返回其单调递增序号。
func (r *Reader) Write(key, value string) (int64, error) {
	if key == "" {
		return 0, ErrEmptyKey
	}
	if value == "" {
		return 0, ErrEmptyValue
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	seq := int64(len(r.log)) + 1
	r.log = append(r.log, Record{Seq: seq, Key: key, Value: value})
	return seq, nil
}

// Freeze 冻结当前序号，并把此前逐键最新值固化成不可变副本；日志不清空。
func (r *Reader) Freeze() (snapshotSeq int64, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	current := int64(len(r.log))
	frozen := make(map[string]string, len(r.snap)+len(r.log))
	for k, v := range r.snap {
		frozen[k] = v
	}
	for i := r.snapSeq; i < current; i++ {
		frozen[r.log[i].Key] = r.log[i].Value
	}
	r.snap = frozen
	r.snapSeq = current
	return r.snapSeq, nil
}

// ReadAt 以快照为基，按序应用快照点之后、位点之前（含）的增量，返回该键在该位点的值。
func (r *Reader) ReadAt(key string, position int64) (ReadResult, error) {
	if key == "" {
		return ReadResult{}, ErrEmptyKey
	}
	if position < 0 {
		return ReadResult{}, ErrInvalidPosition
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.readAtLocked(key, position)
}

// readAtLocked 执行双读合并；调用方必须持有 r.mu（读锁即可）。
func (r *Reader) readAtLocked(key string, position int64) (ReadResult, error) {
	if position < r.snapSeq {
		return ReadResult{}, fmt.Errorf("%w: requested=%d snapshot=%d",
			ErrReadBeforeSnapshot, position, r.snapSeq)
	}
	current := int64(len(r.log))
	readUpTo := position
	if readUpTo > current {
		// 位点早于未来：尚无 [current+1, position] 的写入，按已知最新位点回答。
		readUpTo = current
	}
	value, exists := r.snap[key]
	for i := r.snapSeq; i < readUpTo; i++ {
		rec := r.log[i]
		if rec.Key == key {
			value, exists = rec.Value, true
		}
	}
	return ReadResult{
		Key:      key,
		Value:    value,
		Exists:   exists,
		Position: position,
		BaseSeq:  r.snapSeq,
	}, nil
}

// CurrentSeq 返回当前最新写序号（无写时为 0）。
func (r *Reader) CurrentSeq() int64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return int64(len(r.log))
}

// SnapshotSeq 返回最近一次快照点（无快照时为 0）。
func (r *Reader) SnapshotSeq() int64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.snapSeq
}
