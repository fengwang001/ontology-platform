package ontology

import "errors"

var (
	ErrInvalid       = errors.New("参数非法")
	ErrPolicyMissing = errors.New("保单不存在")
	ErrClaimExists   = errors.New("理赔已存在")
	ErrNotCovered    = errors.New("事故日未承保")
	ErrClaimMissing  = errors.New("理赔不存在")
	ErrNotLast       = errors.New("非末笔")
)

// RejectOrder 是固定拒绝次序，序号越小越先报。
var rejectOrder = []error{
	ErrInvalid,
	ErrPolicyMissing,
	ErrClaimExists,
	ErrNotCovered,
	ErrClaimMissing,
	ErrNotLast,
}
