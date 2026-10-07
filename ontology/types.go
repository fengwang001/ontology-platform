// Package ontology 实现本体平台的实例存储与具备崩溃原子性的批量更新。
//
// 核心保证：一次批量更新在任意中断点崩溃并重启后，系统必须恢复到两种
// 终态之一——整批完全生效或整批完全未生效，且恢复逻辑可重复执行。
package ontology

import "errors"

// BatchID 是批次的唯一标识，单调递增。
type BatchID uint64

// InstanceID 是本体实例的唯一标识。
type InstanceID string

// Instance 是一个本体实例的版本化视图。
type Instance struct {
	ID      InstanceID
	Version uint64
	// LastBatch 是最后一次修改该实例的批次；用于幂等重做/回滚判定。
	LastBatch BatchID
	Props     map[string]string
}

// Clone 返回实例的深拷贝。
func (in Instance) Clone() Instance {
	out := in
	out.Props = make(map[string]string, len(in.Props))
	for k, v := range in.Props {
		out.Props[k] = v
	}
	return out
}

// Mutation 描述对单个实例的一次属性写入。
type Mutation struct {
	Instance InstanceID
	Props    map[string]string
}

// BatchStatus 区分批次必须单独识别的四类终态（及中间态）。
type BatchStatus string

const (
	// StatusInFlight 批次处于生效过程中，终态未定。
	StatusInFlight BatchStatus = "IN_FLIGHT"
	// StatusCommitted 批次正常生效。
	StatusCommitted BatchStatus = "COMMITTED"
	// StatusRolledBack 批次正常回退（提交前主动放弃）。
	StatusRolledBack BatchStatus = "ROLLED_BACK"
	// StatusRecoveredUndone 批次因中断被恢复为“未生效”终态。
	// 对外可观察效果与 StatusRolledBack 完全不可区分。
	StatusRecoveredUndone BatchStatus = "RECOVERED_UNDONE"
	// StatusRecoveredApplied 批次因中断被恢复为“已生效”终态。
	// 对外可观察效果与 StatusCommitted 完全不可区分。
	StatusRecoveredApplied BatchStatus = "RECOVERED_APPLIED"
)

// Terminal 报告状态是否为四种终态之一。
func (s BatchStatus) Terminal() bool {
	switch s {
	case StatusCommitted, StatusRolledBack, StatusRecoveredUndone, StatusRecoveredApplied:
		return true
	}
	return false
}

// Effective 报告终态下批次变更是否对外可见。
// 仅对终态有意义；中间态调用会 panic。
func (s BatchStatus) Effective() bool {
	switch s {
	case StatusCommitted, StatusRecoveredApplied:
		return true
	case StatusRolledBack, StatusRecoveredUndone:
		return false
	}
	panic("ontology: Effective called on non-terminal status " + string(s))
}

var (
	// ErrInstanceUncertain 表示目标实例正处于某个未决批次的生效过程中，
	// 读取或写入都必须被拒绝。
	ErrInstanceUncertain = errors.New("ontology: instance is in an uncertain state (batch in flight)")
	// ErrBatchNotFound 表示批次不存在。
	ErrBatchNotFound = errors.New("ontology: batch not found")
	// ErrInstanceNotFound 表示实例不存在。
	ErrInstanceNotFound = errors.New("ontology: instance not found")
	// ErrDuplicateInstance 表示一个批次内重复变更同一实例（客户端错误）。
	ErrDuplicateInstance = errors.New("ontology: duplicate instance in batch")
	// ErrEngineCrashed 表示引擎已被故障注入标记为崩溃，禁止继续使用。
	ErrEngineCrashed = errors.New("ontology: engine has crashed; restart required")
)
