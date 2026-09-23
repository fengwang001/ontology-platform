package headers

import (
	"strings"

	"ontology/listval"
	"ontology/policy"
	"ontology/token"
)

// splitList 是 listval.Split 的内部包装，供 List 访问器使用。
func splitList(v string) ([]string, error) { return listval.Split(v) }

// check 校验并规范化一对名字/值，返回规范名。任何非法输入都返回错误，
// 调用方保证先校验后修改，因此失败时集合状态零变化。
func (s *Set) check(name, value string) (string, policy.Policy, error) {
	if !token.ValidName(name) {
		return "", policy.Policy{}, token.ErrIllegalName
	}
	if len(name) > s.cfg.MaxName {
		return "", policy.Policy{}, ErrNameTooLong
	}
	if len(value) > s.cfg.MaxValue {
		return "", policy.Policy{}, ErrValueTooLong
	}
	p := s.reg.For(name)
	if err := policy.ValidateValue(p, value); err != nil {
		return "", policy.Policy{}, err
	}
	return policy.Canonical(name, p), p, nil
}

// Add 追加一个头部（不去重，保持顺序）。值含禁止字符时拒绝且不修改集合。
func (s *Set) Add(name, value string) error {
	canon, _, err := s.check(name, value)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.ents)+1 > s.cfg.MaxHeaders {
		return ErrTooManyHeaders
	}
	s.index[keyOf(canon)] = append(s.index[keyOf(canon)], len(s.ents))
	s.ents = append(s.ents, entry{name: canon, value: value})
	return nil
}

// Set 把某名字替换为单个值（删除同名全部再追加）。先校验后修改，
// 因此值非法时集合保持原样。
func (s *Set) Set(name, value string) error {
	canon, _, err := s.check(name, value)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.delLocked(canon)
	s.index[keyOf(canon)] = append(s.index[keyOf(canon)], len(s.ents))
	s.ents = append(s.ents, entry{name: canon, value: value})
	return nil
}

// Del 删除某名字的全部出现，返回删除条数。
func (s *Set) Del(name string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.delLocked(policy.Canonical(name, s.reg.For(name)))
}

// delLocked 删除并重建索引。权威顺序是切片，索引只存位置，重建不破坏保序。
func (s *Set) delLocked(canon string) int {
	key := keyOf(canon)
	kept := s.ents[:0]
	removed := 0
	for _, e := range s.ents {
		if keyOf(e.name) == key {
			removed++
			continue
		}
		kept = append(kept, e)
	}
	if removed == 0 {
		return 0
	}
	s.ents = kept
	s.index = make(map[string][]int, len(s.index))
	for i, e := range s.ents {
		s.index[keyOf(e.name)] = append(s.index[keyOf(e.name)], i)
	}
	return removed
}

// String 返回便于调试的单行摘要。
func (s *Set) String() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var b strings.Builder
	for _, e := range s.ents {
		b.WriteString(e.name)
		b.WriteString(": ")
		b.WriteString(e.value)
		b.WriteString("; ")
	}
	return b.String()
}
