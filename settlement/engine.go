package settlement

import (
	"math"
	"sync"
)

// Engine 结算引擎：管理商户、时钟与全部写操作。
// 所有方法可并发调用，效果等价于某个串行顺序（由互斥锁保证）。
// 被拒绝的操作不改变任何状态与时钟。
type Engine struct {
	mu        sync.Mutex
	cal       Calendar
	lastNow   int64
	merchants map[string]*merchant
}

// NewEngine 以给定营业日日历创建引擎。
func NewEngine(cal Calendar) *Engine {
	return &Engine{cal: cal, lastNow: math.MinInt64, merchants: make(map[string]*merchant)}
}

// AddMerchant 注册商户。所有操作均携带 now。
// 校验顺序：参数非法 > 时钟回退 > 商户已存在。
func (e *Engine) AddMerchant(now int64, id string, cfg Config) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if id == "" || !cfg.valid() {
		return newError(CodeInvalidParam, "invalid merchant id or config %+v", cfg)
	}
	if now < e.lastNow {
		return newError(CodeClockRollback, "now %d < last now %d", now, e.lastNow)
	}
	if _, ok := e.merchants[id]; ok {
		return newError(CodeMerchantExists, "merchant %q already exists", id)
	}
	e.merchants[id] = newMerchant(cfg)
	e.lastNow = now
	return nil
}

// PostTransaction 录入一笔流水（支付为正，退款/拒付/手续费为负）。
// 校验顺序：参数非法 > 时钟回退 > 商户不存在 > 流水编号重复 > 日期非法 > 已封账。
func (e *Engine) PostTransaction(now int64, merchantID, txID string, day int64, amount int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if merchantID == "" || txID == "" {
		return newError(CodeInvalidParam, "empty merchant id or tx id")
	}
	if now < e.lastNow {
		return newError(CodeClockRollback, "now %d < last now %d", now, e.lastNow)
	}
	m, ok := e.merchants[merchantID]
	if !ok {
		return newError(CodeMerchantNotFound, "merchant %q not found", merchantID)
	}
	if err := m.postTx(now, txID, day, amount); err != nil {
		return err
	}
	e.lastNow = now
	return nil
}

// Settle 对商户结算截至营业日 d（含），返回其间每个营业日的出款记录。
// 校验顺序：参数非法 > 时钟回退 > 商户不存在 > 非营业日 > 重复结算。
func (e *Engine) Settle(now int64, merchantID string, d int64) ([]PayoutRecord, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if merchantID == "" {
		return nil, newError(CodeInvalidParam, "empty merchant id")
	}
	if now < e.lastNow {
		return nil, newError(CodeClockRollback, "now %d < last now %d", now, e.lastNow)
	}
	m, ok := e.merchants[merchantID]
	if !ok {
		return nil, newError(CodeMerchantNotFound, "merchant %q not found", merchantID)
	}
	if !e.cal.IsBusinessDay(d) {
		return nil, newError(CodeNonBusinessDay, "day %d is not a business day", d)
	}
	if d <= m.lastSettled {
		return nil, newError(CodeDuplicateSettlement, "day %d <= last settled day %d", d, m.lastSettled)
	}
	days := e.cal.DaysBetween(m.lastSettled, d)
	records := make([]PayoutRecord, 0, len(days))
	for _, t := range days {
		rec := m.settleDay(e.cal, t)
		rec.MerchantID = merchantID
		records = append(records, rec)
	}
	e.lastNow = now
	return records, nil
}

// Snapshot 返回商户当前状态快照，供对账与测试使用。
func (e *Engine) Snapshot(merchantID string) (Snapshot, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	m, ok := e.merchants[merchantID]
	if !ok {
		return Snapshot{}, newError(CodeMerchantNotFound, "merchant %q not found", merchantID)
	}
	return m.snapshot(merchantID), nil
}
