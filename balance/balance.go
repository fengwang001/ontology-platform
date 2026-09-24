// Package balance 单遍扫描判断括号串是否整体配平。
package balance

import "ontology/scan"

// FailureType 标识第一处出错的类型。
type FailureType int

const (
	// None 未出错。
	None FailureType = iota
	// IllegalChar 出现非括号字符。
	IllegalChar
	// UnexpectedRight 右括号多于当前可匹配的左括号。
	UnexpectedRight
	// UnmatchedLeft 扫描结束仍有未匹配的左括号。
	UnmatchedLeft
)

// Result 是单遍配平判定的结果。
type Result struct {
	Balanced bool
	Failure  FailureType
	// Index 是第一处出错的下标；未匹配左括号时为串长。
	Index int
}

// Check 单遍判定整体配平，不配平时给出第一个出错位置与类型。
func Check(s string) Result {
	open := 0
	for i := 0; i < len(s); i++ {
		kind, err := scan.Classify(s, i)
		if err != nil {
			return Result{Failure: IllegalChar, Index: i}
		}
		switch kind {
		case scan.Left:
			open++
		case scan.Right:
			open--
			if open < 0 {
				return Result{Failure: UnexpectedRight, Index: i}
			}
		}
	}
	if open > 0 {
		return Result{Failure: UnmatchedLeft, Index: len(s)}
	}
	return Result{Balanced: true}
}

// IsBalanced 仅报告整体是否配平。
func IsBalanced(s string) bool {
	return Check(s).Balanced
}
