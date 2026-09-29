package graph

import "errors"

var (
	// ErrEmptyID 表示对象 ID 为空。
	ErrEmptyID = errors.New("graph: object id must not be empty")
	// ErrEmptyType 表示对象或边的类型为空。
	ErrEmptyType = errors.New("graph: type must not be empty")
	// ErrMissingEndpoint 表示边引用了不存在的对象。
	ErrMissingEndpoint = errors.New("graph: link endpoint does not exist")
	// ErrSelfLoop 表示边的起点与终点相同。
	ErrSelfLoop = errors.New("graph: self loop is not allowed")
)
