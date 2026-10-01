package inlinecache

import "errors"

var (
	// ErrInvalidCapacity 构造管理器时多态上限 M 小于 2。
	ErrInvalidCapacity = errors.New("inlinecache: polymorphic capacity must be at least 2")
	// ErrInvalidShape 形状编号不是正整数。
	ErrInvalidShape = errors.New("inlinecache: shape id must be a positive integer")
	// ErrSiteExists 创建站点时名字已被占用。
	ErrSiteExists = errors.New("inlinecache: site already exists")
	// ErrSiteNotFound 访问或查询的站点不存在。
	ErrSiteNotFound = errors.New("inlinecache: site not found")
	// ErrShapeUndefined 访问的形状未在全局方法表中定义。
	ErrShapeUndefined = errors.New("inlinecache: shape not defined in method table")
)
