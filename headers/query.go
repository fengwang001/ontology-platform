package headers

import (
	"fmt"

	"ontology/listval"
	"ontology/policy"
	"ontology/token"
)

// positions 返回名字的存活下标（升序），并累计比较计数。
// 调用者必须持有锁。
func (s *Set) positions(name string) []int {
	pos := s.index[token.Canonical(name)]
	s.cmps.Add(int64(len(pos)))
	return pos
}

// GetAll 按出现顺序返回同名全部值；不存在时返回 nil。
func (s *Set) GetAll(name string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	pos := s.positions(name)
	if len(pos) == 0 {
		return nil
	}
	vals := make([]string, 0, len(pos))
	for _, i := range pos {
		vals = append(vals, s.entries[i].value)
	}
	return vals
}

// Get 按 policy 归并返回单值。不存在返回 ErrNotFound（与"存在但
// 值为空"可判定区分：后者返回 ("", nil)）；策略为 Error 且重复
// 时返回 ErrDuplicate。
func (s *Set) Get(name string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	pos := s.positions(name)
	if len(pos) == 0 {
		return "", fmt.Errorf("%w: %q", ErrNotFound, name)
	}
	vals := make([]string, 0, len(pos))
	for _, i := range pos {
		vals = append(vals, s.entries[i].value)
	}
	switch s.cfg.Policies.Lookup(name).Merge {
	case policy.Last:
		return vals[len(vals)-1], nil
	case policy.Join:
		return listval.Join(vals), nil
	case policy.Error:
		if len(vals) > 1 {
			return "", fmt.Errorf("%w: %q (%d values)", ErrDuplicate, name, len(vals))
		}
		return vals[0], nil
	default: // policy.First
		return vals[0], nil
	}
}

// Has 报告名字是否存在（存在但值为空时同样为 true）。
func (s *Set) Has(name string) bool {
	return s.Count(name) > 0
}

// Count 返回名字的出现次数，不存在返回 0。
func (s *Set) Count(name string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.positions(name))
}

// Len 返回存活头部总条数。
func (s *Set) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.liveCount()
}

// TotalBytes 返回当前集合回写后的总字节数。
func (s *Set) TotalBytes() int {
	return len(s.Bytes())
}

// Normalized 报告解析或写入过程中是否发生过规范化改写。
func (s *Set) Normalized() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.normalized
}

// Compares 返回按名查找累计比较的条目数（只读观测，不影响语义）。
func (s *Set) Compares() int64 {
	return s.cmps.Load()
}
