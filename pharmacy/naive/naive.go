// Package naive 是处方调剂系统的独立朴素模型，仅用于差分测试。
//
// 与正式实现刻意采用不同策略：
//   - 事件推进：每次全量扫描所有预留与处方，取 (时刻, 序号) 最小者逐个处理；
//   - 欠药分配：每次全量收集该药欠药行并排序，而非维护队列；
//   - 拒绝原子性：每次操作前整体深拷贝，出错即还原，而非回滚日志。
//
// 因此它的时间复杂度差但逻辑直白，可作为参照物验证正式实现。
package naive

import (
	"fmt"
	"sort"

	"ontology/pharmacy"
)

const validity = 72 * 60

type drug struct {
	box        int
	splittable bool
	onHand     int
	reserved   int
}

type line struct {
	drugID    string
	demand    int
	reserved  int
	dispensed int
	backorder int
}

type rx struct {
	id         string
	patient    string
	issue      int
	whole      bool
	status     pharmacy.RxStatus
	lines      []*line
	acceptTime int
	expirySeq  int // 处方过期事件的创建序号，亦代表受理先后
}

type reservation struct {
	rxID    string
	lineIdx int
	qty     int
	expiry  int
	seq     int
	active  bool
}

// Engine 是朴素模型引擎。
type Engine struct {
	r     int
	clock int
	drugs map[string]*drug
	rxs   map[string]*rx
	res   []*reservation
	seq   int
}

// New 创建朴素模型。
func New(r int) *Engine {
	return &Engine{
		r:     r,
		drugs: make(map[string]*drug),
		rxs:   make(map[string]*rx),
	}
}

// clone 深拷贝全部状态，用于“被拒绝的操作不改变任何状态”。
func (e *Engine) clone() *Engine {
	c := &Engine{
		r:     e.r,
		clock: e.clock,
		seq:   e.seq,
		drugs: make(map[string]*drug, len(e.drugs)),
		rxs:   make(map[string]*rx, len(e.rxs)),
		res:   make([]*reservation, len(e.res)),
	}
	for k, d := range e.drugs {
		dd := *d
		c.drugs[k] = &dd
	}
	for k, p := range e.rxs {
		np := &rx{
			id: p.id, patient: p.patient, issue: p.issue, whole: p.whole,
			status: p.status, acceptTime: p.acceptTime, expirySeq: p.expirySeq,
		}
		for _, l := range p.lines {
			nl := *l
			np.lines = append(np.lines, &nl)
		}
		c.rxs[k] = np
	}
	for i, r := range e.res {
		nr := *r
		c.res[i] = &nr
	}
	return c
}

// run 操作骨架：时钟检查 → 快照 → 事件推进 → 执行 → 出错还原 / 成功走钟。
func (e *Engine) run(now int, fn func() error) error {
	if now < e.clock {
		return pharmacy.NewError(pharmacy.ErrClockRollback, fmt.Sprintf("now=%d 小于当前时钟 %d", now, e.clock))
	}
	snap := e.clone()
	e.advance(now)
	if err := fn(); err != nil {
		*e = *snap
		return err
	}
	e.clock = now
	return nil
}

// advance 全量扫描找出 (时刻, 序号) 最小的待处理事件，逐个处理直到没有
// 不晚于 now 的事件；再分配产生的新事件由循环自然接续（连锁）。
func (e *Engine) advance(now int) {
	for {
		found := false
		bestTime, bestSeq := 0, 0
		var bestRes *reservation
		var bestRx *rx
		consider := func(t, s int) bool {
			if t > now {
				return false
			}
			if !found || t < bestTime || (t == bestTime && s < bestSeq) {
				found = true
				bestTime, bestSeq = t, s
				return true
			}
			return false
		}
		for _, r := range e.res {
			if r.active && consider(r.expiry, r.seq) {
				bestRes, bestRx = r, nil
			}
		}
		for _, p := range e.rxs {
			if p.status == pharmacy.RxActive && consider(p.issue+validity+1, p.expirySeq) {
				bestRes, bestRx = nil, p
			}
		}
		if !found {
			return
		}
		if bestRes != nil {
			bestRes.active = false
			p := e.rxs[bestRes.rxID]
			l := p.lines[bestRes.lineIdx]
			l.reserved -= bestRes.qty
			e.drugs[l.drugID].reserved -= bestRes.qty
			e.realloc(l.drugID, bestTime)
		} else {
			e.terminate(bestRx, pharmacy.RxExpired, bestTime)
		}
	}
}

