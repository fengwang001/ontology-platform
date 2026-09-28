// Package txnset 实现事务标识集合的解析、合并、差集与规范化输出。
//
// 集合按“来源 + 事务号闭区间”表示，用于复制断点续传中将源端事务集合
// 与本地已执行集合相减，得到待补执行的事务规范文本。
//
// 文本语法（不允许任何空白字符）：
//
//	set      := item (";" item)*
//	item     := source ":" interval ("," interval)*
//	interval := number | number "-" number   // 闭区间，要求 lo <= hi
//	source   := [A-Za-z][A-Za-z0-9_-]*        // 来源标识
//	number   := 0 | [1-9][0-9]*               // 十进制，禁止前导零
//
// 规范化规则：同一来源的区间取并集，重叠与相邻（hi+1 == lo）区间合并；
// 输出按来源字典序、区间升序排列，单点写作 "N"，区间写作 "N-M"。
//
// 所有导出的解析、合并、差集函数均可并发调用；集合一旦构造即不可变，
// 任何操作都不会修改已有集合的内部状态。
package txnset

import (
	"errors"
	"fmt"
)

// 可区分的错误类别，调用方可用 errors.Is 判定。
var (
	// ErrSyntax 语法错误：结构不符合文法（如缺少冒号、空区间、多余分隔符、含空白）。
	ErrSyntax = errors.New("txnset: syntax error")
	// ErrIdent 来源标识非法：为空、含非法字符或首字符不是字母。
	ErrIdent = errors.New("txnset: invalid source identifier")
	// ErrNumber 事务号非法：前导零、超出 uint64 范围或区间 lo > hi。
	ErrNumber = errors.New("txnset: invalid transaction number")
	// ErrTooManyIntervals 一段文本中的区间总数超过 MaxIntervals。
	ErrTooManyIntervals = errors.New("txnset: too many intervals")
)

// MaxIntervals 是单次解析允许的区间总数上限，防止恶意输入耗尽内存。
const MaxIntervals = 100000

// ParseError 描述解析失败的位置与原因，Unwrap 返回上述错误类别之一。
type ParseError struct {
	Pos    int    // 出错字符的字节偏移（从 0 开始）
	Reason string // 具体原因说明
	Err    error  // 错误类别（ErrSyntax / ErrIdent / ErrNumber / ErrTooManyIntervals）
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("%v at offset %d: %s", e.Err, e.Pos, e.Reason)
}

func (e *ParseError) Unwrap() error { return e.Err }

// Interval 是事务号闭区间 [Lo, Hi]，Lo <= Hi 恒成立。
type Interval struct {
	Lo, Hi uint64
}

// Set 是事务标识集合，按来源分组保存已合并、升序、互不重叠且互不相邻的区间。
// 零值是空集合，可直接使用。Set 不可变，可安全地在 goroutine 间共享。
type Set struct {
	// groups 按来源名升序排列，组内区间按 Lo 升序且已规范化。
	groups []group
}

type group struct {
	source    string
	intervals []Interval
}
