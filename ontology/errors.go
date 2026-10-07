package ontology

import "fmt"

// ErrKind 标识写入被拒绝的类别。三类冲突错误互斥：
// 任意一次写入最多命中其中一类。
type ErrKind int

const (
	// ErrKindStaleBaseline 表示基线落后导致的版本冲突：
	// 提交时刻最新版本已领先调用方声明基线超过一个版本，
	// 无法在 O(1) 代价下重算合并，拒绝。
	ErrKindStaleBaseline ErrKind = iota + 1
	// ErrKindDeleted 表示目标实例已被逻辑删除。
	// 判定顺序上优先于属性级冲突检查。
	ErrKindDeleted
	// ErrKindPropertyConflict 表示属性级冲突：
	// 本次写入的（写集合 ∪ 相关读集合）与提交时刻已提交的
	// 最近一次写入的同名集合存在交集。
	ErrKindPropertyConflict
	// ErrKindValidation 表示校验钩子拒绝了合并后的预期状态。
	// 与上述三类正交，判定顺序在最后。
	ErrKindValidation
)

func (k ErrKind) String() string {
	switch k {
	case ErrKindStaleBaseline:
		return "stale-baseline"
	case ErrKindDeleted:
		return "deleted"
	case ErrKindPropertyConflict:
		return "property-conflict"
	case ErrKindValidation:
		return "validation"
	default:
		return "unknown"
	}
}

// WriteError 是写入被拒绝时返回的错误，可通过 Kind 字段区分类别。
type WriteError struct {
	Kind     ErrKind
	ObjectID string
	// Baseline 是调用方声明的基线版本。
	Baseline uint64
	// Current 是提交时刻实例的最新已提交版本。
	Current uint64
	// Props 是触发属性级冲突的交集属性（仅 ErrKindPropertyConflict）。
	Props []string
	// RemoteProps 是触发冲突的跨实例属性交集（仅 ErrKindPropertyConflict）。
	RemoteProps []RemoteRead
	// Detail 是附加说明（如校验钩子的错误信息）。
	Detail string
}

func (e *WriteError) Error() string {
	switch e.Kind {
	case ErrKindStaleBaseline:
		return fmt.Sprintf("ontology: stale baseline for %s: baseline=%d current=%d",
			e.ObjectID, e.Baseline, e.Current)
	case ErrKindDeleted:
		return fmt.Sprintf("ontology: object %s is deleted", e.ObjectID)
	case ErrKindPropertyConflict:
		return fmt.Sprintf("ontology: property conflict on %s: props=%v remote=%v",
			e.ObjectID, e.Props, e.RemoteProps)
	case ErrKindValidation:
		return fmt.Sprintf("ontology: validation rejected %s: %s", e.ObjectID, e.Detail)
	default:
		return "ontology: write rejected"
	}
}

// IsKind 报告 err 是否为指定类别的写入拒绝。
func IsKind(err error, kind ErrKind) bool {
	we, ok := err.(*WriteError)
	return ok && we.Kind == kind
}
