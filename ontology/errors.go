package ontology

import "errors"

// ErrKind 对所有可观察的拒绝原因进行归一化分类。
//
// 拒绝原因的判定次序（只报第一个命中）在 service 层强制执行：
//
//	KindInvalidArgument      参数非法（链接类型/实例不存在、批次内有序对重复、有序对与已存在链接重复）
//	KindSourceCardinality    起点一侧基数超限
//	KindTargetCardinality    终点一侧基数超限
//
// 删除不存在的链接单独归一化为 KindNotFound，与「存在但基数已满」明确区分。
type ErrKind int

const (
	// KindOK 不是拒绝，表示操作被接受（仅用于逐条结果）。
	KindOK ErrKind = iota
	KindInvalidArgument
	KindSourceCardinality
	KindTargetCardinality
	KindNotFound
)

// String 返回便于测试输出与日志判读的稳定名称。
func (k ErrKind) String() string {
	switch k {
	case KindOK:
		return "OK"
	case KindInvalidArgument:
		return "INVALID_ARGUMENT"
	case KindSourceCardinality:
		return "SOURCE_CARDINALITY_EXCEEDED"
	case KindTargetCardinality:
		return "TARGET_CARDINALITY_EXCEEDED"
	case KindNotFound:
		return "LINK_NOT_FOUND"
	default:
		return "UNKNOWN"
	}
}

// OpError 是本系统对外暴露的唯一错误类型。
//
// 所有失败路径都返回 *OpError，调用方可用 errors.As 取出 Kind 做分支，
// 无需解析错误文本，从而保证三类错误可稳定相互区分。
type OpError struct {
	kind ErrKind
	msg  string
}

func (e *OpError) Error() string { return e.kind.String() + ": " + e.msg }
func (e *OpError) Kind() ErrKind { return e.kind }

func opError(kind ErrKind, msg string) error { return &OpError{kind: kind, msg: msg} }

// KindOf 从任意 error 中取出归一化类别；nil 与非本系统错误分别映射为 KindOK 与 KindInvalidArgument。
func KindOf(err error) ErrKind {
	if err == nil {
		return KindOK
	}
	var e *OpError
	if errors.As(err, &e) {
		return e.Kind()
	}
	return KindInvalidArgument
}
