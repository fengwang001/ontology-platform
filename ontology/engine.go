package ontology

import (
	"sync"
	"time"
)

// 光伏余电上网月度结算引擎（包文档）。
//
// 核心流程：双向电表按固定计量间隔登记下网/上网电量；封账时先做单间隔功率超限剔除，
// 再施加月度可计入上限；可计入上网先逐度抵扣当月下网，净上网存入带到期月的抵扣额度，
// 净下网按「到期月升序、同到期月存入月升序」使用历史额度，仍不足按下网电价计费；
// 到期月不晚于封账月的剩余额度在当月抵扣之后按余电单价付款清零。
//
// 入口类型为 Engine；错误统一为 *SettError，可按 ErrorKind 区分四类拒绝
// （参数非法 > 月份已封账 > 顺序错误 > 数据缺失）。月份用 MonthKey（YYYY-MM）表示，
// 可用 ParseMonth 解析。所有电量、金额均为 int64 整数。
//
// NaiveEngine 是与正式实现完全独立的逐月线性重放模型，仅用于差分测试。

// Engine 是月度结算引擎。所有方法可并发调用。
type Engine struct {
	mu sync.RWMutex
	g  *TimeGrid

	consumers map[string]*consumerState
}

type consumerState struct {
	mu        sync.Mutex
	params    paramHistory[ContractParams]
	prices    paramHistory[Prices]
	readings  map[MonthKey]map[time.Time]Readings
	results   map[MonthKey]MonthlyResult
	credits   creditStore
	sealedTo  MonthKey // 已封账的最大连续月份
	hasSealed bool     // 是否封过账（首月可任意，之后必须逐月连续）
}

// NewEngine 创建引擎，interval 为全系统统一计量间隔。
func NewEngine(interval time.Duration) (*Engine, error) {
	g, err := NewTimeGrid(interval)
	if err != nil {
		return nil, err
	}
	return &Engine{g: g, consumers: map[string]*consumerState{}}, nil
}

// RegisterConsumer 登记产消者，并给定初始（月份 0 起）参数与单价。
func (e *Engine) RegisterConsumer(id string, p ContractParams, pr Prices) error {
	if err := validateContract(p); err != nil {
		return err
	}
	if err := validatePrices(pr); err != nil {
		return err
	}
	if id == "" {
		return illegal("consumer id must not be empty")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.consumers[id]; ok {
		return illegal("consumer already registered: %s", id)
	}
	c := &consumerState{
		readings: map[MonthKey]map[time.Time]Readings{},
		results:  map[MonthKey]MonthlyResult{},
	}
	c.params.init(p)
	c.prices.init(pr)
	e.consumers[id] = c
	return nil
}

// SetParams 自 from 月起更改合同参数，from 必须晚于已封账月份。
func (e *Engine) SetParams(id string, from MonthKey, p ContractParams) error {
	if err := validateContract(p); err != nil {
		return err
	}
	c, err := e.get(id)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.hasSealed && from <= c.sealedTo {
		return sealed("parameter change at %s", from)
	}
	if from <= c.params.lastFrom() {
		return orderErr("parameter changes must be appended by effective month")
	}
	c.params.set(from, p)
	return nil
}

// SetPrices 自 from 月起更改单价，from 必须晚于已封账月份。
func (e *Engine) SetPrices(id string, from MonthKey, pr Prices) error {
	if err := validatePrices(pr); err != nil {
		return err
	}
	c, err := e.get(id)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.hasSealed && from <= c.sealedTo {
		return sealed("price change at %s", from)
	}
	if from <= c.prices.lastFrom() {
		return orderErr("price changes must be appended by effective month")
	}
	c.prices.set(from, pr)
	return nil
}

// PutReading 登记或修正一个计量间隔的读数。
func (e *Engine) PutReading(id string, start time.Time, r Readings) error {
	if !e.g.AlignedStart(start) {
		return illegal("interval start %s is not grid-aligned", start.UTC().Format(time.RFC3339))
	}
	if r.ImportKWh < 0 || r.ExportKWh < 0 {
		return illegal("readings must be non-negative")
	}
	c, err := e.get(id)
	if err != nil {
		return err
	}
	start = start.UTC()
	m := MonthOf(start)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.hasSealed && m <= c.sealedTo {
		return sealed("reading at %s", start.Format(time.RFC3339))
	}
	monthMap := c.readings[m]
	if monthMap == nil {
		monthMap = map[time.Time]Readings{}
		c.readings[m] = monthMap
	}
	monthMap[start] = r // 同值写入幂等；不同值即修正（Go map 赋值天然覆盖）
	return nil
}

// SealMonth 按月份顺序封账。
func (e *Engine) SealMonth(id string, m MonthKey) error {
	c, err := e.get(id)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.hasSealed && m <= c.sealedTo {
		return sealed("seal %s", m)
	}
	if c.hasSealed && m != c.sealedTo+1 {
		return orderErr("expected seal %s before %s", c.sealedTo+1, m)
	}
	monthMap := c.readings[m]
	for _, t := range e.g.IntervalStarts(m) {
		if _, ok := monthMap[t]; !ok {
			return missing(t) // 数据缺失，且报最早缺失间隔
		}
	}
	p := c.params.at(m)
	pr := c.prices.at(m)
	// 当月抵扣：到期月恰等于 m 的额度也可在本月使用（之后才到期），故 minExpiry = m。
	res := computeMonth(m, monthMap, e.g, p, pr, func(want int64) int64 {
		return c.credits.use(m, want)
	})
	// 净上网存入额度，到期月 = 当月 + 有效月数；额度存入当月不可用（后续月份才满足 minExpiry >= 当月+1）。
	c.credits.deposit(m, m.Add(p.CreditValidMonths), res.NetExportCredited)
	// 到期月不晚于 m 的剩余额度在抵扣之后到期，按当月余电单价付款并清零。
	res.ExpiredCreditKWh = c.credits.expire(m)
	res.ExportPayment += res.ExpiredCreditKWh * pr.ExportPrice
	res.Sealed = true
	res.LiveCreditBalance = c.credits.balance()
	c.results[m] = res
	c.sealedTo = m
	c.hasSealed = true
	return nil
}

// MonthResult 查询月结果（未封账月反映当前数据且不改变额度余额）。
func (e *Engine) MonthResult(id string, m MonthKey) (MonthlyResult, error) {
	c, err := e.get(id)
	if err != nil {
		return MonthlyResult{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if res, ok := c.results[m]; ok {
		return res, nil // 已封账：不可变结果
	}
	p := c.params.at(m)
	pr := c.prices.at(m)
	// 预览使用克隆额度存储，保证额度余额与封账状态不被改变。
	clone := c.credits.snapshot()
	res := computeMonth(m, c.readings[m], e.g, p, pr, func(want int64) int64 {
		return clone.use(m, want)
	})
	// 预览不存入净上网额度、不执行到期付款；LiveCreditBalance 反映真实已封账余额。
	res.LiveCreditBalance = c.credits.balance()
	return res, nil
}

// CreditBalance 查询某产消者当前未到期额度余额（度）。
func (e *Engine) CreditBalance(id string) (int64, error) {
	c, err := e.get(id)
	if err != nil {
		return 0, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.credits.balance(), nil
}

func (e *Engine) get(id string) (*consumerState, error) {
	e.mu.RLock()
	c, ok := e.consumers[id]
	e.mu.RUnlock()
	if !ok {
		return nil, illegal("unknown consumer: %s", id)
	}
	return c, nil
}
