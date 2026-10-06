package preauth

// 本文件是“独立朴素模型”：刻意与生产实现采用完全不同的数据组织方式，
// 每次计算都全表扫描账户下的全部授权，直接按自然语言规则重算，
// 不使用任何堆、惰性删除或缓存。用于随机差分测试的对照基准（oracle）。

type naiveAuth struct {
	id, accountID string
	state         Status // active/voided/finalized（过期仍记 active，用 expiresDay 推导）
	authorized    int64
	captured      int64
	refunded      int64
	expiresDay    int64
}

type naiveAccount struct {
	credit  int64
	lastNow int64
	auths   map[string]*naiveAuth
}

type naiveSystem struct {
	e, bps   int64
	accounts map[string]*naiveAccount
	known    map[string]struct{}
}

func newNaive(e, bps int64) *naiveSystem {
	return &naiveSystem{
		e:        e,
		bps:      bps,
		accounts: map[string]*naiveAccount{},
		known:    map[string]struct{}{},
	}
}

func (n *naiveSystem) status(a *naiveAuth, now int64) Status {
	if a.state != StatusActive {
		return a.state
	}
	if now > a.expiresDay {
		return StatusExpired
	}
	return StatusActive
}

// holds 全表扫描：只统计当前有效的授权持有的剩余（authorized-captured）。
func (n *naiveSystem) holds(acc *naiveAccount, now int64) int64 {
	var h int64
	for _, a := range acc.auths {
		if n.status(a, now) == StatusActive {
			r := a.authorized - a.captured
			if r > 0 {
				h += r
			}
		}
	}
	return h
}

func (n *naiveSystem) available(acc *naiveAccount, now int64) int64 {
	var posted int64
	for _, a := range acc.auths {
		posted += a.captured - a.refunded
	}
	return acc.credit - posted - n.holds(acc, now)
}

func (n *naiveSystem) queryAvailable(accID string, now int64) (int64, error) {
	if accID == "" || now < 0 {
		return 0, ErrInvalid
	}
	acc, ok := n.accounts[accID]
	if !ok {
		return 0, ErrNotFound
	}
	if now < acc.lastNow {
		return 0, ErrClockBackward
	}
	return n.available(acc, now), nil
}

func (n *naiveSystem) queryAuth(id string, now int64) (AuthView, error) {
	if id == "" || now < 0 {
		return AuthView{}, ErrInvalid
	}
	acc, a, err := n.owner(id)
	if err != nil {
		return AuthView{}, err
	}
	if now < acc.lastNow {
		return AuthView{}, ErrClockBackward
	}
	st := n.status(a, now)
	v := AuthView{
		ID:         a.id,
		AccountID:  a.accountID,
		Status:     st,
		Authorized: a.authorized,
		Captured:   a.captured,
		Refunded:   a.refunded,
		ExpiresDay: a.expiresDay,
	}
	if st == StatusActive {
		r := a.authorized - a.captured
		if r > 0 {
			v.Remaining = r
		}
	}
	return v, nil
}

func (n *naiveSystem) createAccount(id string, credit, now int64) error {
	if id == "" || credit <= 0 || now < 0 {
		return ErrInvalid
	}
	if _, ok := n.accounts[id]; ok {
		return ErrDuplicateID
	}
	n.accounts[id] = &naiveAccount{credit: credit, lastNow: now, auths: map[string]*naiveAuth{}}
	return nil
}

func (n *naiveSystem) adjust(id string, credit, now int64) error {
	if id == "" || credit <= 0 || now < 0 {
		return ErrInvalid
	}
	acc, ok := n.accounts[id]
	if !ok {
		return ErrNotFound
	}
	if now < acc.lastNow {
		return ErrClockBackward
	}
	var posted int64
	for _, a := range acc.auths {
		posted += a.captured - a.refunded
	}
	if credit-posted-n.holds(acc, now) < 0 {
		return ErrInsufficient
	}
	acc.credit = credit
	acc.lastNow = now
	return nil
}

func (n *naiveSystem) authorize(id, accID string, amount, now int64) error {
	if id == "" || accID == "" || amount <= 0 || now < 0 {
		return ErrInvalid
	}
	acc, ok := n.accounts[accID]
	if !ok {
		return ErrInvalid
	}
	if now < acc.lastNow {
		return ErrClockBackward
	}
	if _, dup := n.known[id]; dup {
		return ErrDuplicateID
	}
	if n.available(acc, now) < amount {
		return ErrInsufficient
	}
	acc.auths[id] = &naiveAuth{
		id: id, accountID: accID, state: StatusActive,
		authorized: amount, expiresDay: now + n.e - 1,
	}
	n.known[id] = struct{}{}
	acc.lastNow = now
	return nil
}

func (n *naiveSystem) owner(id string) (*naiveAccount, *naiveAuth, error) {
	for _, acc := range n.accounts {
		if a, ok := acc.auths[id]; ok {
			return acc, a, nil
		}
	}
	return nil, nil, ErrNotFound
}

func (n *naiveSystem) increment(id string, amount, now int64) error {
	if id == "" || amount <= 0 || now < 0 {
		return ErrInvalid
	}
	acc, a, err := n.owner(id)
	if err != nil {
		return err
	}
	if now < acc.lastNow {
		return ErrClockBackward
	}
	if n.status(a, now) != StatusActive {
		return ErrClosed
	}
	if n.available(acc, now) < amount {
		return ErrInsufficient
	}
	a.authorized += amount
	a.expiresDay = now + n.e - 1
	acc.lastNow = now
	return nil
}

func (n *naiveSystem) capture(id string, amount int64, final bool, now int64) error {
	if id == "" || amount <= 0 || now < 0 {
		return ErrInvalid
	}
	acc, a, err := n.owner(id)
	if err != nil {
		return err
	}
	if now < acc.lastNow {
		return ErrClockBackward
	}
	if n.status(a, now) != StatusActive {
		return ErrClosed
	}
	limit := a.authorized + (a.authorized*n.bps)/10000
	if a.captured+amount > limit {
		return ErrOverTolerance
	}
	remaining := a.authorized - a.captured
	extra := amount - remaining
	if amount <= remaining {
		extra = 0
	}
	if n.available(acc, now) < extra {
		return ErrInsufficient
	}
	a.captured += amount
	if final {
		a.state = StatusFinalized
	}
	acc.lastNow = now
	return nil
}

func (n *naiveSystem) void(id string, now int64) error {
	if id == "" || now < 0 {
		return ErrInvalid
	}
	acc, a, err := n.owner(id)
	if err != nil {
		return err
	}
	if now < acc.lastNow {
		return ErrClockBackward
	}
	if n.status(a, now) != StatusActive {
		return ErrClosed
	}
	a.state = StatusVoided
	acc.lastNow = now
	return nil
}

func (n *naiveSystem) refund(id string, amount, now int64) error {
	if id == "" || amount <= 0 || now < 0 {
		return ErrInvalid
	}
	acc, a, err := n.owner(id)
	if err != nil {
		return err
	}
	if now < acc.lastNow {
		return ErrClockBackward
	}
	if amount > a.captured-a.refunded {
		return ErrRefundExceeds
	}
	a.refunded += amount
	acc.lastNow = now
	return nil
}
