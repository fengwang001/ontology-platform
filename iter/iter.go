// Package iter 提供段内迭代器与多路归并迭代器。
package iter

import "ontology/segment"

// Entry 是迭代器吐出的一条键值条目。
type Entry struct {
	Key, Val []byte
	Deleted  bool
}

// Source 包装一个段读取器，带层级优先级（Level 小者新，同层 Seq 大者新）。
// 同一时刻只持有当前一条条目。
type Source struct {
	Level, Seq int
	r          *segment.Reader
	off        int64
	end        int64
	cur        Entry
	ok         bool
	err        error
}

func NewSource(r *segment.Reader, level, seq int) *Source {
	return &Source{Level: level, Seq: seq, r: r, off: r.DataStart(), end: r.DataEnd()}
}

// Next 推进一条；返回 false 表示耗尽（或出错，见 Err）。
func (s *Source) Next() bool {
	if s.off >= s.end {
		s.ok = false
		return false
	}
	k, v, del, next, err := s.r.DecodeAt(s.off)
	if err != nil {
		s.ok, s.err = false, err
		return false
	}
	s.cur = Entry{Key: k, Val: v, Deleted: del}
	s.off = next
	s.ok = true
	return true
}

func (s *Source) Entry() Entry { return s.cur }
func (s *Source) Valid() bool  { return s.ok }
func (s *Source) Err() error   { return s.err }
