package remittance

import "math/bits"

// naiveModel 是按需求文字直接写成的独立参考实现：
// 不做任何增量维护，每次需要时扫描该汇款人全部历史汇款重算占用；
// 审核状态在每个操作开始时按 now 全量扫描重算。
// 它故意慢、直白，只用于与生产 Engine 做随机对照。

type naiveQuote struct {
	id        int64
	sender    string
	amount    int64
	ratePPM   int64
	createdAt int64
	expiresAt int64
	consumed  bool
}

type naiveTransfer struct {
	id             int64
	sender         string
	payee          string
	quoteID        int64
	amount         int64
	ratePPM        int64
	target, occ    int64
	day            int64
	status         TransferStatus
	submittedAt    int64
	reviewDeadline int64
	decidedAt      int64
	idemKey        string
}

type naiveIdem struct {
	sender string
	quote  int64
	payee  string
	tid    int64
	status TransferStatus
	target int64
	occ    int64
	dl     int64
}

type naiveModel struct {
	cfg        Config
	limits     map[string]Limits
	sanctioned map[string]struct{}
	quotes     map[int64]*naiveQuote
	transfers  []*naiveTransfer
	byID       map[int64]*naiveTransfer
	idem       map[string]map[string]naiveIdem
	lastNow    int64
	nextQuote  int64
	nextXfer   int64
}

func newNaive(cfg Config) *naiveModel {
	return &naiveModel{
		cfg:        cfg,
		limits:     map[string]Limits{},
		sanctioned: map[string]struct{}{},
		quotes:     map[int64]*naiveQuote{},
		byID:       map[int64]*naiveTransfer{},
		idem:       map[string]map[string]naiveIdem{},
	}
}

func (m *naiveModel) addSender(id string, lim Limits) { m.limits[id] = lim }
func (m *naiveModel) addSanction(p string)            { m.sanctioned[p] = struct{}{} }
func (m *naiveModel) delSanction(p string)            { delete(m.sanctioned, p) }

// settle 全量重算：把所有 deadline < now 的待审核汇款置为失败，
// decidedAt = deadline+1。朴素模型每次操作都调用，保证与惰性物化一致。
func (m *naiveModel) settle(now int64) {
	for _, t := range m.transfers {
		if t.status == StatusPending && t.reviewDeadline < now {
			t.status = StatusFailed
			t.decidedAt = t.reviewDeadline + 1
		}
	}
}

// liveAt 该笔在 now（属于 day）时是否仍占用：
// 成功出款永久占用；待审核占用；逾期/拒绝/撤回不占用。
// settle 已先把逾期 pending 改写为 Failed。
func (m *naiveModel) live(t *naiveTransfer) bool {
	return t.status == StatusSucceeded || t.status == StatusPending
}

func (m *naiveModel) usage(sender string, now int64) (dayUsed, annualUsed int64) {
	day := now / secondsPerDay
	for _, t := range m.transfers {
		if t.sender != sender || !m.live(t) {
			continue
		}
		if t.day == day {
			dayUsed += t.occ
		}
		if day-t.day < annualDays {
			annualUsed += t.occ
		}
	}
	return
}

func nFloor(a, b int64) int64 {
	hi, lo := bits.Mul64(uint64(a), uint64(b))
	q, _ := bits.Div64(hi, lo, uint64(rateScale))
	return int64(q)
}

func nCeil(a, b int64) int64 {
	hi, lo := bits.Mul64(uint64(a), uint64(b))
	lo2, c := bits.Add64(lo, uint64(rateScale)-1, 0)
	q, _ := bits.Div64(hi+c, lo2, uint64(rateScale))
	return int64(q)
}

// 返回结果（与 Engine 形态对齐所需字段）与错误码。
type naiveSubmitOut struct {
	tid    int64
	target int64
	occ    int64
	status TransferStatus
	dl     int64
	replay bool
}

func (m *naiveModel) applyQuote(sender, ccy1, ccy2 string, amount, rate, now int64) (int64, ErrorCode) {
	if sender == "" || ccy1 == "" || ccy2 == "" || ccy1 == ccy2 ||
		amount <= 0 || rate <= 0 || now < 0 || m.cfg.QuoteTTLSeconds <= 0 {
		return 0, ErrCodeInvalidArgument
	}
	if _, ok := m.limits[sender]; !ok {
		return 0, ErrCodeInvalidArgument
	}
	if now < m.lastNow {
		return 0, ErrCodeClockBackward
	}
	m.nextQuote++
	m.quotes[m.nextQuote] = &naiveQuote{
		id: m.nextQuote, sender: sender, amount: amount, ratePPM: rate,
		createdAt: now, expiresAt: now + m.cfg.QuoteTTLSeconds,
	}
	m.lastNow = now
	return m.nextQuote, 0
}

