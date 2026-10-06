package remittance

import (
	"fmt"
	"math"
	"math/bits"
	"sync"
)

// System 为跨境汇款系统门面，协调报价、限额账本、幂等与审核模块。
// 所有公开方法可并发调用，内部以互斥锁串行化，
// 结果等价于某个串行顺序；相同操作序列重放得到完全相同的结果。
//
// 时钟：每个操作携带 now，不得小于上一次被接受操作的 now，
// 否则报 ErrClockRegression；被拒绝的操作不改变任何状态、额度占用与时钟。
//
// 懒过期：审核逾期不依赖任何操作触发。每个被接受的操作先以自身 now
// 做过期扫描，故任意时刻查询到的状态只取决于已被接受的操作与查询的 now。
type System struct {
	mu  sync.Mutex
	cfg Config

	lastNow     int64
	quoteSeq    int64
	remSeq      int64
	quotes      map[string]*quote
	remittances map[string]*remittance
	ledgers     map[string]*limitLedger
	idem        *idemStore
	sanctioned  map[string]bool
	expiry      expiryQueue
}

// NewSystem 构造系统；配置非法（任一参数为负）时返回 ErrInvalidParams。
func NewSystem(cfg Config) (*System, error) {
	if cfg.SingleLimit < 0 || cfg.DayLimit < 0 || cfg.YearLimit < 0 ||
		cfg.QuoteTTL < 0 || cfg.ReviewThreshold < 0 || cfg.ReviewTimeout < 0 {
		return nil, ErrInvalidParams
	}
	return &System{
		cfg:         cfg,
		lastNow:     -1,
		quotes:      make(map[string]*quote),
		remittances: make(map[string]*remittance),
		ledgers:     make(map[string]*limitLedger),
		idem:        newIdemStore(),
		sanctioned:  make(map[string]bool),
	}, nil
}

// checkClock 校验时钟回退。调用前须已完成参数校验（参数非法优先）。
func (s *System) checkClock(now int64) error {
	if now < s.lastNow {
		return fmt.Errorf("%w: now=%d last=%d", ErrClockRegression, now, s.lastNow)
	}
	return nil
}

// sweep 将在 now 已逾期的待审核汇款置为失败并释放其占用，
// 返回当前日序号。仅在时钟校验通过后调用。
func (s *System) sweep(now int64) int64 {
	curDay := dayIndex(now)
	s.expiry.popExpired(now, func(id string) {
		r := s.remittances[id]
		if r.status != StatusPendingReview {
			return // 已被批准/拒绝/撤回
		}
		r.status = StatusFailed
		s.ledgerFor(r.remitter, curDay).releaseHold(r.day, r.hold)
	})
	return curDay
}

// ledgerFor 返回汇款人的账本（不存在则创建），并推进到当前日序号。
func (s *System) ledgerFor(remitter string, curDay int64) *limitLedger {
	l, ok := s.ledgers[remitter]
	if !ok {
		l = newLimitLedger()
		s.ledgers[remitter] = l
	}
	l.advance(curDay)
	return l
}

// accept 在操作被接受时推进时钟。
func (s *System) accept(now int64) { s.lastNow = now }

// RequestQuote 申请锁汇报价，返回报价编号。
// 报价自 now 起有效 QuoteTTL 秒，至多使用一次。
func (s *System) RequestQuote(remitter string, srcAmount, rate, now int64) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if remitter == "" || srcAmount <= 0 || rate <= 0 || now < 0 {
		return "", ErrInvalidParams
	}
	if err := s.checkClock(now); err != nil {
		return "", err
	}
	s.sweep(now)
	s.quoteSeq++
	id := fmt.Sprintf("Q-%d", s.quoteSeq)
	s.quotes[id] = &quote{
		id: id, remitter: remitter, srcAmount: srcAmount, rate: rate, createdAt: now,
	}
	s.accept(now)
	return id, nil
}

// Submit 提交汇款。错误优先级：参数非法 > 时钟回退 > 制裁 > 幂等冲突 >
// 报价不存在/已消耗 > 报价已过期 > 单笔限额 > 日限额 > 年度额度。
// 幂等键与参数完全相同的重复提交在制裁检查之后直接返回原结果。
func (s *System) Submit(remitter, idemKey, quoteID, payee string, now int64) (SubmitResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if remitter == "" || idemKey == "" || quoteID == "" || payee == "" || now < 0 {
		return SubmitResult{}, ErrInvalidParams
	}
	if err := s.checkClock(now); err != nil {
		return SubmitResult{}, err
	}
	curDay := s.sweep(now)
	if s.sanctioned[payee] {
		return SubmitResult{}, ErrSanctioned
	}
	if e, ok := s.idem.lookup(idemKey); ok {
		if e.remitter == remitter && e.quoteID == quoteID && e.payee == payee {
			s.accept(now)
			return e.result, nil
		}
		return SubmitResult{}, fmt.Errorf("%w: key=%q", ErrIdemConflict, idemKey)
	}
	q, ok := s.quotes[quoteID]
	if !ok || q.remitter != remitter {
		return SubmitResult{}, fmt.Errorf("%w: id=%q", ErrQuoteNotFound, quoteID)
	}
	if q.consumed {
		return SubmitResult{}, fmt.Errorf("%w: id=%q", ErrQuoteConsumed, quoteID)
	}
	if q.expired(now, s.cfg.QuoteTTL) {
		return SubmitResult{}, fmt.Errorf("%w: id=%q", ErrQuoteExpired, quoteID)
	}
	target, hold, overflow := mulDiv(q.srcAmount, q.rate)
	if overflow || hold > s.cfg.SingleLimit {
		return SubmitResult{}, ErrSingleLimit
	}
	l := s.ledgerFor(remitter, curDay)
	if hold > s.cfg.DayLimit-l.dayUsed() {
		return SubmitResult{}, ErrDayLimit
	}
	if hold > s.cfg.YearLimit-l.rollingUsed() {
		return SubmitResult{}, ErrYearLimit
	}
	// 接受：消耗报价、占用额度、记录幂等键。
	q.consumed = true
	l.addHold(curDay, hold)
	s.remSeq++
	id := fmt.Sprintf("R-%d", s.remSeq)
	status := StatusSucceeded
	deadline := saturatingAdd(now, s.cfg.ReviewTimeout)
	if target >= s.cfg.ReviewThreshold {
		status = StatusPendingReview
		s.expiry.push(deadline, id)
	}
	s.remittances[id] = &remittance{
		id: id, remitter: remitter, payee: payee, quoteID: quoteID,
		srcAmount: q.srcAmount, rate: q.rate, target: target, hold: hold,
		day: curDay, submitNow: now, deadline: deadline, status: status,
	}
	res := SubmitResult{RemittanceID: id, TargetAmount: target, HoldAmount: hold, Status: status}
	s.idem.save(idemKey, idemEntry{remitter: remitter, quoteID: quoteID, payee: payee, result: res})
	s.accept(now)
	return res, nil
}

