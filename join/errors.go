package join

import (
	"errors"
	"fmt"
)

// ErrNoKeys 表示未提供任何连接键。
var ErrNoKeys = errors.New("join: no join keys")

// ErrInvalidMode 表示连接模式不是 Inner 或 Left。
var ErrInvalidMode = errors.New("join: invalid mode")

// TypeError 表示某个连接键列上出现类型冲突，可判定、可定位。
//
// Side 取值：
//   - "cross"：左表与右表之间类型不一致，此时 TypeA 为左表类型、TypeB 为右表类型；
//   - "left" / "right"：单侧内部该列混用了不同类型族，TypeA/TypeB 为先后出现的两个类型。
//
// int64 与 float64 属于同一数值类型族，不会触发本错误。
type TypeError struct {
	Key   string
	Side  string
	TypeA string
	TypeB string
}

// Error 实现 error 接口。
func (e *TypeError) Error() string {
	return fmt.Sprintf("join: key %q: type conflict (%s): %s vs %s",
		e.Key, e.Side, e.TypeA, e.TypeB)
}

// UnsupportedTypeError 表示连接键上出现了不支持的类型。
// 连接键只支持 string、bool、int64、float64 四种类型。
type UnsupportedTypeError struct {
	Key  string
	Side string // "left" 或 "right"
	Type string
}

// Error 实现 error 接口。
func (e *UnsupportedTypeError) Error() string {
	return fmt.Sprintf("join: key %q: unsupported key type %s on %s side",
		e.Key, e.Type, e.Side)
}