// realloc 全量收集该药欠药行，按 (受理时刻, 受理先后, 行号) 排序后依次分配。
func (e *Engine) realloc(drugID string, t int) {
	d := e.drugs[drugID]
	type cand struct {
		p   *rx
		l   *line
		idx int
	}
	var cs []cand
	for _, p := range e.rxs {
		if p.status != pharmacy.RxActive {
			continue
		}
		for i, l := range p.lines {
			if l.drugID == drugID && l.backorder > 0 {
				cs = append(cs, cand{p, l, i})
			}
		}
	}
	sort.Slice(cs, func(i, j int) bool {
		a, b := cs[i], cs[j]
		if a.p.acceptTime != b.p.acceptTime {
			return a.p.acceptTime < b.p.acceptTime
		}
		if a.p.expirySeq != b.p.expirySeq {
			return a.p.expirySeq < b.p.expirySeq
		}
		return a.idx < b.idx
	})
	for _, c := range cs {
		avail := d.onHand - d.reserved
		b := c.l.backorder
		var r int
		if d.splittable {
			r = b
			if avail < r {
				r = avail
			}
		} else {
			need := (b + d.box - 1) / d.box * d.box
			if avail >= need {
				r = need
			} else {
				r = avail / d.box * d.box
			}
		}
		if r <= 0 {
			return
		}
		e.res = append(e.res, &reservation{rxID: c.p.id, lineIdx: c.idx, qty: r, expiry: t + e.r + 1, seq: e.seq, active: true})
		e.seq++
		c.l.reserved += r
		d.reserved += r
		c.l.backorder -= r
		if c.l.backorder < 0 {
			c.l.backorder = 0 // 向上取整多出部分不算欠药
		}
	}
}

// terminate 过期/取消的共同效果：欠药作废、有效预留失效并分配给其他处方。
func (e *Engine) terminate(p *rx, status pharmacy.RxStatus, t int) {
	p.status = status
	var order []string
	seen := map[string]bool{}
	for _, l := range p.lines {
		l.backorder = 0
		if !seen[l.drugID] {
			seen[l.drugID] = true
			order = append(order, l.drugID)
		}
	}
	released := map[string]bool{}
	for _, r := range e.res {
		if r.active && r.rxID == p.id {
			r.active = false
			l := p.lines[r.lineIdx]
			l.reserved -= r.qty
			e.drugs[l.drugID].reserved -= r.qty
			released[l.drugID] = true
		}
	}
	for _, id := range order {
		if released[id] {
			e.realloc(id, t)
		}
	}
}

func errf(code pharmacy.ErrCode, format string, args ...any) error {
	return pharmacy.NewError(code, fmt.Sprintf(format, args...))
}

// RegisterDrug 登记药品。
func (e *Engine) RegisterDrug(now int, id string, boxSize int, splittable bool) error {
	if now < 0 || now > pharmacy.MaxTime || id == "" || boxSize < 1 || boxSize > pharmacy.MaxQty {
		return errf(pharmacy.ErrInvalidParam, "药品登记参数非法: now=%d id=%q box=%d", now, id, boxSize)
	}
	return e.run(now, func() error {
		if _, ok := e.drugs[id]; ok {
			return errf(pharmacy.ErrStateMismatch, "药品 %q 已登记", id)
		}
		e.drugs[id] = &drug{box: boxSize, splittable: splittable}
		return nil
	})
}

// Inbound 入库。
func (e *Engine) Inbound(now int, drugID string, qty int) error {
	if now < 0 || now > pharmacy.MaxTime || drugID == "" || qty < 1 || qty > pharmacy.MaxQty {
		return errf(pharmacy.ErrInvalidParam, "入库参数非法: now=%d drug=%q qty=%d", now, drugID, qty)
	}
	return e.run(now, func() error {
		d := e.drugs[drugID]
		if d == nil {
			return errf(pharmacy.ErrNotFound, "药品 %q 不存在", drugID)
		}
		d.onHand += qty
		e.realloc(drugID, now)
		return nil
	})
}

