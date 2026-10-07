package traverse

import "errors"

// 四类互斥错误，判定次序固定：起始对象不存在 -> 深度上限非法 -> 条数上限非法 -> 方向集合为空。
var (
	ErrStartObjectNotFound = errors.New("start object not found")
	ErrInvalidDepthLimit   = errors.New("depth limit must be a positive integer")
	ErrInvalidResultLimit  = errors.New("result limit must be a positive integer")
	ErrEmptyDirections     = errors.New("direction set must not be empty")
)
