package gate

import "errors"

// 哨兵错误分类，Detail 携带具体原因（如落后节点名、残留条数）。
var (
	ErrInvalidArg  = errors.New("参数非法")
	ErrExists      = errors.New("已存在")
	ErrNotExist    = errors.New("不存在")
	ErrReadOnly    = errors.New("节点只读")
	ErrUnsupported = errors.New("字段不支持")
	ErrNoNode      = errors.New("无节点")
	ErrNodeLagging = errors.New("节点落后")
	ErrAckFailed   = errors.New("确认失败")
	ErrResidue     = errors.New("有残留")
)

// Error 在哨兵分类之上附带判定依据。
type Error struct {
	Op     string
	Base   error
	Detail string
}

func (e *Error) Error() string {
	if e.Detail == "" {
		return e.Base.Error()
	}
	return e.Base.Error() + "（" + e.Detail + "）"
}

func (e *Error) Unwrap() error { return e.Base }
