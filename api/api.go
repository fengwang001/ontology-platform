// Package api 对外提供稀疏位点索引日志段的并发安全接口。依赖 segment。
package api

import (
	"errors"
	"fmt"
	"sync"

	"ontology/idx"
	"ontology/segment"
)

// 可判定的哨兵错误，互不相同。
var (
	ErrInvalid   = segment.ErrInvalid
	ErrOverflow  = segment.ErrOverflow
	ErrBelowBase = segment.ErrBelowBase
	ErrNotFound  = segment.ErrNotFound
)

// Entry 是一条索引条目（int32 相对位点 + 物理位置）。
type Entry = idx.Entry

// Index 是并发安全的稀疏位点索引日志段。
type Index struct {
	mu  sync.RWMutex
	seg *segment.Segment
}

// New 要求 base >= 0 且 indexInterval > 0。
func New(base, indexInterval int64) (*Index, error) {
	s, err := segment.New(base, indexInterval)
	if err != nil {
		return nil, err
	}
	return &Index{seg: s}, nil
}

// Append 追加一条记录，返回其物理位置。
func (ix *Index) Append(offset, size int64) (int64, error) {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	return ix.seg.Append(offset, size)
}

// Lookup 返回第一条位点 >= target 的记录的位点与物理位置。
func (ix *Index) Lookup(target int64) (int64, int64, error) {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return ix.seg.Lookup(target)
}

// Entries 返回全部索引条目。
func (ix *Index) Entries() []Entry {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return ix.seg.Entries()
}

// naiveLookup 朴素参照：从位置 0 起逐条扫描。
func naiveLookup(recs []segment.Record, target int64) (int64, int64, error) {
	for _, r := range recs {
		if r.Offset >= target {
			return r.Offset, r.Pos, nil
		}
	}
	return 0, 0, ErrNotFound
}

// recompute 按追加规则对记录序列从头重算索引。
func recompute(recs []segment.Record, base, interval int64) []Entry {
	var out []Entry
	var acc int64
	for _, r := range recs {
		if acc >= interval {
			out = append(out, Entry{Rel: int32(r.Offset - base), Pos: r.Pos})
			acc = 0
		}
		acc += r.Size
	}
	return out
}

// SelfCheck 用内置追加与查找序列核验四条不变量，与接收者状态无关。
func (ix *Index) SelfCheck() error {
	const base, interval = 1000, 100
	seq := [][2]int64{
		{1000, 60}, {1001, 50}, {1003, 40}, {1004, 60},
		{1007, 30}, {1008, 80}, {1010, 20}, {1012, 50},
	}
	s, err := segment.New(base, interval)
	if err != nil {
		return err
	}
	for _, r := range seq {
		if _, err := s.Append(r[0], r[1]); err != nil {
			return err
		}
	}
	recs := s.Records()
	last := recs[len(recs)-1].Offset
	// 不变量 1：与朴素参照一致。
	for target := int64(base); target <= last+1; target++ {
		o, p, err := s.Lookup(target)
		no, np, nerr := naiveLookup(recs, target)
		if o != no || p != np || !errors.Is(err, nerr) {
			return fmt.Errorf("selfcheck: invariant 1 violated at target %d", target)
		}
	}
	// 不变量 2：索引良构。
	ents := s.Entries()
	at := map[int64]int64{} // 物理位置 -> 相对位点
	for _, r := range recs {
		at[r.Pos] = r.Offset - base
	}
	for i, e := range ents {
		rel, ok := at[e.Pos]
		if !ok || rel != int64(e.Rel) {
			return fmt.Errorf("selfcheck: invariant 2 violated at entry %d", i)
		}
		if i > 0 && (e.Rel <= ents[i-1].Rel || e.Pos <= ents[i-1].Pos) {
			return fmt.Errorf("selfcheck: invariant 2 violated at entry %d", i)
		}
	}
	// 不变量 3：与规则重算一致。
	if fmt.Sprint(ents) != fmt.Sprint(recompute(recs, base, interval)) {
		return errors.New("selfcheck: invariant 3 violated")
	}
	// 不变量 4：失败不留痕。
	snapshot := func() ([]segment.Record, []idx.Entry, int64) {
		return s.Records(), s.Entries(), s.Bytes()
	}
	b0, b1, b2 := snapshot()
	// 依次尝试：offset<base、位点不递增、size<=0、相对位点溢出、查找低于基位点。
	for _, a := range [][2]int64{{base - 1, 10}, {last, 10}, {last + 1, 0}, {base + 2147483648, 10}} {
		s.Append(a[0], a[1])
	}
	s.Lookup(base - 1)
	if _, err := segment.New(-1, 1); !errors.Is(err, ErrInvalid) {
		return errors.New("selfcheck: invariant 4 violated (New)")
	}
	a0, a1, a2 := snapshot()
	if fmt.Sprint(b0) != fmt.Sprint(a0) || fmt.Sprint(b1) != fmt.Sprint(a1) || b2 != a2 {
		return errors.New("selfcheck: invariant 4 violated")
	}
	return nil
}
