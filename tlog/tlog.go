// Package tlog 是只追加日志本体：位点分配、时间戳校验、容量上限、
// 批量追加原子性；追加时维护 tidx 时间索引，Lookup 经索引二分定位。
package tlog

import (
	"errors"
	"ontology/tidx"
	"sync"
)

// 三类可判定、互不相同的哨兵错误。
var (
	ErrInvalidParam = errors.New("tlog: base 为负或 maxMsgs 非正")
	ErrNegativeTS   = errors.New("tlog: 时间戳为负")
	ErrCapacity     = errors.New("tlog: 追加后消息数超过 maxMsgs")
)

// Log 是进程内只追加日志。
type Log struct {
	mu      sync.RWMutex
	base    int64
	leo     int64
	maxMsgs int64
	ts      []int64 // 按位点顺序的原始时间戳，仅追加路径与自检对照用
	ix      *tidx.Index
}

// New 以起始位点 base 与容量上限 maxMsgs 构造空日志。
func New(base int64, maxMsgs int) (*Log, error) {
	if base < 0 || maxMsgs <= 0 {
		return nil, ErrInvalidParam
	}
	return &Log{base: base, leo: base, maxMsgs: int64(maxMsgs), ix: tidx.New()}, nil
}

// Append 原子批量追加：先整批校验（时间戳负数、容量），全部通过才改任何状态；
// 返回本批首条位点。任一条被拒则整批不生效。
func (l *Log) Append(ts []int64) (int64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, t := range ts {
		if t < 0 {
			return 0, ErrNegativeTS
		}
	}
	if int64(len(l.ts))+int64(len(ts)) > l.maxMsgs {
		return 0, ErrCapacity
	}
	first := l.leo
	for _, t := range ts {
		l.ix.Add(t, l.leo)
		l.ts = append(l.ts, t)
		l.leo++
	}
	return first, nil
}

// Lookup 返回位点最小的、TS >= t 的消息位点；
// 无任何命中（含空日志）返回 (LEO,false)。经时间索引二分，不逐条扫描。
func (l *Log) Lookup(t int64) (int64, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if off, ok := l.ix.Lookup(t); ok {
		return off, true
	}
	return l.leo, false
}

// LEO 返回日志结束位点（下一条将写入的位点，空日志等于 base）。
func (l *Log) LEO() int64 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.leo
}

// Base 返回起始位点，供上层自检做朴素对照。
func (l *Log) Base() int64 { return l.base }

// ProbeWithinBound 以布尔结论报告最近一次 Lookup 探查数是否在二分上界内。
func (l *Log) ProbeWithinBound() bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.ix.ProbeWithinBound()
}

// IndexEntries 返回当前时间索引快照。
func (l *Log) IndexEntries() []tidx.Entry {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.ix.Entries()
}
