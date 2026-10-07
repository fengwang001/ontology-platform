package temporal

import "errors"

// Code 是按固定唯一优先顺序汇报的错误类别。
type Code int

const (
	// ErrCodeNone 不是错误。
	ErrCodeNone Code = iota
	// ErrCodeEffectiveBeforeCommit：生效时刻早于提交时刻。
	ErrCodeEffectiveBeforeCommit
	// ErrCodeQueryBeforeHorizon：查询时刻早于系统已知的最早记录时刻。
	ErrCodeQueryBeforeHorizon
	// ErrCodeWithdrawAlreadyEffective：尝试撤回一条已经生效的变更。
	ErrCodeWithdrawAlreadyEffective
	// ErrCodeGeneric：其余被拒绝情形（重复 ID、未知目标等），排在上述三类之后。
	ErrCodeGeneric
)

func (c Code) String() string {
	switch c {
	case ErrCodeEffectiveBeforeCommit:
		return "effective_before_commit"
	case ErrCodeQueryBeforeHorizon:
		return "query_before_horizon"
	case ErrCodeWithdrawAlreadyEffective:
		return "withdraw_already_effective"
	case ErrCodeGeneric:
		return "generic"
	default:
		return "none"
	}
}

// PriorityOrder 是错误汇报时必须遵循的固定唯一优先顺序。
var PriorityOrder = []Code{
	ErrCodeEffectiveBeforeCommit,
	ErrCodeQueryBeforeHorizon,
	ErrCodeWithdrawAlreadyEffective,
	ErrCodeGeneric,
}

// DomainError 携带固定错误码与可读说明。
type DomainError struct {
	Code Code
	Msg  string
}

func (e *DomainError) Error() string { return e.Code.String() + ": " + e.Msg }

// AsDomainError 从 error 中提取 *DomainError。
func AsDomainError(err error) (*DomainError, bool) {
	var de *DomainError
	if errors.As(err, &de) {
		return de, true
	}
	return nil, false
}
