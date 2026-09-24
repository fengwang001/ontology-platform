// Package cell 表示 CSV 字段值及其在原文中的字节位置与引号标记。
// 本包不依赖工程内任何其他包。
package cell

import "errors"

// Cell 是一个字段。Quoted 区分「未加引号的空字段」与 `""`。
// Start/End 是该字段原始文本在输入中的字节区间 [Start,End)：
// 加引号字段包含两侧引号；未加引号空字段 Start==End，指向分隔符位置。
type Cell struct {
	Value  string
	Quoted bool
	Start  int
	End    int
}

// Limits 为解析上限，0 表示不限制。
type Limits struct {
	MaxFieldBytes int // 单字段解码后最大字节数（"" 算 1 个引号字符）
	MaxFields     int // 单条记录最大字段数
	MaxRecords    int // 保留的记录总数上限
}

// 五类语法/结构错误。
var (
	ErrQuoteInBare       = errors.New("bare field contains a quote")
	ErrGarbageAfterQuote = errors.New("unexpected character after closing quote")
	ErrUnclosedQuote     = errors.New("unterminated quoted field")
	ErrLoneCR            = errors.New("bare carriage return not followed by LF")
	ErrColumnCount       = errors.New("record field count differs from header")
)

// 三类上限错误。
var (
	ErrFieldTooLong   = errors.New("field exceeds maximum byte length")
	ErrTooManyFields  = errors.New("record exceeds maximum field count")
	ErrTooManyRecords = errors.New("table exceeds maximum record count")
)

// ErrTerminal 表示解析器已处于终态后再次写入。
var ErrTerminal = errors.New("parser is in terminal state")

// PosError 携带出错字节偏移（从 0 起）、记录号与字段号（从 1 起；无法判定时为 0）。
type PosError struct {
	Err    error
	Offset int
	Record int
	Field  int
}

func (e *PosError) Error() string {
	return e.Err.Error()
}

func (e *PosError) Unwrap() error { return e.Err }

// AsPosError 从错误链中取出 *PosError，不存在返回 nil。
func AsPosError(err error) *PosError {
	var pe *PosError
	if errors.As(err, &pe) {
		return pe
	}
	return nil
}
