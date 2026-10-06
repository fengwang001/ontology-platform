package settlement

import (
	"sort"
	"sync"
	"time"
)

// Engine 是线程安全的结算引擎。并发语义等价于所有操作按某一顺序串行执行：
// 所有公开方法取同一把互斥锁，所有校验都在锁内完成，拒绝时不落任何状态。
type Engine struct {
	mu            sync.Mutex
	intervalSec   int64
	intervalCapWh func(p Params) int
	epochUnix     int64
	prosumers     map[string]*prosumer
}

type prosumer struct {
	params   []scheduleEntry[Params]
	prices   []scheduleEntry[Prices]
	readings map[int64]Reading // 键为间隔起点 UTC unix 秒
	closed   map[int]bool      // 封账月：月序号 -> true
	last     Month             // 最近一个已封账月（未封过则零值）
	hasLast  bool
	heap     creditHeap // 最后封账月留下的活动额度；试算只克隆它
	results  map[int]MonthResult
}

// NewEngine 建立引擎。intervalSeconds 必须为正数且整除一天（86400 秒），
// epoch 必须为整点秒；读数起点以 epoch 为网格原点对齐。
func NewEngine(intervalSeconds int, epoch Interval) *Engine {
	if intervalSeconds <= 0 || 86400%intervalSeconds != 0 || epoch.UTC().Nanosecond() != 0 {
		return nil
	}
	step := int64(intervalSeconds)
	return &Engine{
		intervalSec: step,
		epochUnix:   utcUnix(epoch),
		prosumers:   map[string]*prosumer{},
	}
}

func (e *Engine) RegisterProsumer(id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if id == "" {
		return errInvalid("RegisterProsumer", "产消者ID为空")
	}
	if _, ok := e.prosumers[id]; ok {
		return errInvalid("RegisterProsumer", "产消者已存在")
	}
	e.prosumers[id] = &prosumer{readings: map[int64]Reading{}, closed: map[int]bool{}, results: map[int]MonthResult{}}
	return nil
}

// SetParams 自 fromMonth 起生效参数，只能指定尚未封账的月份。
func (e *Engine) SetParams(id, fromMonth string, p Params) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	from, err := parseMonth(fromMonth)
	if err != nil {
		return classify(err)
	}
	if p.ContractPowerW < 0 || p.MonthlyCreditableW < 0 || p.CreditValidMonths < 0 {
		return errInvalid("SetParams", "参数为负")
	}
	pr, ok := e.prosumers[id]
	if !ok {
		return errInvalid("SetParams", "未知产消者")
	}
	if pr.hasLast && !from.after(pr.last) {
		return &SettlementError{Kind: KindClosed, Op: "SetParams", Reason: "生效月已封账"}
	}
	pr.params = upsertSchedule(pr.params, from, p)
	return nil
}

// SetPrices 自 fromMonth 起生效电价，只能指定尚未封账的月份。
func (e *Engine) SetPrices(id, fromMonth string, pc Prices) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	from, err := parseMonth(fromMonth)
	if err != nil {
		return classify(err)
	}
	if pc.ImportPricePerWh < 0 || pc.SurplusPricePerWh < 0 {
		return errInvalid("SetPrices", "单价为负")
	}
	pr, ok := e.prosumers[id]
	if !ok {
		return errInvalid("SetPrices", "未知产消者")
	}
	if pr.hasLast && !from.after(pr.last) {
		return &SettlementError{Kind: KindClosed, Op: "SetPrices", Reason: "生效月已封账"}
	}
	pr.prices = upsertSchedule(pr.prices, from, pc)
	return nil
}

// RegisterReading 登记一个计量间隔。同间隔两项都相同为幂等；
// 两项不同且未封账视为修正（整体覆盖）；已封账报「月份已封账」。
func (e *Engine) RegisterReading(id string, rd Reading) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	pr, ok := e.prosumers[id]
	if !ok {
		return errInvalid("RegisterReading", "未知产消者")
	}
	if !aligned(rd.Start, e.epochUnix, e.intervalSec) {
		return errInvalid("RegisterReading", "间隔起点未对齐")
	}
	if rd.ImportWh < 0 || rd.ExportWh < 0 {
		return errInvalid("RegisterReading", "电量为负")
	}
	m := monthOf(rd.Start)
	if pr.closed[m.index()] {
		old, exists := pr.readings[utcUnix(rd.Start)]
		if exists && old.ImportWh == rd.ImportWh && old.ExportWh == rd.ExportWh {
			return nil // 幂等：已封账的重复登记不视为修改，直接成功
		}
		return &SettlementError{Kind: KindClosed, Op: "RegisterReading", Reason: "月份已封账"}
	}
	pr.readings[utcUnix(rd.Start)] = rd
	return nil
}

