package payledger

import "sync"

// Ledger 是支付预授权账务系统的外观入口。
// 所有方法并发安全：内部以单一互斥锁串行化，结果等价于某串行顺序。
// 时钟规则：每个变更操作携带 now，不得小于上一次被接受操作的 now，
// 否则报时钟回退；被拒绝的操作不改变任何状态与时钟。查询不触碰时钟。
type Ledger struct {
	mu       sync.Mutex
	cfg      Config
	lastNow  int64
	hasClock bool

	accounts map[string]*account
	auths    map[string]*authorization // 全局唯一编号索引，含已终结授权
}

// NewLedger 创建账本。cfg.ExpiryDays 须 >= 0，cfg.ToleranceBps 须 >= 0。
func NewLedger(cfg Config) *Ledger {
	if cfg.ExpiryDays < 0 {
		cfg.ExpiryDays = 0
	}
	if cfg.ToleranceBps < 0 {
		cfg.ToleranceBps = 0
	}
	return &Ledger{
		cfg:      cfg,
		accounts: make(map[string]*account),
		auths:    make(map[string]*authorization),
	}
}

// CreateAccount 开户。creditLimit 为信用额度（最小货币单位，须 >= 0）。
func (l *Ledger) CreateAccount(accountID string, creditLimit, now int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.createAccount(accountID, creditLimit, now)
}

// AdjustCredit 调整信用额度。调高立即生效；调低不得使可用额度为负，
// 否则整体拒绝并报额度不足。
func (l *Ledger) AdjustCredit(accountID string, newLimit, now int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.adjustCredit(accountID, newLimit, now)
}

// Authorize 创建授权：占用 amount 的持有，有效期至 now+E（含）。
// 授权编号全局唯一，复用任何已出现过的编号报编号重复。
func (l *Ledger) Authorize(accountID, authID string, amount, now int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.authorize(accountID, authID, amount, now)
}

// Increment 增量授权：为有效授权追加 amount，有效期重置为 now+E（含）。
// 额外可用额度仅按增量金额校验；失败不影响原授权及其有效期。
func (l *Ledger) Increment(authID string, amount, now int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.increment(authID, amount, now)
}

// Capture 捕获：把持有转为已入账。超出剩余持有的部分须由当前可用额度
// 覆盖，成功则该授权持有清零。final 为终捕：成功后释放剩余持有并终结授权。
func (l *Ledger) Capture(authID string, amount int64, final bool, now int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.capture(authID, amount, final, now)
}

// Void 撤销授权：释放剩余持有，已捕获部分不受影响，授权终结。
func (l *Ledger) Void(authID string, now int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.void(authID, now)
}

// Refund 退款：只减少已入账余额，不恢复持有，不改变授权状态与有效期。
// 退款额不得超过该授权累计已捕获额减累计已退款额；已终结授权仍可退款。
func (l *Ledger) Refund(authID string, amount, now int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.refund(authID, amount, now)
}

// Available 查询账户在 now 时刻的可用额度。
// 开销与账户历史授权总数、全部账户数均无关（见 account.available）。
// 查询只读，不修改任何语义状态，也不触碰时钟。
func (l *Ledger) Available(accountID string, now int64) (int64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.available(accountID, now)
}

// AuthSnapshot 查询授权在 now 时刻的状态与累计额。只读，不修改状态。
func (l *Ledger) AuthSnapshot(authID string, now int64) (AuthSnapshot, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.snapshot(authID, now)
}
