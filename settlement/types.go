// Package settlement 实现采购合同履约付款结算服务。
//
// 金额一律以整数分计，时间一律以整数日序号计，所有比例以万分比整数给出。
// 服务内所有操作可并发调用，内部以单一互斥锁串行化，结果等价于某个串行顺序；
// 相同操作序列重放得到完全相同的结果。
package settlement

import "fmt"

// ratioBase 是万分比的分母。
const ratioBase int64 = 10000

// ErrorCode 错误类别。常量声明顺序即拒绝优先级：
// 参数非法 > 时钟回退 > 合同或里程碑不存在 > 状态不允许 > 重复结算 > 变更超出合同总额。
// 一次操作违反多条规则时只报优先级最高的第一个。
type ErrorCode int

const (
	ErrCodeNone               ErrorCode = iota // 无错误（操作被接受）
	ErrCodeInvalidArgument                     // 参数非法
	ErrCodeClockRollback                       // 时钟回退
	ErrCodeNotFound                            // 合同或里程碑不存在
	ErrCodeInvalidState                        // 状态不允许
	ErrCodeAlreadySettled                      // 重复结算
	ErrCodeChangeExceedsTotal                  // 变更超出合同总额
)

func (c ErrorCode) String() string {
	switch c {
	case ErrCodeNone:
		return "ok"
	case ErrCodeInvalidArgument:
		return "参数非法"
	case ErrCodeClockRollback:
		return "时钟回退"
	case ErrCodeNotFound:
		return "合同或里程碑不存在"
	case ErrCodeInvalidState:
		return "状态不允许"
	case ErrCodeAlreadySettled:
		return "重复结算"
	case ErrCodeChangeExceedsTotal:
		return "变更超出合同总额"
	default:
		return fmt.Sprintf("未知错误(%d)", int(c))
	}
}

// Error 是服务返回的唯一错误类型，Code 可用于区分错误类别。
type Error struct {
	Code    ErrorCode
	Message string
}

func (e *Error) Error() string {
	return fmt.Sprintf("settlement: %s: %s", e.Code, e.Message)
}

func invalidArg(format string, args ...any) *Error {
	return &Error{Code: ErrCodeInvalidArgument, Message: fmt.Sprintf(format, args...)}
}

func clockRollback(format string, args ...any) *Error {
	return &Error{Code: ErrCodeClockRollback, Message: fmt.Sprintf(format, args...)}
}

func notFound(format string, args ...any) *Error {
	return &Error{Code: ErrCodeNotFound, Message: fmt.Sprintf(format, args...)}
}

func invalidState(format string, args ...any) *Error {
	return &Error{Code: ErrCodeInvalidState, Message: fmt.Sprintf(format, args...)}
}

func alreadySettled(format string, args ...any) *Error {
	return &Error{Code: ErrCodeAlreadySettled, Message: fmt.Sprintf(format, args...)}
}

func changeExceedsTotal(format string, args ...any) *Error {
	return &Error{Code: ErrCodeChangeExceedsTotal, Message: fmt.Sprintf(format, args...)}
}

// MilestoneSpec 里程碑定义：应付金额（分）与计划验收日（日序号）。
type MilestoneSpec struct {
	ID      string
	Amount  int64
	PlanDay int64
}

// ContractSpec 合同定义。所有比例均为万分比整数。
type ContractSpec struct {
	TotalAmount      int64 // 合同总金额（分）
	AdvanceTotal     int64 // 预付款总额（分），合同成立后不可变更
	AdvanceRatio     int64 // 预付款抵扣比例（万分比），以应付金额全额为基数向下取整
	RetentionRatio   int64 // 质保金比例（万分比），以应付金额全额为基数向上取整
	WarrantyDays     int64 // 质保期天数
	PenaltyDailyRate int64 // 违约金日费率（万分比/日），基数为合同总金额
	PenaltyCapRatio  int64 // 违约金封顶比例（万分比），基数为合同总金额
	Milestones       []MilestoneSpec
}

// Change 变更单中对单个里程碑的调整。
type Change struct {
	MilestoneID string
	Amount      int64
	PlanDay     int64
}

// AcceptResult 验收操作结果。驳回时 Passed=false，其余字段为当前基准条款。
type AcceptResult struct {
	Passed      bool
	Amount      int64 // 验收时适用的应付金额
	PlanDay     int64 // 验收时适用的计划验收日
	OverdueDays int64 // 逾期天数（不大于零则为零）
}

// SettleResult 结算结果，完整记录各项扣减与欠额结转，使每次付款可精确复现。
type SettleResult struct {
	Amount          int64 // 应付金额
	Retention       int64 // 本次扣留质保金（向上取整）
	AdvanceDeducted int64 // 本次预付款抵扣（向下取整且受预付款总额约束）
	OverdueDays     int64 // 逾期天数
	PenaltyAssessed int64 // 本次新计提违约金（已受累计封顶约束）
	DebtCarriedIn   int64 // 结算前结转欠额
	PenaltyDeducted int64 // 本次实际扣抵违约金（含欠额优先扣抵）
	DebtCarriedOut  int64 // 结算后结转欠额
	Payment         int64 // 本次实付金额（不为负）
}

// ReleaseResult 质保金释放结果。
type ReleaseResult struct {
	Withheld int64 // 原扣留质保金
	Forfeit  int64 // 缺陷罚没扣抵（不超过该里程碑质保金）
	PaidOut  int64 // 实际释放支付 = Withheld - Forfeit
}

// Summary 合同金额汇总。所有字段均为 O(1) 读取的累计值。
// 守恒不变式：PaidTotal + RetentionHeld + DefectForfeitTotal +
// AdvanceDeductedTotal + PenaltyDeductedTotal == SettledAmountTotal。
// OutstandingPenaltyDebt 为未扣抵违约金欠额，不计入该和。
type Summary struct {
	PaidTotal              int64 // 累计实付（含质保金释放后的支付）
	RetentionHeld          int64 // 质保金余额（已扣留未释放）
	DefectForfeitTotal     int64 // 累计缺陷罚没扣抵
	AdvanceDeductedTotal   int64 // 累计预付款抵扣
	PenaltyDeductedTotal   int64 // 累计违约金扣抵
	OutstandingPenaltyDebt int64 // 未扣抵违约金欠额（结转至下次结算优先扣抵）
	SettledAmountTotal     int64 // 已结算里程碑应付之和
}

// Conserved 校验守恒不变式。
func (s Summary) Conserved() bool {
	return s.PaidTotal+s.RetentionHeld+s.DefectForfeitTotal+
		s.AdvanceDeductedTotal+s.PenaltyDeductedTotal == s.SettledAmountTotal
}