// CloseMonth 按规则校验并封账，成功后月结果与额度余额冻结。
func (e *Engine) CloseMonth(id, month string) (*MonthResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	m, err := parseMonth(month)
	if err != nil {
		return nil, classify(err)
	}
	pr, ok := e.prosumers[id]
	if !ok {
		return nil, errInvalid("CloseMonth", "未知产消者")
	}
	// 拒绝次序：非法 > 已封账 > 顺序错误 > 数据缺失。
	if pr.closed[m.index()] {
		return nil, &SettlementError{Kind: KindClosed, Op: "CloseMonth", Reason: "月份已封账"}
	}
	if pr.hasLast && !m.equal(pr.last.add(1)) {
		return nil, &SettlementError{Kind: KindOrder, Op: "CloseMonth", Reason: "必须按月份顺序封账"}
	}
	readings, missing := e.collectReadings(pr, m)
	if !missing.IsZero() {
		return nil, &SettlementError{Kind: KindMissing, Op: "CloseMonth", Reason: "该月存在未登记的计量间隔", Missing: missing}
	}
	p, pc, err := pr.effective(m)
	if err != nil {
		return nil, err
	}
	res := settleMonth(monthInput{
		Month:         m,
		Readings:      readings,
		Params:        p,
		Prices:        pc,
		IntervalCapWh: p.ContractPowerW * int(e.intervalSec) / 3600,
	}, &pr.heap)
	pr.closed[m.index()] = true
	pr.last = m
	pr.hasLast = true
	pr.results[m.index()] = res
	return cloneResult(res), nil
}

// Query 返回某月结果：已封账月返回冻结结果；未封账月从最后封账月的额度快照
// 起按月试算，反映当前读数/参数/单价，但不改变任何持久额度。
func (e *Engine) Query(id, month string) (*MonthResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	m, err := parseMonth(month)
	if err != nil {
		return nil, classify(err)
	}
	pr, ok := e.prosumers[id]
	if !ok {
		return nil, errInvalid("Query", "未知产消者")
	}
	if r, ok := pr.results[m.index()]; ok {
		return cloneResult(r), nil
	}
	heap := pr.heap.clone()
	cursor := pr.last.add(1)
	if !pr.hasLast {
		if len(pr.params) == 0 || len(pr.prices) == 0 {
			return nil, errInvalid("Query", "尚未设置生效参数或单价")
		}
		cursor = pr.params[0].from
		if first := pr.prices[0].from; first.after(cursor) {
			cursor = first
		}
		if cursor.after(m) {
			return nil, errInvalid("Query", "目标月份早于任何生效参数")
		}
	}
	var res MonthResult
	for {
		p, pc, err := pr.effective(cursor)
		if err != nil {
			return nil, err
		}
		readings, _ := e.collectReadings(pr, cursor)
		res = settleMonth(monthInput{
			Month:         cursor,
			Readings:      readings,
			Params:        p,
			Prices:        pc,
			IntervalCapWh: p.ContractPowerW * int(e.intervalSec) / 3600,
		}, &heap)
		if cursor.equal(m) {
			break
		}
		cursor = cursor.add(1)
	}
	return cloneResult(res), nil
}

// ActiveCreditWh 返回持久化（最后封账月之后）的活动额度总量，供断言不变量。
func (e *Engine) ActiveCreditWh(id string) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	pr, ok := e.prosumers[id]
	if !ok {
		return 0, errInvalid("ActiveCreditWh", "未知产消者")
	}
	total := 0
	for _, lot := range pr.heap {
		total += lot.RemainingWh
	}
	return total, nil
}

// ClosedResult 取已封账月结果；未封账返回错误。
func (e *Engine) ClosedResult(id, month string) (*MonthResult, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	pr, ok := e.prosumers[id]
	if !ok {
		return nil, false
	}
	m, err := parseMonth(month)
	if err != nil {
		return nil, false
	}
	r, ok := pr.results[m.index()]
	if !ok {
		return nil, false
	}
	return cloneResult(r), true
}

func (p *prosumer) effective(m Month) (Params, Prices, error) {
	pa, okp := valueAt(p.params, m)
	pc, okc := valueAt(p.prices, m)
	if !okp || !okc {
		return Params{}, Prices{}, errInvalid("effective", "该月缺少生效参数或单价")
	}
	return pa, pc, nil
}

// collectReadings 收集某月全部已登记读数（按起点排序）；
// 若该月有任何缺失间隔，missing 返回最早缺失的间隔起点，readings 仍可用于试算。
func (e *Engine) collectReadings(p *prosumer, m Month) ([]Reading, time.Time) {
	start := monthStartUTC(m).Unix()
	next := monthStartUTC(m.add(1)).Unix()
	var readings []Reading
	var missing time.Time
	for t := start; t < next; t += e.intervalSec {
		rd, ok := p.readings[t]
		if !ok {
			if missing.IsZero() {
				missing = time.Unix(t, 0).UTC()
			}
			continue
		}
		readings = append(readings, rd)
	}
	sort.Slice(readings, func(i, j int) bool { return utcUnix(readings[i].Start) < utcUnix(readings[j].Start) })
	return readings, missing
}

func (m Month) after(o Month) bool { return m.index() > o.index() }

func classify(err error) error {
	if se, ok := err.(*SettlementError); ok {
		return se
	}
	return err
}

func cloneResult(r MonthResult) *MonthResult {
	cp := r
	return &cp
}
