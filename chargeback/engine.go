package chargeback

import "sync"

// txnReasonKey 是“同一交易、同一原因”的索引键。
type txnReasonKey struct {
	txnID  string
	reason Reason
}

// profileKey 是重复扣款依据交易的索引键：同卡、同商户、同金额。
type profileKey struct {
	cardID     string
	merchantID string
	amount     int64
}

// Engine 是拒付案件管理与资金扣回引擎。
//
// 所有公开方法持有同一把互斥锁，因此并发调用等价于某个串行顺序。
// 引擎只追加事件记录；逾期自动认定通过 scheduler 惰性物化到账，
// 任何查询结果都只是“已接受操作序列 + now”的函数。
type Engine struct {
	mu  sync.Mutex
	cfg Config

	lastNow      int
	clockStarted bool

	txns  map[string]*Transaction
	cases map[string]*Case

	caseIDsByTxnReason map[txnReasonKey][]string
	caseIDsByBasis     map[string][]string
	txnIDsByProfile    map[profileKey][]string

	merchantBal map[string]int64
	issuerBal   int64
	pendingHeld int64
	feesTotal   int64
	heldByTxn   map[string]int64

	sched *scheduler
}

// opView 是一次操作/查询在工作时看到的账目视图：已物化账目 + 到期事件差额。
type opView struct {
	engine *Engine
	delta  *ledgerDelta
	due    []scheduledEvent
}

// begin 校验时钟并收集到期事件。返回 nil 错误时必须配对调用 commit 或 reject。
func (e *Engine) begin(now int) (*opView, *Error) {
	if e.clockStarted && now < e.lastNow {
		return nil, newError(ErrClockRegression, "now=%d 小于已接受的最新时刻 %d", now, e.lastNow)
	}
	due := e.sched.collectDue(now)
	d := newLedgerDelta()
	for _, ev := range due {
		if c, ok := e.cases[ev.caseID]; ok {
			d.absorb(ev, c)
		}
	}
	return &opView{engine: e, delta: d, due: due}, nil
}

// commit 提交差额、物化到期事件并推进时钟。
// 被接受的操作与查询共用此路径：查询不产生业务事件，
// 但同样推进物化水位，保证缓存与单调时钟一致。
func (v *opView) commit(now int) {
	v.applyDelta()
	for _, c := range v.delta.settledCases {
		c.MoneySettled = true
	}
	e := v.engine
	e.lastNow = now
	e.clockStarted = true
}

func (v *opView) applyDelta() {
	e := v.engine
	for m, amt := range v.delta.merchant {
		e.merchantBal[m] += amt
	}
	e.issuerBal += v.delta.issuer
	e.pendingHeld += v.delta.pending
	for t, amt := range v.delta.held {
		e.heldByTxn[t] += amt
	}
}

// reject 回滚到期事件，保证被拒绝的操作不留任何痕迹。
func (v *opView) reject() {
	v.engine.sched.rollback(v.due)
}

func (v *opView) merchantBalance(id string) int64 {
	return v.engine.merchantBal[id] + v.delta.merchant[id]
}

func (v *opView) heldOf(txnID string) int64 {
	return v.engine.heldByTxn[txnID] + v.delta.held[txnID]
}

// New 创建引擎。配置在创建后不可变（可验证确定性的前提）。
func New(cfg Config) *Engine {
	return &Engine{
		cfg:                cfg,
		txns:               make(map[string]*Transaction),
		cases:              make(map[string]*Case),
		caseIDsByTxnReason: make(map[txnReasonKey][]string),
		caseIDsByBasis:     make(map[string][]string),
		txnIDsByProfile:    make(map[profileKey][]string),
		merchantBal:        make(map[string]int64),
		heldByTxn:          make(map[string]int64),
		sched:              newScheduler(),
	}
}

// ledgerDelta 收集到期资金事件引起的账目差额。
// 操作被接受时提交，被拒绝时丢弃（事件回滚回堆）。
type ledgerDelta struct {
	merchant     map[string]int64
	issuer       int64
	pending      int64
	held         map[string]int64
	settledCases []*Case
}

func newLedgerDelta() *ledgerDelta {
	// 映射按需惰性分配：无到期事件时查询零分配。
	return &ledgerDelta{}
}

// absorb 把一笔到期事件折算进差额；事件条件不满足时忽略（惰性删除）。
func (d *ledgerDelta) absorb(ev scheduledEvent, c *Case) {
	switch ev.kind {
	case eventForfeit:
		if c.RespondDay != noDay || c.MoneySettled {
			return
		}
		d.issuer += c.Amount
		d.pending -= c.Amount
		d.settledCases = append(d.settledCases, c)
	case eventAutoAccept:
		if c.MoneySettled || c.PreArbDay != noDay {
			return
		}
		if d.merchant == nil {
			d.merchant = make(map[string]int64)
			d.held = make(map[string]int64)
		}
		d.merchant[c.MerchantID] += c.Amount
		d.pending -= c.Amount
		d.held[c.TxnID] -= c.Amount
		d.settledCases = append(d.settledCases, c)
	}
}
