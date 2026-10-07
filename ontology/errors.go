package ontology

import "fmt"

// ErrorKind 区分遍历可能失败的几类互不相同的错误。
type ErrorKind int

const (
	// ErrKindStartNotExist 遍历起始对象在指定历史时刻尚不存在。
	ErrKindStartNotExist ErrorKind = iota
	// ErrKindBeforeHorizon 指定历史时刻早于系统所能回放的最早边界。
	ErrKindBeforeHorizon
	// ErrKindLimitExceeded 遍历深度或访问规模超出预先声明的上限。
	ErrKindLimitExceeded
	// ErrKindHistoryMissing 底层历史数据缺失，无法确定某一步的状态。
	ErrKindHistoryMissing
)

// errorPriority 规定多类错误条件同时具备时的报告优先级（数值小者优先）。
// 优先级顺序与需求列举顺序一致：
// 起始对象不存在 > 早于最早边界 > 超出上限 > 历史数据缺失。
func (k ErrorKind) errorPriority() int { return int(k) }

func (k ErrorKind) String() string {
	switch k {
	case ErrKindStartNotExist:
		return "START_OBJECT_NOT_EXIST"
	case ErrKindBeforeHorizon:
		return "BEFORE_REPLAY_HORIZON"
	case ErrKindLimitExceeded:
		return "TRAVERSAL_LIMIT_EXCEEDED"
	case ErrKindHistoryMissing:
		return "HISTORY_MISSING"
	default:
		return "UNKNOWN"
	}
}

// TraverseError 是遍历失败的结构化错误。任一错误发生都不会对历史轨迹
// 产生可观察改动，也不会返回掺杂部分展开结果的不完整快照。
type TraverseError struct {
	Kind   ErrorKind
	Detail string
}

func (e *TraverseError) Error() string {
	return fmt.Sprintf("%s: %s", e.Kind, e.Detail)
}
