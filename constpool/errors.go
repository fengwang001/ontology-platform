package constpool

import "errors"

var (
	// ErrInvalidCapacity 在构造容量小于 1 时返回。
	ErrInvalidCapacity = errors.New("constpool: capacity must be >= 1")
	// ErrUnknownKind 在常量种类不是 KindInt/KindFloat/KindString 时返回。
	ErrUnknownKind = errors.New("constpool: unknown constant kind")
	// ErrPoolFull 在池已满且待驻留/合并的常量是新常量时返回。
	ErrPoolFull = errors.New("constpool: pool is full")
	// ErrIndexOutOfRange 在取值下标为负或不小于池大小时返回。
	ErrIndexOutOfRange = errors.New("constpool: index out of range")
)
