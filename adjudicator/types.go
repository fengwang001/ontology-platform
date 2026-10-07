// Package adjudicator 实现本体平台备份重建的裁决组件。
//
// 备份按四类数据独立存放：对象类型定义、对象实例、链接实例、动作执行记录。
// 本组件判定重建顺序与可重建范围，保证重建过程中任意阶段的中间状态内部自洽。
package adjudicator

import "fmt"

// Category 表示备份数据类别。声明顺序即固定的类别优先级，
// 同时作为重建顺序平局裁决的第一关键字。
type Category int

const (
	// CategoryObjectTypeDef 对象类型定义。
	CategoryObjectTypeDef Category = iota
	// CategoryObjectInstance 对象实例。
	CategoryObjectInstance
	// CategoryLinkInstance 链接实例。
	CategoryLinkInstance
	// CategoryActionRecord 动作执行记录。
	CategoryActionRecord
)

// categoryCount 类别总数，内部使用。
const categoryCount = int(CategoryActionRecord) + 1

func (c Category) String() string {
	switch c {
	case CategoryObjectTypeDef:
		return "ObjectTypeDef"
	case CategoryObjectInstance:
		return "ObjectInstance"
	case CategoryLinkInstance:
		return "LinkInstance"
	case CategoryActionRecord:
		return "ActionRecord"
	default:
		return fmt.Sprintf("Category(%d)", int(c))
	}
}

// RecordRef 唯一指向某类别下的一条记录。
type RecordRef struct {
	Category Category
	ID       string
}

func (r RecordRef) String() string {
	return fmt.Sprintf("%s/%s", r.Category, r.ID)
}

// Record 备份中的一条记录及其跨类别依赖。
type Record struct {
	Ref RecordRef
	// DependsOn 为该记录重建前必须已可用的直接依赖。
	DependsOn []RecordRef
}

// CategoryBackup 某一类别备份的整体状态。
// 零值表示该类别整体缺失。
type CategoryBackup struct {
	// Present 表示该类别备份是否存在。
	Present bool
	// WhollyCorrupt 表示备份虽存在但整体损坏、无法解析出任何记录。
	WhollyCorrupt bool
	// Damaged 为部分损坏场景下不可恢复的记录 ID 集合；
	// 不在集合中的记录视为可恢复。
	Damaged map[string]bool
	// Records 为备份中可解析出的全部记录（含已损坏的，
	// 以便裁决时给出明确的不可重建判定而非静默忽略）。
	Records []Record
}

// Available 报告该类别备份是否整体可用（存在且未整体损坏）。
func (b CategoryBackup) Available() bool {
	return b.Present && !b.WhollyCorrupt
}

// Snapshot 一批备份数据的只读快照，是裁决的唯一输入。
// 裁决过程不得修改 Snapshot 及其任何字段。
type Snapshot struct {
	Backups [categoryCount]CategoryBackup
}

// Backup 返回指定类别的备份状态。
func (s *Snapshot) Backup(c Category) CategoryBackup {
	return s.Backups[int(c)]
}

// ErrorKind 区分四类互不相同、判定遵循固定优先级的错误。
type ErrorKind int

const (
	// ErrKindNone 无错误。
	ErrKindNone ErrorKind = iota
	// ErrKindCategoryUnavailable 某类备份整体缺失或整体损坏。优先级最高：
	// 它是输入的固有属性，决定其余一切判定的前提。
	ErrKindCategoryUnavailable
	// ErrKindCascade 部分不可用沿依赖链级联导致的不可重建。
	// 次于整体不可用：只有整体可用的类别才谈得上部分损坏的级联。
	ErrKindCascade
	// ErrKindCycle 可重建子图上仍存在循环依赖，重建顺序无法确定。
	// 次于级联：只有先剔除已判定不可重建的记录，循环检测才有意义。
	ErrKindCycle
	// ErrKindLateCorruption 重建过程中途新发现的损坏。优先级最低：
	// 它只在执行阶段发生，前序三类错误都已在静态裁决中处理完毕。
	ErrKindLateCorruption
)

func (k ErrorKind) String() string {
	switch k {
	case ErrKindNone:
		return "None"
	case ErrKindCategoryUnavailable:
		return "CategoryUnavailable"
	case ErrKindCascade:
		return "Cascade"
	case ErrKindCycle:
		return "Cycle"
	case ErrKindLateCorruption:
		return "LateCorruption"
	default:
		return fmt.Sprintf("ErrorKind(%d)", int(k))
	}
}

// Error 裁决或重建过程中报告的错误。
type Error struct {
	Kind ErrorKind
	// Category 仅对 ErrKindCategoryUnavailable 有意义。
	Category Category
	// Refs 为与错误相关的记录（如循环依赖的环、中途损坏的记录）。
	Refs []RecordRef
	Msg  string
}

func (e *Error) Error() string {
	if e.Kind == ErrKindCategoryUnavailable {
		return fmt.Sprintf("%s: 类别 %s 整体不可用: %s", e.Kind, e.Category, e.Msg)
	}
	return fmt.Sprintf("%s: %s", e.Kind, e.Msg)
}

// ReasonCode 单条记录判定结论的原因码。
type ReasonCode int

const (
	// ReasonOK 记录可重建。
	ReasonOK ReasonCode = iota
	// ReasonCategoryUnavailable 所属类别整体不可用。
	ReasonCategoryUnavailable
	// ReasonSelfDamaged 记录自身在备份中损坏、不可恢复。
	ReasonSelfDamaged
	// ReasonDependencyUnavailable 依赖链上存在不可重建的记录（级联）。
	ReasonDependencyUnavailable
	// ReasonCycle 记录处于可重建子图的循环依赖中。
	ReasonCycle
	// ReasonLateCorruption 重建中途新发现该记录或其依赖损坏。
	ReasonLateCorruption
)

func (r ReasonCode) String() string {
	switch r {
	case ReasonOK:
		return "OK"
	case ReasonCategoryUnavailable:
		return "CategoryUnavailable"
	case ReasonSelfDamaged:
		return "SelfDamaged"
	case ReasonDependencyUnavailable:
		return "DependencyUnavailable"
	case ReasonCycle:
		return "Cycle"
	case ReasonLateCorruption:
		return "LateCorruption"
	default:
		return fmt.Sprintf("ReasonCode(%d)", int(r))
	}
}

// RecordVerdict 单条记录的判定结论。
type RecordVerdict struct {
	Ref         RecordRef
	Rebuildable bool
	Reason      ReasonCode
	// BlockedBy 为导致不可重建的直接原因记录（自身损坏时为自身）。
	BlockedBy []RecordRef
}

// DecisionEntry 判定依据日志：记录每次裁决的输入要点、输出与理由。
type DecisionEntry struct {
	Ref     RecordRef
	Verdict bool
	Reason  ReasonCode
	Detail  string
}

// Verdict 一次完整裁决的输出。
type Verdict struct {
	// Order 为唯一确定的重建顺序，仅包含可重建记录。
	Order []RecordRef
	// Verdicts 按 (类别, ID) 固定顺序排列的全部记录判定。
	Verdicts []RecordVerdict
	// Log 为每条记录的判定依据，与 Verdicts 同序。
	Log []DecisionEntry
	// Err 为裁决层面的最高优先级错误；无错误时为 nil。
	Err *Error
}

// Rebuildable 查询单条记录是否可重建。
func (v *Verdict) Rebuildable(ref RecordRef) bool {
	for _, rv := range v.Verdicts {
		if rv.Ref == ref {
			return rv.Rebuildable
		}
	}
	return false
}
