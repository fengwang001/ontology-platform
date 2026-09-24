// Package longest 单遍求最长配平子串的长度与起始下标。
package longest

import (
	"errors"

	"ontology/balance"
	"ontology/scan"
)

var (
	// ErrTooLong 表示输入长度超过可配置上限。
	ErrTooLong = errors.New("longest: input exceeds configured limit")
	// ErrInvalidLimit 表示上限被配置成 0 或负数。
	ErrInvalidLimit = errors.New("longest: limit must be positive")
	// ErrBadRange 表示自检时给定区间越界。
	ErrBadRange = errors.New("longest: start/length out of range")
	// ErrSubstringNotBalanced 表示自检发现目标子串并不配平。
	ErrSubstringNotBalanced = errors.New("longest: substring is not balanced")
	// ErrNotLongest 表示存在比目标区间更长的配平子串。
	ErrNotLongest = errors.New("longest: a longer balanced substring exists")
)

// Solver 持有可配置上限，对同一只读输入可并发调用。
type Solver struct {
	limit int
}

// New 用给定最大长度构造 Solver；非正上限返回 ErrInvalidLimit。
func New(limit int) (*Solver, error) {
	if limit <= 0 {
		return nil, ErrInvalidLimit
	}
	return &Solver{limit: limit}, nil
}

// Longest 单遍返回最长配平子串的起始下标与长度。
func (s *Solver) Longest(input string) (start, length int, err error) {
	if len(input) > s.limit {
		return 0, 0, ErrTooLong
	}
	var visits visitCount
	start, length, err = scanOnce(input, &visits)
	return
}

// visitCount 是非导出的字符访问计数器，不出现在任何公开接口里。
type visitCount int64

// scanOnce 单遍走栈：基准 -1，不匹配的右括号压入作新基准（见 NOTES.md）。
func scanOnce(input string, visits *visitCount) (start, length int, err error) {
	stack := []int{-1}
	for i := 0; i < len(input); i++ {
		*visits++
		kind, classifyErr := scan.Classify(input, i)
		if classifyErr != nil {
			return 0, 0, classifyErr
		}
		if kind == scan.Left {
			stack = append(stack, i)
			continue
		}
		stack = stack[:len(stack)-1]
		if len(stack) == 0 {
			stack = append(stack, i)
			continue
		}
		if candidate := i - stack[len(stack)-1]; candidate > length {
			length = candidate
			start = stack[len(stack)-1] + 1
		}
	}
	return start, length, nil
}

// SelfCheck 核验给定区间是配平子串且不存在更长者。
func (s *Solver) SelfCheck(input string, start, length int) error {
	if start < 0 || length < 0 || start+length > len(input) {
		return ErrBadRange
	}
	if length > 0 && !balance.IsBalanced(input[start:start+length]) {
		return ErrSubstringNotBalanced
	}
	bestStart, bestLength, err := s.Longest(input)
	if err != nil {
		return err
	}
	if bestLength != length {
		return ErrNotLongest
	}
	if bestLength > 0 && bestStart != start {
		return ErrNotLongest
	}
	return nil
}
