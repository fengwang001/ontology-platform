package preauth

import "sync"

// System 是额度账务系统的入口，线程安全。
//
// 锁序（固定，避免死锁）：始终先 registryMu 后账户 mu，绝不反向。
//
// 时间与到期的关键约定：
//   - 账户 lastNow 记录最后一次“被接受操作”的时刻，只增不减；
//   - 所有校验都基于 expirySchedule.peekHolds(now) 非破坏性地观察 now 时刻
//     的有效持有，过期由 now 与 expiresDay 实时推导；
//   - 仅当操作通过全部校验、确定被接受时，才 foldPersist(now) 落盘到期
//     折叠并应用业务变更、推进 lastNow。因此任何被拒绝的操作（时钟回退、
//     已终结、超容差、额度不足、编号重复）都不改变任何状态；
//   - 查询同样使用 peekHolds，只读不写，结果只取决于已接受操作与 now。
type System struct {
	cfg Config

	registryMu sync.Mutex
	accounts   map[string]*account
	idOwner    map[string]*account
}

func NewSystem(cfg Config) *System {
	if cfg.ValidityDays <= 0 || cfg.ToleranceBPS < 0 {
		panic("preauth: invalid config")
	}
	return &System{
		cfg:      cfg,
		accounts: make(map[string]*account),
		idOwner:  make(map[string]*account),
	}
}

func (s *System) CreateAccount(accountID string, credit, now int64) error {
	if accountID == "" || credit <= 0 || now < 0 {
		return ErrInvalid
	}
	s.registryMu.Lock()
	defer s.registryMu.Unlock()
	if _, ok := s.accounts[accountID]; ok {
		return ErrDuplicateID
	}
	s.accounts[accountID] = newAccount(accountID, credit, now)
	return nil
}

func (s *System) AdjustCredit(accountID string, newCredit, now int64) error {
	if accountID == "" || newCredit <= 0 || now < 0 {
		return ErrInvalid
	}
	acc, err := s.account(accountID)
	if err != nil {
		return err
	}
	acc.mu.Lock()
	defer acc.mu.Unlock()
	if now < acc.lastNow {
		return ErrClockBackward
	}
	snap := acc.expiry.transientFold(now)
	if newCredit-acc.posted-acc.expiry.holds() < 0 {
		acc.expiry.undoFold(snap)
		return ErrInsufficient
	}
	acc.credit = newCredit
	acc.lastNow = now
	return nil
}

func (s *System) Authorize(authID, accountID string, amount, now int64) error {
	if authID == "" || accountID == "" || amount <= 0 || now < 0 {
		return ErrInvalid
	}
	s.registryMu.Lock()
	acc, ok := s.accounts[accountID]
	if !ok {
		s.registryMu.Unlock()
		return ErrInvalid
	}
	acc.mu.Lock()
	defer acc.mu.Unlock()
	if now < acc.lastNow {
		s.registryMu.Unlock()
		return ErrClockBackward
	}
	if _, dup := s.idOwner[authID]; dup {
		s.registryMu.Unlock()
		return ErrDuplicateID
	}
	// 临时折叠以观察 now 时刻有效持有；新授权尚未入堆，不受影响。
	snap := acc.expiry.transientFold(now)
	if acc.credit-acc.posted-acc.expiry.holds() < amount {
		acc.expiry.undoFold(snap)
		s.registryMu.Unlock()
		return ErrInsufficient
	}
	// 接受：折叠保留（不撤销即提交）。
	a := &authorization{
		id:         authID,
		accountID:  accountID,
		state:      StatusActive,
		authorized: amount,
		expiresDay: now + s.cfg.ValidityDays - 1,
	}
	a.entry = acc.expiry.place(authID, a.expiresDay, amount)
	acc.auths[authID] = a
	s.idOwner[authID] = acc
	acc.lastNow = now
	s.registryMu.Unlock()
	return nil
}

func (s *System) Increment(authID string, amount, now int64) error {
	if authID == "" || amount <= 0 || now < 0 {
		return ErrInvalid
	}
	acc, a, err := s.lookup(authID)
	if err != nil {
		return err
	}
	defer acc.mu.Unlock()
	if now < acc.lastNow {
		return ErrClockBackward
	}
	if a.statusAt(now) != StatusActive {
		return ErrClosed
	}
	snap := acc.expiry.transientFold(now)
	// 额外可用额度只按增量额：原持有仍在 holds() 中，不重复计。
	if acc.credit-acc.posted-acc.expiry.holds() < amount {
		acc.expiry.undoFold(snap)
		return ErrInsufficient
	}
	newAuthorized := a.authorized + amount
	newExpires := now + s.cfg.ValidityDays - 1
	newHold := newAuthorized - a.captured
	if newHold < 0 {
		newHold = 0
	}
	a.entry = acc.expiry.replace(a.entry, newExpires, newHold)
	a.authorized = newAuthorized
	a.expiresDay = newExpires
	acc.lastNow = now
	return nil
}

