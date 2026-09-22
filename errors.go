package ontology

import (
	"errors"
	"fmt"
	"strings"
)

// 语法类错误（词法 / 解析阶段），可用 errors.Is 区分。
var (
	// ErrEmpty 表示空表达式或纯空白输入。
	ErrEmpty = errors.New("空表达式")
	// ErrIllegalChar 表示非法字符或不该出现的记号。
	ErrIllegalChar = errors.New("非法字符")
	// ErrParen 表示括号不匹配。
	ErrParen = errors.New("括号不匹配")
	// ErrBadLiteral 表示字面量 malformed。
	ErrBadLiteral = errors.New("非法字面量")
)

// 数值类错误（求值 / 字面量精确性阶段），可用 errors.Is 区分。
var (
	// ErrDivZero 表示除零。
	ErrDivZero = errors.New("除零")
	// ErrOverflow 表示 int64 运算溢出。
	ErrOverflow = errors.New("溢出")
	// ErrInexact 表示数值不可精确表示。
	ErrInexact = errors.New("数值不可精确表示")
)

// Error 携带错误类别（Kind，可用 errors.Is 判定）、
// 输入中的字节位置（Pos，无定位时为 -1）与补充说明。
type Error struct {
	Kind error
	Pos  int
	Note string
}

// Error 返回带定位与说明的错误文本。
func (e *Error) Error() string {
	var b strings.Builder
	b.WriteString(e.Kind.Error())
	if e.Pos >= 0 {
		fmt.Fprintf(&b, "（字节位置 %d）", e.Pos)
	}
	b.WriteString(e.Note)
	return b.String()
}

// Unwrap 暴露类别哨兵，供 errors.Is 判定。
func (e *Error) Unwrap() error { return e.Kind }

// errAt 构造带位置与格式化说明的错误；format 为空时不附加说明。
func errAt(kind error, pos int, format string, args ...any) *Error {
	note := ""
	if format != "" {
		note = "：" + fmt.Sprintf(format, args...)
	}
	return &Error{Kind: kind, Pos: pos, Note: note}
}
