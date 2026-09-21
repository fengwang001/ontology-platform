// Package wal 提供写前日志：记录先整组落盘（经调用方注入的 Sink），
// 落盘成功后才进入可重放集合；落盘失败不占用 Seq、不留任何记录。
package wal

import (
	"fmt"
	"sync"
)

// Record 是一条账本变更记录。同一笔转账的多条记录共用同一个 Txn。
type Record struct {
	Seq   uint64
	Txn   uint64
	Shard int
	Delta int64
}

// Sink 是落盘端，由调用方注入，可用于模拟落盘失败。
type Sink interface {
	Write(p []byte) (int, error)
}

// Log 是写前日志。所有方法并发安全。
type Log struct {
	mu      sync.Mutex
	sink    Sink
	records []Record // 已落盘的记录，按 Seq 升序
	lastSeq uint64
}

// New 创建一个以 s 为落盘端的日志。
func New(s Sink) *Log {
	return &Log{sink: s}
}

// Append 把一组记录原子地写入日志：整组编码进一个缓冲区、一次 Write，
// 要么全部落盘并进入可重放集合，要么全部不落（失败时不消耗 Seq）。
func (l *Log) Append(recs ...Record) error {
	if len(recs) == 0 {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	base := l.lastSeq
	staged := make([]Record, len(recs))
	for i, r := range recs {
		r.Seq = base + uint64(i) + 1
		staged[i] = r
	}
	buf := encode(staged)
	if l.sink != nil {
		n, err := l.sink.Write(buf)
		if err != nil {
			return fmt.Errorf("wal: append txn %d: %w", recs[0].Txn, err)
		}
		if n != len(buf) {
			return fmt.Errorf("wal: short write: %d/%d bytes", n, len(buf))
		}
	}
	l.records = append(l.records, staged...)
	l.lastSeq = base + uint64(len(staged))
	return nil
}

// Replay 返回 Seq 大于 from 的全部记录，按 Seq 升序。
func (l *Log) Replay(from uint64) ([]Record, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []Record
	for _, r := range l.records {
		if r.Seq > from {
			out = append(out, r)
		}
	}
	return out, nil
}

// Truncate 丢弃 Seq <= upto 的记录。
func (l *Log) Truncate(upto uint64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	kept := l.records[:0]
	for _, r := range l.records {
		if r.Seq > upto {
			kept = append(kept, r)
		}
	}
	l.records = kept
	return nil
}

// LastSeq 返回已分配的最大 Seq；没有任何记录时为 0。
func (l *Log) LastSeq() uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.lastSeq
}
