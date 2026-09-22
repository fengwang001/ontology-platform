// Package wal 提供带校验与截断恢复的内存预写日志。
//
// 写入先按记录追加进日志；Sync 把同步点推到当前写入点；
// 恢复时从头扫描，只回放到同步点为止，遇到半条记录或
// 校验不符就停在第一处损坏，停止点之后的字节一律不解释。
package wal

import (
	"sync"

	"ontology/segment"
)

// Entry 是恢复时回放出来的一条记录。
type Entry struct {
	Offset  int64  // 记录在缓冲中的起始偏移
	Payload []byte // 负载（独立副本，零长度负载为空的非 nil 切片）
}

// Recovery 是一次恢复回放的完整结论。
type Recovery struct {
	Entries []Entry            // 已成功回放的记录序列
	Report  segment.ScanReport // 停在第几字节、因为什么停
}

// Log 是对外预写日志，管理写入、同步点与恢复回放。
// 所有方法并发安全。
type Log struct {
	dev *segment.Device
	seg *segment.Segment

	mu     sync.RWMutex
	synced int64 // 已同步字节数
}

// Open 在注入的 dev 上打开一条日志。dev 由调用方持有，
// 测试可借它截断、翻转字节或读取快照。
func Open(dev *segment.Device) *Log {
	return &Log{dev: dev, seg: segment.New(dev)}
}

// Append 把 payload 追加为一条新记录，返回记录起始偏移。
// 零长度负载合法。追加不修改任何已写入字节。
func (l *Log) Append(payload []byte) int64 {
	return l.seg.Append(payload)
}

// WritePosition 返回当前已写入字节数。
func (l *Log) WritePosition() int64 {
	return l.dev.Len()
}

// SyncedPosition 返回已同步字节数。
func (l *Log) SyncedPosition() int64 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.synced
}

// Sync 把同步点推到当前写入点。
func (l *Log) Sync() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.synced = l.dev.Len()
}

// Recover 从头扫描到同步点为止，返回回放出的记录序列与停止结论。
// 同步点之后的记录即使完整也不回放；遇到半条记录或校验不符
// 立即停下，停止点之后的字节一律不解释。
func (l *Log) Recover() Recovery {
	limit := l.SyncedPosition()
	rec := Recovery{}
	rec.Report = l.seg.Scan(0, limit, func(off int64, payload []byte) bool {
		rec.Entries = append(rec.Entries, Entry{Offset: off, Payload: payload})
		return true
	})
	// 同步点跟进恢复进度，避免下次重复扫描已恢复区域。
	l.mu.Lock()
	l.synced = rec.Report.StopAt
	l.mu.Unlock()
	return rec
}