func (e *Engine) validateRx(now int, in pharmacy.RxInput) error {
	if now < 0 || now > pharmacy.MaxTime || in.ID == "" || in.Patient == "" {
		return errf(pharmacy.ErrInvalidParam, "处方标识非法: id=%q patient=%q", in.ID, in.Patient)
	}
	if in.IssueTime < 0 || in.IssueTime > pharmacy.MaxTime || in.IssueTime > now {
		return errf(pharmacy.ErrInvalidParam, "开具时刻 %d 非法", in.IssueTime)
	}
	if len(in.Lines) < 1 || len(in.Lines) > pharmacy.MaxLines {
		return errf(pharmacy.ErrInvalidParam, "处方行数 %d 非法", len(in.Lines))
	}
	seen := map[string]bool{}
	for _, l := range in.Lines {
		if l.DrugID == "" || l.Qty < 1 || l.Qty > pharmacy.MaxQty {
			return errf(pharmacy.ErrInvalidParam, "处方行非法: drug=%q qty=%d", l.DrugID, l.Qty)
		}
		if seen[l.DrugID] {
			return errf(pharmacy.ErrInvalidParam, "处方中药品 %q 重复", l.DrugID)
		}
		seen[l.DrugID] = true
	}
	return nil
}

// AcceptPrescription 受理处方。
func (e *Engine) AcceptPrescription(now int, in pharmacy.RxInput) error {
	if err := e.validateRx(now, in); err != nil {
		return err
	}
	return e.run(now, func() error {
		for _, l := range in.Lines {
			if e.drugs[l.DrugID] == nil {
				return errf(pharmacy.ErrNotFound, "药品 %q 不存在", l.DrugID)
			}
		}
		if _, ok := e.rxs[in.ID]; ok {
			return errf(pharmacy.ErrStateMismatch, "处方 %q 已受理", in.ID)
		}
		if now > in.IssueTime+validity {
			return errf(pharmacy.ErrPrescriptionExpired, "处方 %q 已过期", in.ID)
		}
		if in.WholeOrder {
			for _, l := range in.Lines {
				d := e.drugs[l.DrugID]
				avail := d.onHand - d.reserved
				var r int
				if d.splittable {
					r = l.Qty
					if avail < r {
						r = avail
					}
				} else {
					need := (l.Qty + d.box - 1) / d.box * d.box
					if avail >= need {
						r = need
					} else {
						r = avail / d.box * d.box
					}
				}
				if r < l.Qty {
					return errf(pharmacy.ErrOutOfStock, "药品 %q 缺药", l.DrugID)
				}
			}
		}
		p := &rx{id: in.ID, patient: in.Patient, issue: in.IssueTime, whole: in.WholeOrder, status: pharmacy.RxActive, acceptTime: now}
		for i, li := range in.Lines {
			d := e.drugs[li.DrugID]
			l := &line{drugID: li.DrugID, demand: li.Qty}
			p.lines = append(p.lines, l)
			avail := d.onHand - d.reserved
			var r int
			if d.splittable {
				r = li.Qty
				if avail < r {
					r = avail
				}
			} else {
				need := (li.Qty + d.box - 1) / d.box * d.box
				if avail >= need {
					r = need
				} else {
					r = avail / d.box * d.box
				}
			}
			if r < 0 {
				r = 0
			}
			if r > 0 {
				e.res = append(e.res, &reservation{rxID: in.ID, lineIdx: i, qty: r, expiry: now + e.r + 1, seq: e.seq, active: true})
				e.seq++
				l.reserved += r
				d.reserved += r
			}
			bo := li.Qty - r
			if bo < 0 {
				bo = 0
			}
			l.backorder = bo
		}
		e.rxs[in.ID] = p
		p.expirySeq = e.seq
		e.seq++
		return nil
	})
}

// Dispense 取药。
func (e *Engine) Dispense(now int, rxID string) error {
	if now < 0 || now > pharmacy.MaxTime || rxID == "" {
		return errf(pharmacy.ErrInvalidParam, "取药参数非法: now=%d rx=%q", now, rxID)
	}
	return e.run(now, func() error {
		p := e.rxs[rxID]
		if p == nil {
			return errf(pharmacy.ErrNotFound, "处方 %q 不存在", rxID)
		}
		total := 0
		for _, r := range e.res {
			if r.active && r.rxID == rxID {
				r.active = false
				l := p.lines[r.lineIdx]
				l.reserved -= r.qty
				l.dispensed += r.qty
				d := e.drugs[l.drugID]
				d.reserved -= r.qty
				d.onHand -= r.qty
				total += r.qty
			}
		}
		if total == 0 {
			return errf(pharmacy.ErrNoValidReservation, "处方 %q 无有效预留", rxID)
		}
		complete := true
		for _, l := range p.lines {
			if l.dispensed < l.demand || l.reserved > 0 {
				complete = false
				break
			}
		}
		if complete {
			p.status = pharmacy.RxCompleted
		}
		return nil
	})
}