// Approve 审核批准：须在提交起 ReviewTimeout 秒内（含恰等时刻）。
func (s *System) Approve(id string, now int64) error { return s.review(id, now, true) }

// Reject 审核拒绝：释放全部占用，归还到原占用日序号。
func (s *System) Reject(id string, now int64) error { return s.review(id, now, false) }

// Withdraw 汇款人撤回，仅针对待审核汇款，等同于拒绝。
func (s *System) Withdraw(id string, now int64) error { return s.review(id, now, false) }

// review 处理批准/拒绝/撤回。
// 错误优先级：参数非法 > 时钟回退 > 汇款不存在 > 当前状态不允许（含已逾期失败）。
func (s *System) review(id string, now int64, approve bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == "" || now < 0 {
		return ErrInvalidParams
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	curDay := s.sweep(now)
	r, ok := s.remittances[id]
	if !ok {
		return fmt.Errorf("%w: id=%q", ErrNotFound, id)
	}
	if r.status != StatusPendingReview {
		return fmt.Errorf("%w: id=%q status=%s", ErrInvalidState, id, r.status)
	}
	if approve {
		r.status = StatusSucceeded
	} else {
		r.status = StatusFailed
		s.ledgerFor(r.remitter, curDay).releaseHold(r.day, r.hold)
	}
	s.accept(now)
	return nil
}

// GetRemittance 查询汇款在 now 的状态（逾期失败在此刻自然生效）。
func (s *System) GetRemittance(id string, now int64) (RemittanceView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == "" || now < 0 {
		return RemittanceView{}, ErrInvalidParams
	}
	if err := s.checkClock(now); err != nil {
		return RemittanceView{}, err
	}
	s.sweep(now)
	r, ok := s.remittances[id]
	if !ok {
		return RemittanceView{}, fmt.Errorf("%w: id=%q", ErrNotFound, id)
	}
	s.accept(now)
	return RemittanceView{
		ID: r.id, Remitter: r.remitter, Payee: r.payee, QuoteID: r.quoteID,
		TargetAmount: r.target, HoldAmount: r.hold, DayIndex: r.day,
		SubmitNow: r.submitNow, Deadline: r.deadline, Status: r.status,
	}, nil
}

// QueryUsage 查询汇款人在 now 的当日与滚动年度占用。
func (s *System) QueryUsage(remitter string, now int64) (Usage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if remitter == "" || now < 0 {
		return Usage{}, ErrInvalidParams
	}
	if err := s.checkClock(now); err != nil {
		return Usage{}, err
	}
	curDay := s.sweep(now)
	l := s.ledgerFor(remitter, curDay)
	s.accept(now)
	return Usage{DayIndex: curDay, DayUsed: l.dayUsed(), RollingYearUsed: l.rollingUsed()}, nil
}

// AddSanctionedPayee 将收款人加入制裁名单（管理操作，不携带 now，不影响时钟）。
func (s *System) AddSanctionedPayee(payee string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sanctioned[payee] = true
}

// RemoveSanctionedPayee 将收款人移出制裁名单。
func (s *System) RemoveSanctionedPayee(payee string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sanctioned, payee)
}

// mulDiv 计算 a*b/1e6 的向下取整（目标额）与向上取整（占用额）。
// 乘积以 128 位计算；商超出 int64 时置 overflow（此时占用额必超任何合法限额）。
func mulDiv(a, b int64) (floor, ceil int64, overflow bool) {
	hi, lo := bits.Mul64(uint64(a), uint64(b))
	const d = uint64(microMillion)
	if hi >= d {
		return 0, 0, true
	}
	q, r := bits.Div64(hi, lo, d)
	if q > math.MaxInt64 || (r > 0 && q == math.MaxInt64) {
		return 0, 0, true
	}
	floor = int64(q)
	ceil = floor
	if r > 0 {
		ceil++
	}
	return floor, ceil, false
}

// saturatingAdd 饱和加法，防止截止时刻溢出。
func saturatingAdd(a, b int64) int64 {
	if b > math.MaxInt64-a {
		return math.MaxInt64
	}
	return a + b
}
