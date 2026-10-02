// Package acl 实现带继承标志、仅继承位与保护开关的有序访问控制列表求值器。
package acl

import "fmt"

// 继承标志位。
const (
	FlagOI uint8 = 1 << 0 // 对象继承
	FlagCI uint8 = 1 << 1 // 容器继承
	FlagNP uint8 = 1 << 2 // 不再传播
	FlagIO uint8 = 1 << 3 // 仅继承
)

const (
	maxMask     = 65535
	maxDepthLim = 64
	maxACEsLim  = 64
)

// ErrorKind 区分被拒绝操作的类别。
type ErrorKind int

const (
	ErrInvalid  ErrorKind = iota // 参数非法
	ErrNotFound                  // 节点不存在
	ErrConflict                  // 冲突
	ErrLimit                     // 超限
)

func (k ErrorKind) String() string {
	switch k {
	case ErrInvalid:
		return "invalid argument"
	case ErrNotFound:
		return "node not found"
	case ErrConflict:
		return "conflict"
	case ErrLimit:
		return "limit exceeded"
	}
	return "unknown"
}

// Error 是被拒绝操作返回的错误，携带类别与定位信息。
type Error struct {
	Kind ErrorKind
	Msg  string
}

func (e *Error) Error() string { return fmt.Sprintf("acl: %s: %s", e.Kind, e.Msg) }

func newError(kind ErrorKind, format string, args ...interface{}) *Error {
	return &Error{Kind: kind, Msg: fmt.Sprintf(format, args...)}
}

// ACE 是一条显式访问控制项。
type ACE struct {
	Allow     bool
	Principal string
	Mask      uint32
	Flags     uint8
}

// EffectiveEntry 是有效列表中的条目，带来源节点。
type EffectiveEntry struct {
	Allow     bool
	Principal string
	Mask      uint32
	Flags     uint8
	Source    string
}

// ResultKind 是判定的结果类别。
type ResultKind int

const (
	ResultGranted      ResultKind = iota // 授予
	ResultDeniedHit                      // 命中拒绝
	ResultImplicitDeny                   // 隐式拒绝
)

func (k ResultKind) String() string {
	switch k {
	case ResultGranted:
		return "granted"
	case ResultDeniedHit:
		return "denied"
	case ResultImplicitDeny:
		return "implicit-deny"
	}
	return "unknown"
}

// EvalResult 是一次 Eval 的完整结果。
type EvalResult struct {
	Allowed           bool
	Kind              ResultKind
	DecisiveIndex     int    // 决定性条目在 E(node) 中的下标，隐式拒绝时为 -1
	DecisiveSource    string // 决定性条目的来源节点，隐式拒绝时为空
	DecisiveInherited bool   // 决定性条目是否继承而来
	Granted           uint32 // 判定结束时的已授予位 G
}
