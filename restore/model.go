// Package restore 实现本体平台备份重建裁决组件。
//
// 四类备份按固定类别序排列：
//
//	对象类型定义(Type) -> 对象实例(Object) -> 链接实例(Link) -> 动作执行记录(Action)
//
// 裁决过程只读输入快照，不修改任何备份数据。
package restore

import "io"

// Class 标识备份对象类别。
type Class int

const (
	// ClassType 对象类型定义。
	ClassType Class = iota
	// ClassObject 对象实例。
	ClassObject
	// ClassLink 链接实例。
	ClassLink
	// ClassAction 动作执行记录。
	ClassAction
)

// String 返回类别固定名称。
func (c Class) String() string {
	switch c {
	case ClassType:
		return "type"
	case ClassObject:
		return "object"
	case ClassLink:
		return "link"
	case ClassAction:
		return "action"
	default:
		return "unknown"
	}
}

// Classes 按固定依赖顺序返回全部类别，任何确定性排序都以此为准。
func Classes() []Class {
	return []Class{ClassType, ClassObject, ClassLink, ClassAction}
}

// Rank 返回类别在固定重建顺序中的序号（从 0 开始）。
func (c Class) Rank() int { return int(c) }

// RecordID 唯一标识任意类别中的一条记录。
type RecordID struct {
	Class Class
	Key   string
}

func (id RecordID) String() string { return id.Class.String() + ":" + id.Key }

// RecordState 表示单条记录在其所属备份内部的状态。
type RecordState int

const (
	// StateIntact 记录完好，仅凭自身备份可恢复。
	StateIntact RecordState = iota
	// StateCorrupt 记录损坏，仅凭自身备份不可恢复。
	StateCorrupt
)

// TypeDef 对象类型定义记录。类型定义不依赖任何其它记录。
type TypeDef struct {
	Key   string
	State RecordState
}

// ObjectInstance 对象实例，依赖其所属对象类型定义。
type ObjectInstance struct {
	Key     string
	TypeKey string
	State   RecordState
}

// LinkInstance 链接实例，依赖其两端引用的对象实例。
type LinkInstance struct {
	Key          string
	SourceObject string
	TargetObject string
	State        RecordState
}

// ActionRecord 动作执行记录，依赖其涉及的全部对象实例与链接实例。
type ActionRecord struct {
	Key     string
	Objects []string
	Links   []string
	State   RecordState
}

// ClassBackup 描述某一类备份整体的存放状态。
type ClassBackup struct {
	// Missing 表示该类备份整体缺失（备份不存在）。
	Missing bool
	// CorruptAll 表示该类备份整体损坏（备份存在但完全不可读）。
	// Missing 与 CorruptAll 同时为真时按 Missing 处理；二者都判定为整体不可用。
	CorruptAll bool
}

// Edge 表示一条“From 依赖 To（To 必须先于 From 重建）”的依赖边。
// 领域内依赖由快照结构自动推导；ExtraDeps 仅用于对畸形数据（例如跨类别反向依赖、
// 循环依赖）做防御性建模，正常备份不会使用。
type Edge struct {
	From RecordID
	To   RecordID
}

// Snapshot 是一次裁决的完整只读输入。
type Snapshot struct {
	Classes map[Class]ClassBackup
	Types   []TypeDef
	Objects []ObjectInstance
	Links   []LinkInstance
	Actions []ActionRecord
	// ExtraDeps 追加领域推导之外的依赖边。
	ExtraDeps []Edge
}

// ReasonCode 是单条记录可重建性结论的判定依据代码。
type ReasonCode int

const (
	// ReasonNone 无判定（不应出现在有效裁决中）。
	ReasonNone ReasonCode = iota
	// ReasonIntact 自身完好且全部依赖可恢复，可重建。
	ReasonIntact
	// ReasonClassUnavailable 所属类备份整体不可用。
	ReasonClassUnavailable
	// ReasonSelfCorrupt 自身记录在备份中损坏。
	ReasonSelfCorrupt
	// ReasonDependencyUnavailable 依赖项不可恢复导致的级联不可重建。
	ReasonDependencyUnavailable
	// ReasonReferenceBroken 依赖引用指向不存在的记录。
	ReasonReferenceBroken
	// ReasonCycle 处于循环依赖（或其下游），重建顺序无法确定。
	ReasonCycle
	// ReasonNewlyCorrupt 重建过程中被新发现损坏。
	ReasonNewlyCorrupt
	// ReasonBlockedByNewDamage 被新发现损坏级联阻断。
	ReasonBlockedByNewDamage
	// ReasonAlreadyCompleted 已在新发现损坏前完成重建，不撤销。
	ReasonAlreadyCompleted
)

