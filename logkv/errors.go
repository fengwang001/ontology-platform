package logkv

import (
	"errors"
	"fmt"
)

// Kind 区分错误的类别。恢复、读取与合并路径在同一时刻只报告
// 次序最靠前的一类错误，次序即常量的声明顺序。
type Kind int

const (
	// KindInvalidArgument 参数非法（空键、非法配置、合并集合含重复段号等）。
	KindInvalidArgument Kind = iota
	// KindSegmentCorruption 段损坏：已封口段记录校验失败、活动段非尾部
	// 校验失败、或采用提示信息的段在读取时记录校验失败。
	KindSegmentCorruption
	// KindDirectoryDistortion 目录失真：键目录登记的位置/写序号与盘上
	// 记录不一致。失真触发自愈，通常不作为错误返回，而是通过
	// Stats.Distortions 对外报告。
	KindDirectoryDistortion
	// KindSegmentNotFound 合并指定的段号不存在。
	KindSegmentNotFound
	// KindActiveSegmentNotMergeable 合并集合包含活动段。
	KindActiveSegmentNotMergeable
)

func (k Kind) String() string {
	switch k {
	case KindInvalidArgument:
		return "invalid argument"
	case KindSegmentCorruption:
		return "segment corruption"
	case KindDirectoryDistortion:
		return "directory distortion"
	case KindSegmentNotFound:
		return "segment not found"
	case KindActiveSegmentNotMergeable:
		return "active segment not mergeable"
	default:
		return "unknown error"
	}
}

// Error 是引擎返回的唯一结构化错误类型。
type Error struct {
	Kind    Kind
	Segment uint32 // 相关段号；与段无关时为 ^uint32(0)
	Offset  int64  // 段内首个损坏位置；不适用时为 -1
	Msg     string
}

func (e *Error) Error() string {
	if e.Segment != noSegment {
		if e.Offset >= 0 {
			return fmt.Sprintf("logkv: %s (segment %06d, offset %d): %s", e.Kind, e.Segment, e.Offset, e.Msg)
		}
		return fmt.Sprintf("logkv: %s (segment %06d): %s", e.Kind, e.Segment, e.Msg)
	}
	return fmt.Sprintf("logkv: %s: %s", e.Kind, e.Msg)
}

const noSegment = ^uint32(0)

func newError(kind Kind, msg string) *Error {
	return &Error{Kind: kind, Segment: noSegment, Offset: -1, Msg: msg}
}

func corruptError(segment uint32, offset int64, msg string) *Error {
	return &Error{Kind: KindSegmentCorruption, Segment: segment, Offset: offset, Msg: msg}
}

// IsKind 报告 err 是否为给定类别的引擎错误。
func IsKind(err error, kind Kind) bool {
	var e *Error
	if errors.As(err, &e) {
		return e.Kind == kind
	}
	return false
}
