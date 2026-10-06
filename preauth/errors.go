package preauth

import "errors"

var (
	// ErrInvalid 参数非法：空编号、非正金额、不存在的账户等。
	ErrInvalid = errors.New("preauth: invalid argument")
	// ErrClockBackward 时钟回退：now 小于上一次被接受操作的 now。
	ErrClockBackward = errors.New("preauth: clock moved backwards")
	// ErrDuplicateID 授权编号全局重复（含已终结授权的编号）。
	ErrDuplicateID = errors.New("preauth: duplicate authorization id")
	// ErrNotFound 授权或账户不存在。
	ErrNotFound = errors.New("preauth: not found")
	// ErrClosed 授权已终结（已撤销、已终捕、已过期）。
	ErrClosed = errors.New("preauth: authorization closed")
	// ErrOverTolerance 捕获超过累计授权额按容差基点上浮后的上限。
	ErrOverTolerance = errors.New("preauth: capture exceeds tolerance")
	// ErrInsufficient 可用额度不足。
	ErrInsufficient = errors.New("preauth: insufficient available credit")
	// ErrRefundExceeds 退款额超过该授权可退额（累计已捕获减累计已退款）。
	ErrRefundExceeds = errors.New("preauth: refund exceeds captured amount")
)
