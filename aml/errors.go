package aml

import "errors"

// 可区分的错误类型。单操作内按如下优先级只报第一个：
// 参数非法 > 时钟回退 > 账户不存在 > 交易号重复/不存在/已冲正 > 已关联。
var (
	ErrInvalidParam    = errors.New("参数非法")
	ErrClockRollback   = errors.New("时钟回退")
	ErrAccountNotFound = errors.New("账户不存在")
	ErrAccountExists   = errors.New("账户已存在")
	ErrDuplicateTxn    = errors.New("交易号重复")
	ErrTxnNotFound     = errors.New("交易号不存在")
	ErrAlreadyReversed = errors.New("交易已冲正")
	ErrAlreadyLinked   = errors.New("账户已关联")
)
