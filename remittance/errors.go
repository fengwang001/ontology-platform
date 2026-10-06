package remittance

import "errors"

// 可区分的错误哨兵，按提交操作的优先级排列：
// 参数非法 > 时钟回退 > 收款人命中制裁 > 幂等冲突 > 报价不存在/已消耗 >
// 报价已过期 > 单笔限额 > 日限额 > 年度额度。
var (
	ErrInvalidParams   = errors.New("remittance: invalid params")
	ErrClockRegression = errors.New("remittance: clock regression")
	ErrSanctioned      = errors.New("remittance: payee sanctioned")
	ErrIdemConflict    = errors.New("remittance: idempotency conflict")
	ErrQuoteNotFound   = errors.New("remittance: quote not found")
	ErrQuoteConsumed   = errors.New("remittance: quote already consumed")
	ErrQuoteExpired    = errors.New("remittance: quote expired")
	ErrSingleLimit     = errors.New("remittance: single transaction limit exceeded")
	ErrDayLimit        = errors.New("remittance: daily limit exceeded")
	ErrYearLimit       = errors.New("remittance: rolling yearly limit exceeded")
	ErrNotFound        = errors.New("remittance: remittance not found")
	ErrInvalidState    = errors.New("remittance: current state does not allow the operation")
)
