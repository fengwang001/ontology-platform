// Package wal 提供带校验与截断恢复的内存预写日志。
// 写入先按记录追加进日志，Sync 把同步点推到写入点；
// 恢复时从头扫描，只回放到同步点为止，且遇到半条记录
// 或校验不符就停在第一处损坏。wal 依赖 segment 与 codec。
package wal

import (
	"sync"

	"ontology/codec"
	"ontology/segment"
)

// Record 是一条成功回放的记录。
type Record struct {
	Offset  int    // 记录在缓冲中的起始字节偏移
	Payload []byte // 负载（副本，可安全持有）
}

// StopInfo 描述恢复为何停止、停在哪里。
type StopInfo struct {
	Offset int        // 停止点的字节偏移
	Reason codec.Kind // Truncated / Corrupt；完整回放到同步点时为 Complete
}

// WAL 是内存预写日志，并发安全。
type WAL struct {
	mu      sync.Mutex
	seg     *segment.Segment
	buf     *segment.MemBuffer
	written int // 已写入字节数
	synced  int // 已同步字节数
}

// Open 在注入的内存缓冲上打开一个 WAL。
// 结构化状态（写入点、同步点）放进程内存，缓冲本身当"磁盘"。
func Open(buf *segment.MemBuffer) *WAL {
	return &WAL{
		seg:     segment.New(buf),
		buf:     buf,
		written: buf.Len(),
	}
}

// Append 把 payload 作为一条记录追加到日志尾，
// 返回该记录的起始字节偏移。空负载合法。
func (w *WAL) Append(payload []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	off, err := w.seg.Append(payload)
	if err != nil {
		return 0, err
	}
	w.written = w.seg.Len()
	return off, nil
}

// Sync 把同步点推到当前写入点。
func (w *WAL) Sync() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.synced = w.written
}

// Written 返回当前已写入字节数。
func (w *WAL) Written() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.written
}

// Synced 返回当前已同步字节数。
func (w *WAL) Synced() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.synced
}

// Snapshot 返回底层缓冲的独立副本，用于比对历史前缀不变。
func (w *WAL) Snapshot() []byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Snapshot()
}

// Recover 从头扫描并回放，最多回放到同步点为止；
// 遇到半条记录或校验不符立即停在第一处损坏，
// 停止点之后的字节一律不解释。
// 返回已成功回放的记录序列与停止信息。
func (w *WAL) Recover() ([]Record, StopInfo) {
	w.mu.Lock()
	defer w.mu.Unlock()
	var recs []Record
	_, stop := w.seg.Scan(w.synced, func(off int, payload []byte) bool {
		recs = append(recs, Record{Offset: off, Payload: payload})
		return true
	})
	return recs, StopInfo{Offset: stop.Offset, Reason: stop.Reason}
}
