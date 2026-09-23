// Package gapfill 对对齐结果中的空桶应用填充策略。
package gapfill

import (
	"errors"

	"ontology/agg"
)

// Strategy 是空桶填充策略。
type Strategy int

const (
	// KeepMissing：空桶不出现在结果中（不是 Count=0 的记录）。
	KeepMissing Strategy = iota
	// CarryForward：空桶取前一非空桶的 Last，Count=0；开头无前置值则保持缺失。
	CarryForward
	// ZeroFill：空桶值 0、Count=0。
	ZeroFill
)

// ErrInvalidStep 表示 step 非正；ErrInvalidInput 表示桶未按起点升序或重复。
var (
	ErrInvalidStep  = errors.New("gapfill: step must be positive")
	ErrInvalidInput = errors.New("gapfill: buckets must be ascending with no duplicate start")
)

// Fill 在 first..last 的 step 网格上填补空桶。输入必须为非空桶，按起点升序、
// 无重复、且起点都落在 step 网格上，否则返回 ErrInvalidInput。
func Fill(buckets []agg.Bucket, step, first, last int64, s Strategy) ([]agg.Bucket, error) {
	if step <= 0 {
		return nil, ErrInvalidStep
	}
	var prevStart int64
	for i, b := range buckets {
		if b.Start%step != 0 || b.Start < first || b.Start > last {
			return nil, ErrInvalidInput
		}
		if i > 0 && b.Start <= prevStart {
			return nil, ErrInvalidInput
		}
		prevStart = b.Start
	}
	if first%step != 0 || last%step != 0 || last < first {
		return nil, ErrInvalidInput
	}

	have := make(map[int64]agg.Bucket, len(buckets))
	for _, b := range buckets {
		have[b.Start] = b
	}
	out := make([]agg.Bucket, 0, last/step-first/step+1)
	var carried float64
	hasCarry := false
	for t := first; ; t += step {
		if b, ok := have[t]; ok {
			out = append(out, b)
			carried, hasCarry = b.Last, true
		} else {
			switch s {
			case KeepMissing:
				// 空桶直接跳过。
			case CarryForward:
				if hasCarry {
					out = append(out, agg.Bucket{Start: t,
						First: carried, Last: carried, Min: carried,
						Max: carried, Mean: carried, Count: 0})
				}
			case ZeroFill:
				out = append(out, agg.Bucket{Start: t, Count: 0})
			}
		}
		if t == last {
			break
		}
	}
	return out, nil
}
