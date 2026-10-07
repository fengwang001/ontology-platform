package delegation

import "errors"

// 委托声明被拒绝的错误类型。校验按以下固定且唯一的优先顺序进行，
// 排在前面的错误优先被报告：
//
//  1. ErrSubsetExceeds        委托声明的权限子集超出委托方当前实际拥有的范围
//  2. ErrRedelegateNotAllowed 委托方所依赖的上游委托不允许再委托
//  3. ErrCycle                新增委托将导致委托链成环
//  4. ErrExpired              委托已超出有效期（有效期区间非法或已结束）
var (
	ErrSubsetExceeds         = errors.New("delegation: declared subset exceeds delegator's effective permissions")
	ErrRedelegateNotAllowed  = errors.New("delegation: upstream delegation does not allow redelegation")
	ErrCycle                 = errors.New("delegation: new delegation would create a cycle")
	ErrExpired               = errors.New("delegation: validity window is invalid or already expired")
	ErrDelegationNotFound    = errors.New("delegation: delegation record not found")
	ErrDelegationRevokedDone = errors.New("delegation: delegation already revoked")
)
