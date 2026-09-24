// Package api 对外提供日志段稀疏位点索引的并发安全接口。
package api

import (
	"fmt"
	"sync"

	"ontology/idx"
	"ontology/segment"
)

// 四类可判定哨兵错误，互不相同。
var (
	ErrInvalid     = segment.ErrInvalid
	ErrRelOverflow = segment.ErrRelOverflow
	ErrBelowBase   = segment.ErrBelowBase
	ErrNotFound    = segment.ErrNotFound
)

// Log 是并发安全的稀疏索引日志段。
type Log struct {
	mu  sync.RWMutex
	seg *segment.Segment
}

// New 要求 base >= 0 且 indexInterval > 0。
func New(base, indexInterval int64) (*Log, error) {
	s, err := segment.New(base, indexInterval)
	if err != nil {
		return nil, err
	}
	return &Log{seg: s}, nil
}

// Append 追加记录，返回物理位置。
func (l *Log) Append(offset, size int64) (int64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.seg.Append(offset, size)
}

// Lookup 返回第一条位点 >= target 的记录的位点、物理位置与扫描记录数。
func (l *Log) Lookup(target int64) (int64, int64, int, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.seg.Lookup(target)
}

// Entries 返回全部索引条目。
func (l *Log) Entries() []idx.Entry {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.seg.Entries()
}

// SelfCheck 对内置追加与查找序列核验四条不变量，全部通过返回 nil。
func (l *Log) SelfCheck() error {
	const base, interval = 1000, 100
	seg, err := segment.New(base, interval)
	if err != nil {
		return err
	}
	// 内置追加序列：第三节八条 + 确定性生成的 200 条（含空洞、变长）。
	seq := [][2]int64{{1000, 60}, {1001, 50}, {1003, 40}, {1004, 60},
		{1007, 30}, {1008, 80}, {1010, 20}, {1012, 50}}
	off, seed := int64(1013), int64(1)
	for i := 0; i < 200; i++ {
		seed = seed*6364136223846793005 + 1442695040888963407
		seq = append(seq, [2]int64{off, int64(uint64(seed)>>33)%97 + 1})
		off += int64(uint64(seed)>>62) + 1
	}
	for _, r := range seq {
		if _, err := seg.Append(r[0], r[1]); err != nil {
			return fmt.Errorf("selfcheck append: %w", err)
		}
	}
	recs, ents := seg.Records(), seg.Entries()
	// 不变量 1：与朴素参照一致。
	last := recs[len(recs)-1].Offset
	for t := int64(base); t <= last+1; t++ {
		o, p, _, err := seg.Lookup(t)
		no, np, nerr := naive(recs, t)
		if (err == nil) != (nerr == nil) || (err == nil && (o != no || p != np)) {
			return fmt.Errorf("selfcheck naive mismatch at %d", t)
		}
	}
	// 不变量 2：索引良构——严格递增且每条目恰对应一条记录。
	for i, e := range ents {
		if i > 0 && (e.Rel <= ents[i-1].Rel || e.Pos <= ents[i-1].Pos) {
			return fmt.Errorf("selfcheck index not increasing")
		}
		found := false
		for _, r := range recs {
			if r.Pos == e.Pos && r.Offset-base == int64(e.Rel) {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("selfcheck entry %v matches no record", e)
		}
	}
	// 不变量 3：与规则重算一致。
	var want []idx.Entry
	var accum, bytes int64
	for _, r := range recs {
		if accum >= interval {
			want = append(want, idx.Entry{Rel: int32(r.Offset - base), Pos: bytes})
			accum = 0
		}
		accum += r.Size
		bytes += r.Size
	}
	if fmt.Sprint(want) != fmt.Sprint(ents) {
		return fmt.Errorf("selfcheck index != recomputed")
	}
	// 不变量 4：失败不留痕。
	before := fmt.Sprint(recs, ents)
	seg.Append(last, 10)            // 位点不严格递增
	seg.Append(last+1, 0)           // size 非法
	seg.Append(base-1, 10)          // 位点低于基位点
	seg.Append(base+2147483648, 10) // 相对位点溢出
	seg.Lookup(base - 1)            // 查找低于基位点
	if fmt.Sprint(seg.Records(), seg.Entries()) != before {
		return fmt.Errorf("selfcheck rejected op mutated state")
	}
	return nil
}

func naive(recs []segment.Record, target int64) (int64, int64, error) {
	for _, r := range recs {
		if r.Offset >= target {
			return r.Offset, r.Pos, nil
		}
	}
	return 0, 0, ErrNotFound
}
