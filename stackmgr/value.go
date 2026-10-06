package stackmgr

import "fmt"

// ErrorClass 对错误进行分类，拒绝次序固定为：
// 未定义 > 参数错误 > 悬垂 > 跨栈或逃逸 > 栈溢出 > 配额不足。
type ErrorClass int

const (
	ClassUnknown ErrorClass = iota
	ClassUndefined
	ClassParameter
	ClassDangling
	ClassCrossStack
	ClassEscape
	ClassStackOverflow
	ClassQuota
	ClassConfig
)

// StackError 携带固定错误类别与可读信息。
type StackError struct {
	Class ErrorClass
	Msg   string
}

func (e *StackError) Error() string { return e.Msg }

func errf(class ErrorClass, format string, args ...any) error {
	return &StackError{Class: class, Msg: fmt.Sprintf(format, args...)}
}

// String 返回错误类别的可读名称。
func (c ErrorClass) String() string {
	switch c {
	case ClassUndefined:
		return "undefined"
	case ClassParameter:
		return "parameter"
	case ClassDangling:
		return "dangling"
	case ClassCrossStack:
		return "cross-stack"
	case ClassEscape:
		return "escape"
	case ClassStackOverflow:
		return "stack-overflow"
	case ClassQuota:
		return "quota"
	case ClassConfig:
		return "config"
	default:
		return "ok"
	}
}

// Value 是槽位中存放的内容：普通整数值或栈内指针。
type Value struct {
	isPtr bool
	ival  int64
	ptr   Pointer
}

// IntValue 构造一个普通值。
func IntValue(v int64) Value { return Value{ival: v} }

// PtrValue 构造一个指针值。
func PtrValue(p Pointer) Value { return Value{isPtr: true, ptr: p} }

// IsPtr 报告该值是否为栈内指针。
func (v Value) IsPtr() bool { return v.isPtr }

// Int 取出普通值；若实为指针返回 false。
func (v Value) Int() (int64, bool) {
	if v.isPtr {
		return 0, false
	}
	return v.ival, true
}

// AsPtr 取出指针；若实为普通值返回 false。
func (v Value) AsPtr() (Pointer, bool) {
	if !v.isPtr {
		return Pointer{}, false
	}
	return v.ptr, true
}

// Pointer 是栈内指针。
//
// 它携带：所属协程 coID、目标帧的唯一且永不复用 id frameID、
// 帧内槽位下标 slot，以及目标槽位在 arena 中的绝对偏移 arenaOff。
// 搬迁只改变基址，故只需 arenaOff += delta；frameID 永不复用，
// 因而即使偏移位置被新帧复用也能可靠判定悬垂。
type Pointer struct {
	coID     int
	frameID  int64
	slot     int
	arenaOff int
}

// CoID 返回指针所属协程。
func (p Pointer) CoID() int { return p.coID }
