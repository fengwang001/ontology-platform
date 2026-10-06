// Package medclaim 实现医疗费用保单的理赔分摊结算引擎。
//
// 模块划分（职责单一、相互协作）：
//   - policy.go     保单条款登记、参数校验、保单年度换算、比例/不赔集合查询；
//   - settlement.go 单笔理赔结算核心（免赔逐条消耗、比例赔付向上取整、
//     年度自付封顶单次截断与追加赔付归属）；
//   - engine.go     并发安全引擎：年度累计账、受理次序、撤销末笔回滚、
//     固定拒绝次序与错误码；
//   - naive.go      独立朴素参考模型（全量重放 + 先预算后截断的另一种写法）；
//   - errors.go     可区分的错误码。
//
// 所有金额均为非负整数分，赔付额按分向上取整；相同操作序列必然重放出
// 完全相同的赔付与自付金额（无随机、无浮点、无 map 迭代依赖）。
package medclaim

import "errors"

// ErrCode 以固定次序区分各类拒绝原因。
type ErrCode int

const (
	ErrInvalidParameter ErrCode = iota + 1
	ErrPolicyNotFound
	ErrClaimExists
	ErrDateNotCovered
	ErrClaimNotFound
	ErrNotLast
)

// ClaimError 携带可区分的错误码与说明。
type ClaimError struct {
	Code ErrCode
	Msg  string
}

func (e *ClaimError) Error() string {
	return e.Msg
}

func newError(code ErrCode, msg string) error {
	return &ClaimError{Code: code, Msg: msg}
}

// ErrorCode 提取错误码；非本引擎错误返回 0。
func ErrorCode(err error) ErrCode {
	var ce *ClaimError
	if errors.As(err, &ce) {
		return ce.Code
	}
	return 0
}
