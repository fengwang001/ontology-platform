// Package rule 维护数据质量规则集与规则版本，并对记录字段求值。
package rule

import (
	"errors"
	"fmt"
	"sort"
)

// ErrInvalid 表示参数非法。
var ErrInvalid = errors.New("rule: invalid argument")

// ErrNoRule 表示要删除的规则不存在。
var ErrNoRule = errors.New("rule: rule not found")

// Severity 为规则严重级别：Block 违规即不通过，Warn 仅记录。
type Severity int

const (
	Block Severity = iota
	Warn
)

// Rule 描述一条区间规则：字段缺失或值不在闭区间 [Lo,Hi] 内即违规。
type Rule struct {
	ID       string
	Field    string
	Lo       int64
	Hi       int64
	Severity Severity
}

// Store 为规则集，rv 为规则版本，每个被接受的变更加 1。
type Store struct {
	rules map[string]Rule
	rv    uint64
}

// NewStore 返回空规则集，rv 从 0 起。
func NewStore() *Store { return &Store{rules: make(map[string]Rule)} }

// RV 返回当前规则版本。
func (s *Store) RV() uint64 { return s.rv }

// Put 按 id 新增或覆盖规则；参数非法时不改状态、不增版本。
func (s *Store) Put(r Rule) error {
	if r.ID == "" || r.Field == "" || r.Lo > r.Hi || (r.Severity != Block && r.Severity != Warn) {
		return fmt.Errorf("%w: %+v", ErrInvalid, r)
	}
	s.rules[r.ID] = r
	s.rv++
	return nil
}

// Drop 按 id 删除规则；规则不存在时报 ErrNoRule，不改状态、不增版本。
func (s *Store) Drop(id string) error {
	if id == "" {
		return fmt.Errorf("%w: empty rule id", ErrInvalid)
	}
	if _, ok := s.rules[id]; !ok {
		return fmt.Errorf("%w: %q", ErrNoRule, id)
	}
	delete(s.rules, id)
	s.rv++
	return nil
}

// Eval 对一条记录按当前规则集求值一次，返回违规规则 id 的升序列表。
func (s *Store) Eval(fields map[string]int64) []string {
	var out []string
	for id, r := range s.rules {
		v, ok := fields[r.Field]
		if !ok || v < r.Lo || v > r.Hi {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// HasBlock 报告违规列表中是否含 Block 级规则（即判定不通过）。
func (s *Store) HasBlock(violations []string) bool {
	for _, id := range violations {
		if s.rules[id].Severity == Block {
			return true
		}
	}
	return false
}