// ErrorCode 是裁决错误的四类固定优先级代码（数值越小优先级越高）。
type ErrorCode int

const (
	// ErrClassUnavailable 优先级1：某类备份整体缺失/整体损坏。
	ErrClassUnavailable ErrorCode = iota + 1
	// ErrCascade 优先级2：部分不可用导致的级联不可重建。
	ErrCascade
	// ErrCycle 优先级3：循环依赖导致重建顺序无法确定。
	ErrCycle
	// ErrNewDamage 优先级4：重建过程中途新发现的损坏。
	ErrNewDamage
)

// VerdictError 描述一条裁决错误，按 ErrorCode 的固定优先级排序输出。
type VerdictError struct {
	Code    ErrorCode
	Class   *Class
	Record  *RecordID
	Message string
}

// ClassStatus 是某类备份经内部可用性判定后的状态。
type ClassStatus int

const (
	// ClassOK 类备份完好，全部记录可恢复。
	ClassOK ClassStatus = iota
	// ClassPartial 类备份部分可用：至少一条记录可恢复、至少一条不可恢复。
	ClassPartial
	// ClassUnavailable 类备份整体缺失或整体损坏。
	ClassUnavailable
)

// RecordVerdict 是单条记录的裁决结论与有序判定依据。
type RecordVerdict struct {
	ID          RecordID
	Recoverable bool
	Reasons     []ReasonCode
	Detail      string
}

// StepKind 区分重建计划中的记录步骤与类别屏障步骤。
type StepKind int

const (
	// StepRecord 重建单条记录。
	StepRecord StepKind = iota
	// StepBarrier 将某一类数据标记为“已重建完成”。
	// 保证任何一类数据不会在其依赖数据之前被标记完成。
	StepBarrier
)

// PlanStep 是确定性重建计划中的一个步骤。
type PlanStep struct {
	Kind           StepKind
	Record         *RecordID
	CompletedClass *Class
}

// AuditEvent 记录一次判定的输入要点、输出与依据，供事后复核。
type AuditEvent struct {
	Stage    string `json:"stage"`
	Class    string `json:"class,omitempty"`
	Record   string `json:"record,omitempty"`
	Outcome  string `json:"outcome"`
	Basis    string `json:"basis,omitempty"`
	Priority int    `json:"priority,omitempty"`
}

// Verdict 是对一批备份数据的完整裁决结果。
type Verdict struct {
	ClassStatuses map[Class]ClassStatus
	Records       map[RecordID]RecordVerdict
	Plan          []PlanStep
	Errors        []VerdictError
	Audit         []AuditEvent
}

// Progress 描述重建中途新发现损坏时的执行进度。
type Progress struct {
	// Completed 已经完成重建的记录集合，这部分结论不撤销。
	Completed map[RecordID]bool
}

// DamageReport 描述重建过程中新发现的损坏。
type DamageReport struct {
	// NewlyCorrupt 此前认为完好、中途确认损坏的记录。
	NewlyCorrupt []RecordID
	// NewlyClassUnavailable 此前认为可用、中途确认整体不可用的类别。
	NewlyClassUnavailable []Class
}

// Adjudicator 是无状态裁决器，可被任意多 goroutine 并发使用。
type Adjudicator struct{}

// New 创建裁决器。
func New() *Adjudicator { return &Adjudicator{} }

// Adjudicate 对一批备份快照执行一次性裁决。该调用只读快照。
func (a *Adjudicator) Adjudicate(snap *Snapshot) *Verdict {
	return adjudicate(a, snap)
}

// Reassess 在重建中途发现新损坏后重新评估全部依赖关系。
func (a *Adjudicator) Reassess(prev *Verdict, snap *Snapshot, prog Progress, report DamageReport) *Verdict {
	return reassess(a, prev, snap, prog, report)
}

// WriteAuditJSON 将审计轨迹以 JSON Lines 形式写出，供复核与归档。
func (v *Verdict) WriteAuditJSON(w io.Writer) error {
	return writeAuditJSON(v, w)
}
