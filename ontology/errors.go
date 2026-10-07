package ontology

import "fmt"

// 错误类别，按固定优先级从高到低排列。
type ErrorKind int

const (
	// KindSourceNotFound 源对象实例不存在。
	KindSourceNotFound ErrorKind = iota + 1
	// KindLinkTypeNotSupported 链接类型不支持派生索引。
	KindLinkTypeNotSupported
	// KindNotUnique 因链接不唯一导致的不可索引状态。
	KindNotUnique
	// KindCyclicDerivation 检测到循环传递，链接声明被拒绝。
	KindCyclicDerivation
	// KindDownstreamUpdateFailed 下游索引更新失败导致整体回滚。
	KindDownstreamUpdateFailed
)

var kindName = map[ErrorKind]string{
	KindSourceNotFound:         "source instance not found",
	KindLinkTypeNotSupported:   "link type does not support derived index",
	KindNotUnique:              "link is not unique",
	KindCyclicDerivation:       "cyclic derivation rejected",
	KindDownstreamUpdateFailed: "downstream index update failed",
}

func (k ErrorKind) String() string { return kindName[k] }

// DeriveError 是本子系统所有错误的统一载体，Kind 用于按固定优先级裁决。
type DeriveError struct {
	Kind  ErrorKind
	Msg   string
	Cause error
}

func (e *DeriveError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %s: %v", e.Kind, e.Msg, e.Cause)
	}
	return fmt.Sprintf("%s: %s", e.Kind, e.Msg)
}

func (e *DeriveError) Unwrap() error { return e.Cause }

func newError(kind ErrorKind, format string, args ...any) *DeriveError {
	return &DeriveError{Kind: kind, Msg: fmt.Sprintf(format, args...)}
}

func wrapError(kind ErrorKind, cause error, format string, args ...any) *DeriveError {
	return &DeriveError{Kind: kind, Msg: fmt.Sprintf(format, args...), Cause: cause}
}

// ErrorKindOf 提取错误类别；非本系统错误返回 0。
func ErrorKindOf(err error) ErrorKind {
	if de, ok := err.(*DeriveError); ok {
		return de.Kind
	}
	return 0
}

// classify 按固定优先级返回一组候选错误中优先级最高的那个；无候选时返回 nil。
// 优先级数值越小优先级越高：源实例不存在 > 链接类型不支持 > 不唯一 >
// 循环传递被拒 > 下游更新失败回滚。
func classify(errs []error) error {
	var best *DeriveError
	for _, err := range errs {
		if err == nil {
			continue
		}
		de, ok := err.(*DeriveError)
		if !ok {
			de = wrapError(KindDownstreamUpdateFailed, err, "%v", err)
		}
		if best == nil || de.Kind < best.Kind {
			best = de
		}
	}
	if best == nil {
		return nil
	}
	return best
}