func (m *naiveModel) submit(sender string, quoteID int64, payee, key string, now int64) (naiveSubmitOut, ErrorCode) {
	switch {
	case sender == "" || payee == "" || key == "" || quoteID <= 0 || now < 0 ||
		m.cfg.ReviewSeconds <= 0:
		return naiveSubmitOut{}, ErrCodeInvalidArgument
	}
	if _, ok := m.limits[sender]; !ok {
		return naiveSubmitOut{}, ErrCodeInvalidArgument
	}
	if now < m.lastNow {
		return naiveSubmitOut{}, ErrCodeClockBackward
	}
	if _, bad := m.sanctioned[payee]; bad {
		return naiveSubmitOut{}, ErrCodeSanctionedPayee
	}
	if recs, ok := m.idem[sender]; ok {
		if rec, ok2 := recs[key]; ok2 {
			if rec.quote != quoteID || rec.payee != payee {
				return naiveSubmitOut{}, ErrCodeIdempotencyConflict
			}
			// 接受重放：先 settle 再推进时钟，返回首次原始结果。
			m.settle(now)
			m.lastNow = now
			return naiveSubmitOut{
				tid: rec.tid, target: rec.target, occ: rec.occ,
				status: rec.status, dl: rec.dl, replay: true,
			}, 0
		}
	}
	q := m.quotes[quoteID]
	if q == nil || q.consumed || q.sender != sender {
		return naiveSubmitOut{}, ErrCodeQuoteNotFound
	}
	if now > q.expiresAt {
		return naiveSubmitOut{}, ErrCodeQuoteExpired
	}
	target := nFloor(q.amount, q.ratePPM)
	occ := nCeil(q.amount, q.ratePPM)
	day := now / secondsPerDay

	// 限额校验必须在“settle 后”的视图上做；若拒绝需字段级回滚，
	// 保证被拒绝的提交不物化任何逾期（与 Engine 的纯函数投影一致）。
	m.settle(now)
	lim := m.limits[sender]
	dayUsed, annualUsed := m.usage(sender, now)
	singleOK := occ <= lim.Single
	dailyOK := dayUsed <= lim.Daily
	annualOK := annualUsed <= lim.Annual

	var code ErrorCode
	switch {
	case !singleOK:
		code = ErrCodeSingleLimitExceeded
	case !dailyOK:
		code = ErrCodeDailyLimitExceeded
	case !annualOK:
		code = ErrCodeAnnualLimitExceeded
	}
	if code != 0 {
		for _, t := range m.transfers {
			if t.status == StatusFailed && t.decidedAt == t.reviewDeadline+1 &&
				t.reviewDeadline < now {
				t.status = StatusPending
				t.decidedAt = 0
			}
		}
		return naiveSubmitOut{}, code
	}

	m.nextXfer++
	t := &naiveTransfer{
		id: m.nextXfer, sender: sender, payee: payee, quoteID: quoteID,
		amount: q.amount, ratePPM: q.ratePPM, target: target, occ: occ,
		day: day, submittedAt: now, idemKey: key,
	}
	if target >= m.cfg.ReviewThreshold {
		t.status = StatusPending
		t.reviewDeadline = now + m.cfg.ReviewSeconds
	} else {
		t.status = StatusSucceeded
		t.decidedAt = now
	}
	m.transfers = append(m.transfers, t)
	m.byID[t.id] = t
	q.consumed = true
	if m.idem[sender] == nil {
		m.idem[sender] = map[string]naiveIdem{}
	}
	m.idem[sender][key] = naiveIdem{
		sender: sender, quote: quoteID, payee: payee, tid: t.id,
		status: t.status, target: target, occ: occ, dl: t.reviewDeadline,
	}
	m.lastNow = now
	return naiveSubmitOut{
		tid: t.id, target: target, occ: occ,
		status: t.status, dl: t.reviewDeadline,
	}, 0
}

func (m *naiveModel) review(tid, now int64, approve bool) ErrorCode {
	if tid <= 0 || now < 0 {
		return ErrCodeInvalidArgument
	}
	if now < m.lastNow {
		return ErrCodeClockBackward
	}
	t := m.byID[tid]
	if t == nil {
		return ErrCodeTransferNotFound
	}
	// 状态判定用“纯 now 视图”：不先 settle，避免拒绝路径改写状态。
	if t.status != StatusPending || t.reviewDeadline < now {
		return ErrCodeIllegalState
	}
	// 接受：先 settle 其他逾期单，再处理本单。
	m.settle(now)
	if approve {
		t.status = StatusSucceeded
		t.decidedAt = now
	} else {
		t.status = StatusFailed
		t.decidedAt = now
	}
	m.lastNow = now
	return 0
}

func (m *naiveModel) get(tid, now int64) (status TransferStatus, decidedAt int64, day int64, code ErrorCode) {
	if tid <= 0 || now < 0 {
		return 0, 0, 0, ErrCodeInvalidArgument
	}
	if now < m.lastNow {
		return 0, 0, 0, ErrCodeClockBackward
	}
	t := m.byID[tid]
	if t == nil {
		return 0, 0, 0, ErrCodeTransferNotFound
	}
	m.settle(now)
	m.lastNow = now
	return t.status, t.decidedAt, t.day, 0
}

func (m *naiveModel) useQuery(sender string, now int64) (int64, int64, int64, ErrorCode) {
	if sender == "" || now < 0 {
		return 0, 0, 0, ErrCodeInvalidArgument
	}
	if now < m.lastNow {
		return 0, 0, 0, ErrCodeClockBackward
	}
	if _, ok := m.limits[sender]; !ok {
		return 0, 0, 0, ErrCodeInvalidArgument
	}
	m.settle(now)
	m.lastNow = now
	d, a := m.usage(sender, now)
	return now / secondsPerDay, d, a, 0
}
