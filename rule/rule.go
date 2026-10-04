// Package rule 维护数据质量规则集及其单调递增的规则版本号 rv。
// 规则集自身互斥，可被多个闸门操作并发调用。
package rule

import (
	"errors"
	"sort"
	"sync"
)

// ErrInvalidParam 表示规则参数非法（id/field 为空、lo>hi、severity 非法）。
var ErrInvalidParam = errors.New("rule: invalid parameter")

// ErrRuleNotFound 表示删除一个不存在的规则 id。
var ErrRuleNotFound = errors.New("rule: id not found")

type Severity int

const (
	Block Severity = iota + 1
	Warn
)

type Rule struct {
	ID       string
	Field    string
	Lo, Hi   int64
	Severity Severity
}

type Verdict struct {
	RV         int64
	Violations []string
	HasBlock   bool
}

// Set 是线程安全的规则集。
type Set struct {
	mu    sync.Mutex
	rules map[string]Rule
	rv    int64
}

// NewSet 创建空规则集，rv 从 0 开始。
func NewSet() *Set {
	return &Set{rules: make(map[string]Rule)}
}

// Put 按 id 新增或覆盖规则；每被接受一次（含内容相同的覆盖）rv 加 1。
func (s *Set) Put(r Rule) error {
	if r.ID == "" || r.Field == "" || r.Lo > r.Hi || (r.Severity != Block && r.Severity != Warn) {
		return ErrInvalidParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rules[r.ID] = r
	s.rv++
	return nil
}

// Drop 删除规则；规则不存在时返回 ErrRuleNotFound。
func (s *Set) Drop(id string) error {
	if id == "" {
		return ErrInvalidParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.rules[id]; !ok {
		return ErrRuleNotFound
	}
	delete(s.rules, id)
	s.rv++
	return nil
}

// RV 返回当前规则版本号。
func (s *Set) RV() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rv
}

// Eval 对字段集在当前规则集上判定一次：字段缺失或值越界即违规。
// 结果中的违规 id 按字节序升序排列；Verdict.RV 记录本次判定依据的版本。
func (s *Set) Eval(fields map[string]int64) Verdict {
	s.mu.Lock()
	defer s.mu.Unlock()

	v := Verdict{RV: s.rv}
	for id, r := range s.rules {
		val, ok := fields[r.Field]
		if !ok || val < r.Lo || val > r.Hi {
			v.Violations = append(v.Violations, id)
			if r.Severity == Block {
				v.HasBlock = true
			}
		}
	}
	sort.Strings(v.Violations)
	return v
}
