// Package subro 实现保险代位追偿回收款分配与差额调整。
//
// 模块协作：alloc 负责纯分配计算，ledger 负责已发放台账与最小差结清，
// clock 负责操作时钟单调性，本包负责参数校验、错误优先级、
// 案件注册表、并发串行化与已接受操作日志。
package subro

import "errors"

// 可区分的错误类别，按优先级排列：参数非法 > 时钟回退 > 案件不存在 >
// 已过时效 > 超出总损失额 > 已放弃。同一操作违反多条规则时只报第一个。
var (
	ErrInvalidParam     = errors.New("subro: 参数非法")
	ErrClockRollback    = errors.New("subro: 时钟回退")
	ErrCaseNotFound     = errors.New("subro: 案件不存在")
	ErrExpired          = errors.New("subro: 已过追偿时效")
	ErrExceedsTotalLoss = errors.New("subro: 超出总损失额")
	ErrAlreadyWaived    = errors.New("subro: 已放弃追偿权")
)

// OpKind 操作类型。
type OpKind int

const (
	OpRegisterCase OpKind = iota // 登记案件
	OpRecover                    // 登记一笔回收
	OpAdjustRatio                // 调整第三方责任比例
	OpSupplement                 // 保险人补充赔付
	OpWaive                      // 被保险人声明放弃追偿权
)

func (k OpKind) String() string {
	switch k {
	case OpRegisterCase:
		return "register_case"
	case OpRecover:
		return "recover"
	case OpAdjustRatio:
		return "adjust_ratio"
	case OpSupplement:
		return "supplement"
	case OpWaive:
		return "waive"
	default:
		return "unknown"
	}
}

// CaseInput 登记案件的参数。
type CaseInput struct {
	CaseID      string // 唯一编号
	TotalLoss   int64  // 总损失额
	InsurerPaid int64  // 保险人已赔付额，不得超过总损失额
	Deadline    int64  // 追偿时效截止日（整数天，含当日）
	RatioBP     int64  // 第三方责任比例，基点 0..10000
}

// Op 一次操作的完整输入；已接受操作按应用顺序记入日志，可原样重放。
type Op struct {
	Kind    OpKind
	Now     int64  // 操作时间，整数天
	CaseID  string // 目标案件（登记案件时取自 Case.CaseID）
	Gross   int64  // recover: 回收毛额
	Fee     int64  // recover: 该笔追偿费用，不得大于毛额
	RatioBP int64  // adjust_ratio: 新责任比例
	Amount  int64  // supplement: 补充赔付额
	Case    CaseInput
}
