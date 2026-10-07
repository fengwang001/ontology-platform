package gc

import "errors"

// 拒绝次序固定为：未定义 > 参数错误 > 悬垂引用 > 空间不足。
var (
	ErrUndefined   = errors.New("gc: undefined object")
	ErrBadArgument = errors.New("gc: invalid argument")
	ErrDangling    = errors.New("gc: dangling reference")
	ErrOutOfMemory = errors.New("gc: insufficient space")
)