// CancelPrescription 取消处方。
func (e *Engine) CancelPrescription(now int, rxID string) error {
	if now < 0 || now > pharmacy.MaxTime || rxID == "" {
		return errf(pharmacy.ErrInvalidParam, "取消参数非法: now=%d rx=%q", now, rxID)
	}
	return e.run(now, func() error {
		p := e.rxs[rxID]
		if p == nil {
			return errf(pharmacy.ErrNotFound, "处方 %q 不存在", rxID)
		}
		if p.status != pharmacy.RxActive {
			return errf(pharmacy.ErrStateMismatch, "处方 %q 状态为 %s，不可取消", rxID, p.status)
		}
		e.terminate(p, pharmacy.RxCancelled, now)
		return nil
	})
}

func lineStatus(p *rx, l *line) pharmacy.LineStatus {
	if l.dispensed >= l.demand && l.reserved == 0 && l.backorder == 0 {
		return pharmacy.LineFulfilled
	}
	if p.status == pharmacy.RxExpired || p.status == pharmacy.RxCancelled {
		return pharmacy.LineVoid
	}
	if l.backorder > 0 {
		if l.reserved > 0 {
			return pharmacy.LinePartial
		}
		return pharmacy.LineBackordered
	}
	if l.reserved > 0 {
		return pharmacy.LineReserved
	}
	return pharmacy.LineBackordered
}

func (e *Engine) rxState(p *rx) pharmacy.RxState {
	out := pharmacy.RxState{ID: p.id, Patient: p.patient, IssueTime: p.issue, WholeOrder: p.whole, Status: p.status}
	for _, l := range p.lines {
		out.Lines = append(out.Lines, pharmacy.LineState{
			DrugID:    l.drugID,
			Demand:    l.demand,
			Reserved:  l.reserved,
			Dispensed: l.dispensed,
			Backorder: l.backorder,
			Status:    lineStatus(p, l),
		})
	}
	return out
}

func (e *Engine) drugState(id string, d *drug) pharmacy.DrugState {
	bo := 0
	for _, p := range e.rxs {
		for _, l := range p.lines {
			if l.drugID == id {
				bo += l.backorder
			}
		}
	}
	return pharmacy.DrugState{
		ID:             id,
		BoxSize:        d.box,
		Splittable:     d.splittable,
		OnHand:         d.onHand,
		Reserved:       d.reserved,
		Available:      d.onHand - d.reserved,
		BackorderTotal: bo,
	}
}

// QueryPrescription 查询处方。
func (e *Engine) QueryPrescription(now int, rxID string) (pharmacy.RxState, error) {
	if now < 0 || now > pharmacy.MaxTime || rxID == "" {
		return pharmacy.RxState{}, errf(pharmacy.ErrInvalidParam, "查询参数非法: now=%d rx=%q", now, rxID)
	}
	var out pharmacy.RxState
	err := e.run(now, func() error {
		p := e.rxs[rxID]
		if p == nil {
			return errf(pharmacy.ErrNotFound, "处方 %q 不存在", rxID)
		}
		out = e.rxState(p)
		return nil
	})
	return out, err
}

// QueryDrug 查询药品。
func (e *Engine) QueryDrug(now int, drugID string) (pharmacy.DrugState, error) {
	if now < 0 || now > pharmacy.MaxTime || drugID == "" {
		return pharmacy.DrugState{}, errf(pharmacy.ErrInvalidParam, "查询参数非法: now=%d drug=%q", now, drugID)
	}
	var out pharmacy.DrugState
	err := e.run(now, func() error {
		d := e.drugs[drugID]
		if d == nil {
			return errf(pharmacy.ErrNotFound, "药品 %q 不存在", drugID)
		}
		out = e.drugState(drugID, d)
		return nil
	})
	return out, err
}

// Snapshot 直接读取当前状态（不推进事件、不走钟），供差分测试全量比对。
func (e *Engine) Snapshot() (map[string]pharmacy.DrugState, map[string]pharmacy.RxState) {
	drugs := make(map[string]pharmacy.DrugState, len(e.drugs))
	for id, d := range e.drugs {
		drugs[id] = e.drugState(id, d)
	}
	rxs := make(map[string]pharmacy.RxState, len(e.rxs))
	for id, p := range e.rxs {
		rxs[id] = e.rxState(p)
	}
	return drugs, rxs
}
