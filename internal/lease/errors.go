package lease

import "errors"

// 所有可判定错误的哨兵值。调用方一律用 errors.Is 判定，
// 不要比较错误文本。
var (
	// ErrEmptyHolder：holder 为空串。
	ErrEmptyHolder = errors.New("lease: holder must not be empty")

	// ErrInvalidTTL：ttlMillis <= 0。
	ErrInvalidTTL = errors.New("lease: ttlMillis must be positive")

	// ErrLeaseHeld：租约正被其他持有者持有，Acquire 失败。
	ErrLeaseHeld = errors.New("lease: held by another holder")

	// ErrAlreadyHeld：调用方自己就是当前有效持有者，却再次 Acquire。
	// 设计决策（题面第四节）：同一 holder 在租约有效时重复 Acquire，
	// 既不算续约也不发新 token，而是报错。理由：Acquire 的语义是
	// "开启一个新的租约纪元"，延长已有租约应走 Renew；若发新 token，
	// 旧 token 立即失效会让持有者自己在并发写时被 fencing 拒绝，
	// 语义混乱。保持 "一个纪元一个 token" 最清晰。
	ErrAlreadyHeld = errors.New("lease: caller already holds the lease; use Renew")

	// ErrNotHolder：Renew/Release 时，调用方不是当前租约的持有者
	// （从未持有、已被抢占、已释放，或 token 不匹配）。
	// 设计决策（题面第四节）：被抢占后 Renew 与 "租约已过期" 不是
	// 同一类错误。被抢占意味着租约已属于别人，错误应指出
	// "你不是持有者"；而 ErrLeaseExpired 表示 "你仍是登记在册的
	// 持有者，但租约已自然到期"。两者对调用方的恢复策略不同。
	ErrNotHolder = errors.New("lease: caller is not the current holder")

	// ErrLeaseExpired：调用方仍是当前登记持有者，但租约已到期。
	ErrLeaseExpired = errors.New("lease: lease has expired")

	// ErrStaleToken：Write 的 token 曾经发出过，但已被取代
	// （被抢占、已过期、已释放）。属于 fencing 拒绝，无副作用。
	ErrStaleToken = errors.New("lease: token was superseded")

	// ErrUnknownToken：Write 的 token 从未被发出过（大于已发出的
	// 最大 token）。设计决策（题面第四节）：与 ErrStaleToken 不归为
	// 同一类。旧 token 是 "输了竞争的合法客户端"，从未发出的 token
	// 是 "伪造或 bug 的客户端"，运维上应区别对待；但两者都被
	// fencing 拒绝，且都保证无副作用。
	ErrUnknownToken = errors.New("lease: token was never issued")
)
