// Package check 提供朴素参照实现、结果对比及全部测试支持。
package check

import (
	"errors"
	"fmt"

	"ontology/match"
)

// ErrMismatch 表示 Rabin-Karp 结果与朴素参照不一致（假阳性/假阴性）。
var ErrMismatch = errors.New("check: match result mismatch")

// Naive 逐位置 substring 比较，返回 pattern 在 text 中的全部位置（升序）。
// 空 pattern 返回 match.ErrEmptyPattern；pattern 长于 text 返回空切片。
func Naive(text, pattern string) ([]int, error) {
	positions := []int{}
	if len(pattern) == 0 {
		return nil, match.ErrEmptyPattern
	}
	for i := 0; i+len(pattern) <= len(text); i++ {
		if text[i:i+len(pattern)] == pattern {
			positions = append(positions, i)
		}
	}
	return positions, nil
}

// Diff 比较两个位置序列，不一致时返回包裹 ErrMismatch 的错误。
func Diff(want, got []int) error {
	if len(want) != len(got) {
		return fmt.Errorf("%w: want %v got %v", ErrMismatch, want, got)
	}
	for i := range want {
		if want[i] != got[i] {
			return fmt.Errorf("%w: want %v got %v", ErrMismatch, want, got)
		}
	}
	return nil
}

// Compare 对同一输入运行 FindAll 与朴素参照并比对；空 pattern 透传哨兵错误。
func Compare(text, pattern string) error {
	want, err := Naive(text, pattern)
	if err != nil {
		return err
	}
	got, err := safeFindAll(text, pattern)
	if err != nil {
		return err
	}
	return Diff(want, got)
}

// safeFindAll 把 FindAll 的哨兵 panic 还原为 error。
func safeFindAll(text, pattern string) (positions []int, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			if e, ok := rec.(error); ok {
				err = e
			} else {
				err = fmt.Errorf("check: panic: %v", rec)
			}
		}
	}()
	return match.FindAll(text, pattern), nil
}