func (s *System) Capture(authID string, amount int64, final bool, now int64) error {
	if authID == "" || amount <= 0 || now < 0 {
		return ErrInvalid
	}
	acc, a, err := s.lookup(authID)
	if err != nil {
		return err
	}
	defer acc.mu.Unlock()
	if now < acc.lastNow {
		return ErrClockBackward
	}
	if a.statusAt(now) != StatusActive {
		return ErrClosed
	}
	snap := acc.expiry.transientFold(now)
	capLimit := a.authorized + (a.authorized*s.cfg.ToleranceBPS)/10000
	if a.captured+amount > capLimit {
		acc.expiry.undoFold(snap)
		return ErrOverTolerance
	}
	remaining := a.remainingHold()
	fromHold, extra := amount, int64(0)
	if fromHold > remaining {
		fromHold, extra = remaining, amount-remaining
	}
	if acc.credit-acc.posted-acc.expiry.holds() < extra {
		acc.expiry.undoFold(snap)
		return ErrInsufficient
	}
	acc.posted += amount
	acc.expiry.reduce(a.entry, fromHold)
	a.captured += amount
	if final {
		acc.expiry.release(a.entry)
		a.state = StatusFinalized
	}
	acc.lastNow = now
	return nil
}

func (s *System) Void(authID string, now int64) error {
	if authID == "" || now < 0 {
		return ErrInvalid
	}
	acc, a, err := s.lookup(authID)
	if err != nil {
		return err
	}
	defer acc.mu.Unlock()
	if now < acc.lastNow {
		return ErrClockBackward
	}
	if a.statusAt(now) != StatusActive {
		return ErrClosed
	}
	// 撤销路径在状态确认后不会再有业务拒绝；直接持久折叠。
	acc.expiry.transientFold(now)
	acc.expiry.release(a.entry)
	a.state = StatusVoided
	acc.lastNow = now
	return nil
}

func (s *System) Refund(authID string, amount, now int64) error {
	if authID == "" || amount <= 0 || now < 0 {
		return ErrInvalid
	}
	acc, a, err := s.lookup(authID)
	if err != nil {
		return err
	}
	defer acc.mu.Unlock()
	if now < acc.lastNow {
		return ErrClockBackward
	}
	// 退款不依赖持有、不改状态/有效期；已终结或已过期授权仍可退。
	if amount > a.captured-a.refunded {
		return ErrRefundExceeds
	}
	acc.posted -= amount
	a.refunded += amount
	acc.lastNow = now
	return nil
}

func (s *System) Auth(authID string, now int64) (AuthView, error) {
	if authID == "" || now < 0 {
		return AuthView{}, ErrInvalid
	}
	acc, a, err := s.lookup(authID)
	if err != nil {
		return AuthView{}, err
	}
	defer acc.mu.Unlock()
	if now < acc.lastNow {
		return AuthView{}, ErrClockBackward
	}
	return a.viewAt(now), nil
}

func (s *System) Available(accountID string, now int64) (int64, error) {
	v, err := s.Account(accountID, now)
	if err != nil {
		return 0, err
	}
	return v.Available, nil
}

func (s *System) Account(accountID string, now int64) (AccountView, error) {
	if accountID == "" || now < 0 {
		return AccountView{}, ErrInvalid
	}
	acc, err := s.account(accountID)
	if err != nil {
		return AccountView{}, err
	}
	acc.mu.Lock()
	defer acc.mu.Unlock()
	if now < acc.lastNow {
		return AccountView{}, ErrClockBackward
	}
	onHold := acc.expiry.peekHolds(now)
	return AccountView{
		ID:        acc.id,
		Credit:    acc.credit,
		Posted:    acc.posted,
		OnHold:    onHold,
		Available: acc.credit - acc.posted - onHold,
		Now:       now,
	}, nil
}

func (s *System) account(accountID string) (*account, error) {
	s.registryMu.Lock()
	defer s.registryMu.Unlock()
	acc, ok := s.accounts[accountID]
	if !ok {
		return nil, ErrNotFound
	}
	return acc, nil
}

// lookup 定位授权并持有其账户 mu；调用方必须 defer Unlock。
func (s *System) lookup(authID string) (*account, *authorization, error) {
	s.registryMu.Lock()
	acc, ok := s.idOwner[authID]
	s.registryMu.Unlock()
	if !ok {
		return nil, nil, ErrNotFound
	}
	acc.mu.Lock()
	a, ok := acc.auths[authID]
	if !ok {
		acc.mu.Unlock()
		return nil, nil, ErrNotFound
	}
	return acc, a, nil
}
