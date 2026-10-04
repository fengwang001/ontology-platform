package flush

import "fmt"

// Code 标识操作被拒绝的具体原因，各操作按规格顺序只报第一个命中的原因。
type Code int

const (
	// CodeInvalidArgument 参数非法：D、页号、LSN、target 越界，或 AddDep 的 a 等于 b。
	CodeInvalidArgument Code = iota + 1
	// CodeLSNNotAdvanced Modify 的 LSN 未严格大于已接受的最大 LSN，或 SetFlushed 回退。
	CodeLSNNotAdvanced
	// CodeDirtyPoolFull 干净页变脏时脏页数已等于 D。
	CodeDirtyPoolFull
	// CodePageNotDirty FlushStart 的页不脏。
	CodePageNotDirty
	// CodePageInFlight FlushStart 的页已在途。
	CodePageInFlight
	// CodeLogNotFlushed FlushStart 时 p.lsn 大于已落盘 LSN。
	CodeLogNotFlushed
	// CodePredecessorDirty FlushStart 时前置页仍脏（在途也算脏）。
	CodePredecessorDirty
	// CodePageNotInFlight FlushDone 的页不在途。
	CodePageNotInFlight
	// CodeDependencyCycle AddDep 会使依赖边成环。
	CodeDependencyCycle
)

func (c Code) String() string {
	switch c {
	case 0:
		return "ok"
	case CodeInvalidArgument:
		return "invalid argument"
	case CodeLSNNotAdvanced:
		return "lsn not advanced"
	case CodeDirtyPoolFull:
		return "dirty pool full"
	case CodePageNotDirty:
		return "page not dirty"
	case CodePageInFlight:
		return "page already in flight"
	case CodeLogNotFlushed:
		return "log not flushed"
	case CodePredecessorDirty:
		return "predecessor still dirty"
	case CodePageNotInFlight:
		return "page not in flight"
	case CodeDependencyCycle:
		return "dependency cycle"
	default:
		return "unknown"
	}
}

// Error 是一次被拒绝的操作，携带可区分的拒绝原因。
type Error struct {
	Op     string
	Code   Code
	Detail string
}

func (e *Error) Error() string {
	return fmt.Sprintf("flush: %s rejected: %s (%s)", e.Op, e.Code, e.Detail)
}
