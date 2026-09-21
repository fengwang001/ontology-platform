// Package lease 实现带 fencing token 的租约管理器。
//
// 时间完全由外部注入（New 的 now 参数），实现内部绝不调用 time.Now()，
// 因此租约的过期行为在测试中可以确定性地复现。
package lease

import "errors"

// 哨兵错误：调用方可以用 errors.Is 判定，不得仅比较字符串。
var (
	// ErrInvalidHolder 表示 holder 为空串。
	ErrInvalidHolder = errors.New("lease: holder 不能为空")

	// ErrInvalidTTL 表示 ttlMillis <= 0。
	ErrInvalidTTL = errors.New("lease: ttl 必须为正数")

	// ErrLeaseHeld 表示租约仍被其他持有者有效持有，抢占失败。
	ErrLeaseHeld = errors.New("lease: 租约被他人持有")

	// ErrNotHolder 表示调用者不是当前记录的持有者
	// （从未持有、已被他人抢占、或已释放）。
	//
	// 设计决策：被抢占后调用 Renew/Release 返回 ErrNotHolder，
	// 与"自己持有但已过期"返回的 ErrLeaseExpired 是不同类别。
	// 理由：两种情形的恢复策略不同——被抢占意味着锁已被别人拿走，
	// 调用者必须重新 Acquire；而自己过期且无人接管时，
	// 语义上只是"让租约 lapse 了"。区分开便于调用方分别处理。
	ErrNotHolder = errors.New("lease: 调用者不是当前持有者")

	// ErrTokenMismatch 表示 holder 匹配但 token 与当前租约不符。
	ErrTokenMismatch = errors.New("lease: token 与当前租约不匹配")

	// ErrLeaseExpired 表示租约已过期（含"恰好到期"的边界时刻）。
	//
	// 设计决策：租约在 now >= expiresAt 时失效，即到期那一刻起
	// 续约与写入都算失效。理由：不变量 5 要求"过期即失效"，
	// 采用左闭右开区间 [start, expiresAt) 可以让新持有者在
	// now == expiresAt 时立刻 Acquire 成功，而旧持有者在同一时刻
	// 的写必须被拒绝——若边界算有效，则同一时刻可能出现两个
	// 持有者都认为自己有效，违反互斥。
	ErrLeaseExpired = errors.New("lease: 租约已过期")

	// ErrStaleToken 表示 Write 携带的 token 不是当前有效租约的 token。
	//
	// 设计决策：token 是"当前 token 的前一个值"与"从未发出过的巨大值"
	// 归为同一类错误 ErrStaleToken。理由：fencing 不变量只要求
	// "拒绝一切不等于当前有效 token 的写"，区分"曾经合法但已过时"
	// 与"从未存在"对安全性没有帮助，反而泄露内部状态。
	ErrStaleToken = errors.New("lease: 过期或未知的 fencing token")
)
