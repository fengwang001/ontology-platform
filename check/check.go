// Package check 提供朴素参照（真实集合），用于对拍布隆过滤器。
package check

import (
	"errors"

	"ontology/bloom"
)

// ErrFalseNegative 表示对拍发现假阴性：集合中的值被过滤器判为不存在。
var ErrFalseNegative = errors.New("check: false negative")

// Set 是真实集合，作为布隆过滤器的参照实现。
type Set struct{ m map[string]struct{} }

// NewSet 返回空集合。
func NewSet() *Set { return &Set{m: make(map[string]struct{})} }

// Add 将值加入集合。
func (s *Set) Add(b []byte) { s.m[string(b)] = struct{}{} }

// Contains 报告值是否确实在集合中。
func (s *Set) Contains(b []byte) bool {
	_, ok := s.m[string(b)]
	return ok
}

// Verify 断言无假阴性：集合中的每个值，f 都必须判为可能存在。
func Verify(f *bloom.Filter, s *Set) error {
	for v := range s.m {
		if !f.MaybeContains([]byte(v)) {
			return ErrFalseNegative
		}
	}
	return nil
}
