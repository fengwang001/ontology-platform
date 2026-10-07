package payledger

// 本文件是独立编写的朴素模型：直接按规则逐条实现，可用额度通过
// 遍历账户全部授权求和得到（O(历史授权数)），用于与生产实现交叉对照。

type naiveAuth struct {
	account  string
	cumAuth  int64
	captured int64
	refunded int64
	expiry   int64
	active   bool
	terminal AuthStatus
}

type naive struct {
	cfg      Config
	clock    int64
	hasClock bool
	credit   map[string]int64
	posted   map[string]int64
	auths    map[string]*naiveAuth
}

func newNaive(cfg Config) *naive {
	return &naive{
		cfg:    cfg,
		credit: make(map[string]int64),
		posted: make(map[string]int64),
		auths:  make(map[string]*naiveAuth),
	}
}

func (n *naive) checkClock(now int64) error {
	if n.hasClock && now < n.clock {
		return newErr(ErrClockRollback, "时钟回退")
	}
	return nil
}

func (n *naive) remaining(a *naiveAuth, now int64) int64 {
	if !a.active || now > a.expiry {
		return 0
	}
	if r := a.cumAuth - a.captured; r > 0 {
		return r
	}
	return 0
}

func (n *naive) status(a *naiveAuth, now int64) AuthStatus {
	if !a.active {
		return a.terminal
	}
	if now > a.expiry {
		return StatusExpired
	}
	return StatusActive
}

// available 朴素实现：遍历全部授权求和有效持有。
func (n *naive) available(acct string, now int64) (int64, error) {
	credit, ok := n.credit[acct]
	if !ok {
		return 0, newErr(ErrAccountNotFound, "账户不存在")
	}
	var holds int64
	for _, a := range n.auths {
		if a.account == acct {
			holds += n.remaining(a, now)
		}
	}
	return credit - n.posted[acct] - holds, nil
}

func (n *naive) createAccount(id string, limit, now int64) error {
	if id == "" || limit < 0 {
		return newErr(ErrInvalidParam, "参数非法")
	}
	if _, ok := n.credit[id]; ok {
		return newErr(ErrInvalidParam, "账户已存在")
	}
	if err := n.checkClock(now); err != nil {
		return err
	}
	n.credit[id] = limit
	n.clock, n.hasClock = now, true
	return nil
}

func (n *naive) authorize(acct, id string, amount, now int64) error {
	if acct == "" || id == "" || amount <= 0 {
		return newErr(ErrInvalidParam, "参数非法")
	}
	if err := n.checkClock(now); err != nil {
		return err
	}
	if _, dup := n.auths[id]; dup {
		return newErr(ErrDuplicateAuthID, "编号重复")
	}
	if _, ok := n.credit[acct]; !ok {
		return newErr(ErrAccountNotFound, "账户不存在")
	}
	avail, _ := n.available(acct, now)
	if avail < amount {
		return newErr(ErrInsufficientFunds, "额度不足")
	}
	n.auths[id] = &naiveAuth{
		account: acct, cumAuth: amount, expiry: now + n.cfg.ExpiryDays, active: true,
	}
	n.clock, n.hasClock = now, true
	return nil
}

func (n *naive) increment(id string, amount, now int64) error {
	if id == "" || amount <= 0 {
		return newErr(ErrInvalidParam, "参数非法")
	}
	if err := n.checkClock(now); err != nil {
		return err
	}
	a, ok := n.auths[id]
	if !ok {
		return newErr(ErrAuthNotFound, "授权不存在")
	}
	if n.status(a, now) != StatusActive {
		return newErr(ErrAuthTerminated, "授权已终结")
	}
	avail, _ := n.available(a.account, now)
	if avail < amount {
		return newErr(ErrInsufficientFunds, "额度不足")
	}
	a.cumAuth += amount
	a.expiry = now + n.cfg.ExpiryDays
	n.clock, n.hasClock = now, true
	return nil
}

func (n *naive) capture(id string, amount int64, final bool, now int64) error {
	if id == "" || amount <= 0 {
		return newErr(ErrInvalidParam, "参数非法")
	}
	if err := n.checkClock(now); err != nil {
		return err
	}
	a, ok := n.auths[id]
	if !ok {
		return newErr(ErrAuthNotFound, "授权不存在")
	}
	if n.status(a, now) != StatusActive {
		return newErr(ErrAuthTerminated, "授权已终结")
	}
	if a.captured+amount > allowedCapture(a.cumAuth, n.cfg.ToleranceBps) {
		return newErr(ErrOverTolerance, "超容差")
	}
	if excess := amount - n.remaining(a, now); excess > 0 {
		avail, _ := n.available(a.account, now)
		if avail < excess {
			return newErr(ErrInsufficientFunds, "额度不足")
		}
	}
	a.captured += amount
	n.posted[a.account] += amount
	if final {
		a.active = false
		a.terminal = StatusFinalCaptured
	}
	n.clock, n.hasClock = now, true
	return nil
}

func (n *naive) void(id string, now int64) error {
	if id == "" {
		return newErr(ErrInvalidParam, "参数非法")
	}
	if err := n.checkClock(now); err != nil {
		return err
	}
	a, ok := n.auths[id]
	if !ok {
		return newErr(ErrAuthNotFound, "授权不存在")
	}
	if n.status(a, now) != StatusActive {
		return newErr(ErrAuthTerminated, "授权已终结")
	}
	a.active = false
	a.terminal = StatusVoided
	n.clock, n.hasClock = now, true
	return nil
}

func (n *naive) refund(id string, amount, now int64) error {
	if id == "" || amount <= 0 {
		return newErr(ErrInvalidParam, "参数非法")
	}
	if err := n.checkClock(now); err != nil {
		return err
	}
	a, ok := n.auths[id]
	if !ok {
		return newErr(ErrAuthNotFound, "授权不存在")
	}
	if amount > a.captured-a.refunded {
		return newErr(ErrRefundExceeds, "超出可退额")
	}
	a.refunded += amount
	n.posted[a.account] -= amount
	n.clock, n.hasClock = now, true
	return nil
}

func (n *naive) adjustCredit(id string, limit, now int64) error {
	if limit < 0 {
		return newErr(ErrInvalidParam, "参数非法")
	}
	if err := n.checkClock(now); err != nil {
		return err
	}
	if _, ok := n.credit[id]; !ok {
		return newErr(ErrAccountNotFound, "账户不存在")
	}
	avail, _ := n.available(id, now)
	if avail-(n.credit[id]-limit) < 0 { // 新可用 = 新额度 - 已入账 - 有效持有
		return newErr(ErrInsufficientFunds, "额度不足")
	}
	n.credit[id] = limit
	n.clock, n.hasClock = now, true
	return nil
}
