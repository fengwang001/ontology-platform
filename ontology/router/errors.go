package router

import "errors"

var (
	// ErrInvalidArgument 表示请求参数非法(写入目标属性在该版本下不可写、
	// 迁移声明自相矛盾、修改已生效的对应关系等)。
	ErrInvalidArgument = errors.New("invalid argument")
	// ErrNotFound 表示目标实例不存在。
	ErrNotFound = errors.New("instance not found")
)
