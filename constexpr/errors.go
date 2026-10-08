package constexpr

import "fmt"

// EvalCode 是求值/登记错误的可区分分类。
type EvalCode uint8

const (
	EvalOK               EvalCode = iota
	EvalInvalidArgument           // 参数非法：结构非法、未知类型名、空名字
	EvalDuplicateName             // 重复登记
	EvalUnknownName               // 引用未登记的名字
	EvalTypeMismatch              // 两个有类型操作数类型不一致
	EvalIllegalOp                 // 非法操作：种类不支持、非法移位
	EvalDivideByZero              // 除零 / 对零取余
	EvalTruncation                // 转换整型时值不是整数
	EvalOverflow                  // 越界 / 舍入到无穷
	EvalConstantTooLarge          // 无类型整数超过 512 位
)

// EvalError 携带错误分类与可读信息。
type EvalError struct {
	Code    EvalCode
	Message string
}

func (e *EvalError) Error() string { return e.Message }

func (c EvalCode) String() string {
	if int(c) < len(evalCodeNames) {
		return evalCodeNames[c]
	}
	return "<unknown-code>"
}

var evalCodeNames = [...]string{
	EvalOK:               "ok",
	EvalInvalidArgument:  "invalid-argument",
	EvalDuplicateName:    "duplicate-name",
	EvalUnknownName:      "unknown-name",
	EvalTypeMismatch:     "type-mismatch",
	EvalIllegalOp:        "illegal-op",
	EvalDivideByZero:     "divide-by-zero",
	EvalTruncation:       "truncation",
	EvalOverflow:         "overflow",
	EvalConstantTooLarge: "constant-too-large",
}

func errf(code EvalCode, format string, args ...any) error {
	return &EvalError{Code: code, Message: fmt.Sprintf(format, args...)}
}
