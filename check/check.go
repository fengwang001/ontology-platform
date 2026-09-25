// Package check 提供朴素参照集合，用于对拍布隆过滤器。
package check

// Set 是真实集合参照，精确回答成员关系。
type Set struct {
	m map[string]struct{}
}

// NewSet 返回空参照集合。
func NewSet() *Set {
	return &Set{m: make(map[string]struct{})}
}

// Add 把值加入参照集合。
func (s *Set) Add(b []byte) {
	s.m[string(b)] = struct{}{}
}

// Contains 精确报告值是否在集合中。
func (s *Set) Contains(b []byte) bool {
	_, ok := s.m[string(b)]
	return ok
}
