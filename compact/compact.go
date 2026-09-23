// Package compact 把若干层的段合并成新段，丢弃被覆盖的旧版本。
package compact

import (
	"errors"
	"ontology/iter"
	"ontology/level"
	"ontology/segment"
	"os"
	"sort"
)

var ErrNothingToCompact = errors.New("compact: no segments in given levels")

// canDropTombstone 推导：当且仅当从源层到最深层之间的每一层都全部
// 参与合并时，删除标记才可物理丢弃，否则更深层旧值会复活。
func canDropTombstone(s *level.Store, from []int, toLevel int) bool {
	if toLevel != s.MaxLevel() {
		return false
	}
	in := map[int]bool{}
	for _, l := range from {
		in[l] = true
	}
	sort.Ints(from)
	for l := from[0]; l <= toLevel; l++ {
		if !in[l] {
			return false
		}
	}
	return true
}

// Compact 把 fromLevels 各层的全部段合并为 toLevel 层的一个新段。
// 顺序：写 .tmp → fsync → 原子 rename → 锁内换状态 → 删旧段，无读空窗。
func Compact(s *level.Store, fromLevels []int, toLevel int) (level.Meta, error) {
	snap := s.Snapshot()
	var old []level.Meta
	for _, l := range fromLevels {
		old = append(old, snap[l]...)
	}
	if len(old) == 0 {
		return level.Meta{}, ErrNothingToCompact
	}
	drop := canDropTombstone(s, fromLevels, toLevel)
	seq := s.AllocSeq()
	final := s.SegName(toLevel, seq)
	tmp := final + ".tmp"
	w, err := segment.NewWriter(tmp)
	if err != nil {
		return level.Meta{}, err
	}
	var srcs []*iter.Source
	for _, m := range old {
		srcs = append(srcs, iter.NewSource(s.Reader(m.Path), m.Level, m.Seq))
	}
	merger := iter.NewMerger(srcs, true) // 保留删除标记，由 drop 决定是否丢弃
	for e, ok := merger.Next(); ok; e, ok = merger.Next() {
		if e.Deleted && drop {
			continue // 已覆盖所有可能层级，物理丢弃
		}
		var err error
		if e.Deleted {
			err = w.Delete(e.Key)
		} else {
			err = w.Add(e.Key, e.Val)
		}
		if err != nil {
			w.Close()
			os.Remove(tmp)
			return level.Meta{}, err
		}
	}
	if err := w.Close(); err != nil {
		os.Remove(tmp)
		return level.Meta{}, err
	}
	if err := os.Rename(tmp, final); err != nil {
		os.Remove(tmp)
		return level.Meta{}, err
	}
	meta, r, err := level.Load(final, toLevel, seq)
	if err != nil {
		return level.Meta{}, err
	}
	s.Replace(old, meta, r)
	return meta, nil
}
