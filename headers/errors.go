package headers

import (
	"errors"
	"fmt"
)

// Stage 是解析状态机的阶段，用于截断定位。
type Stage int

const (
	// StageName 正在读头部名。
	StageName Stage = iota
	// StageColon 名字读完，期待冒号。
	StageColon
	// StageValue 正在读值。
	StageValue
	// StageFold 正在读折行续行。
	StageFold
	// StageEnd 期待终止空行。
	StageEnd
)

func (s Stage) String() string {
	switch s {
	case StageName:
		return "name"
	case StageColon:
		return "colon"
	case StageValue:
		return "value"
	case StageFold:
		return "fold"
	case StageEnd:
		return "terminator"
	}
	return "unknown"
}

// ParseError 描述一次可判定的解析失败：阶段、相关头部、字节偏移。
type ParseError struct {
	Stage  Stage
	Header string // 出错时正在处理的头部名（未知则为空）
	Offset int    // 输入中的字节偏移
	Err    error
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("headers: parse failed at offset %d (stage %s, header %q): %v",
		e.Offset, e.Stage, e.Header, e.Err)
}

func (e *ParseError) Unwrap() error { return e.Err }

// 四类资源上限错误，彼此可用 errors.Is 判定。
var (
	ErrTooManyEntries = errors.New("headers: too many entries")
	ErrNameTooLong    = errors.New("headers: header name too long")
	ErrValueTooLong   = errors.New("headers: header value too long")
	ErrTooLarge       = errors.New("headers: input too large")
)

// 写入与查询错误。
var (
	ErrForbiddenByte = errors.New("headers: forbidden byte in value")
	ErrInvalidName   = errors.New("headers: invalid header name")
	ErrNotFound      = errors.New("headers: header not found")
	ErrDuplicate     = errors.New("headers: duplicate header rejected by policy")
)
