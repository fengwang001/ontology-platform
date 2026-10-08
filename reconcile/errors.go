package reconcile

import "errors"

// ErrInsufficientReplicas 表示可读副本数量不足以确定位点基准，
// 本次和解整体失败。这是三类错误中优先级最高、唯一致命的一类。
var ErrInsufficientReplicas = errors.New("reconcile: 可读副本数量不足以确定位点基准")

// ErrorClass 标识和解过程中三类互不相同的错误。
//
// 判定遵循固定优先级（与编号顺序一致）：
//
//  1. ClassBaselineUndetermined：可读副本不足，无法确定位点基准。
//     没有基准就无法解释任何副本内容，故优先级最高且致命。
//  2. ClassReplicaCorrupted：副本自身结构性损坏。只缩小参与集合，
//     不影响其余副本和解，故逐副本记录、不致命。
//  3. ClassObjectUnreconcilable：按优先规则裁决后仍存在冲突。
//     只影响单个对象实例，作用域最小，故优先级最低。
//
// 该顺序是数据依赖决定的：必须先有可读集合才能判定基准是否充分，
// 必须先有基准与参与集合才能进行逐对象裁决。
type ErrorClass int

const (
	// ClassBaselineUndetermined 可读副本数量不足以确定位点基准（致命）。
	ClassBaselineUndetermined ErrorClass = iota + 1
	// ClassReplicaCorrupted 副本结构性损坏，被隔离（逐副本记录）。
	ClassReplicaCorrupted
	// ClassObjectUnreconcilable 对象实例裁决后仍冲突，不可和解（逐对象记录）。
	ClassObjectUnreconcilable
)

// CorruptionKind 区分副本损坏的范围。
type CorruptionKind string

const (
	// CorruptWhole 头部不可读，整个副本无法解析。
	CorruptWhole CorruptionKind = "whole"
	// CorruptPartial 头部可读但部分条目校验失败。
	CorruptPartial CorruptionKind = "partial"
)

// ReplicaFault 记录一个被隔离的损坏副本。
type ReplicaFault struct {
	// ReplicaID 在头部可读时为副本 ID，否则为空。
	ReplicaID string         `json:"replica_id"`
	Kind      CorruptionKind `json:"kind"`
	Detail    string         `json:"detail"`
	// Digest 是原始输入的 SHA-256，用于审计与确定性排序。
	Digest string `json:"digest"`
}

// IssueReason 区分对象实例不可和解的原因。
type IssueReason string

const (
	// ReasonConflictTie 最高优先级上出现并列的不同取值，优先规则
	// 本身没有更细一层的比较依据，判定为不可和解。
	ReasonConflictTie IssueReason = "conflict_tie"
	// ReasonInsufficientParticipants 对象的所有来源副本均被隔离，
	// 剩余可参与副本不足以确定其唯一取值。
	ReasonInsufficientParticipants IssueReason = "insufficient_participants"
)

// ObjectIssue 记录一个不可和解的对象实例（或其中一个属性）。
type ObjectIssue struct {
	ObjectID string `json:"object_id"`
	// Attribute 为空表示整个对象不可和解（来源不足）。
	Attribute string      `json:"attribute,omitempty"`
	Reason    IssueReason `json:"reason"`
	Detail    string      `json:"detail"`
}
