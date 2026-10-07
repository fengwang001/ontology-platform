package pharmacy

import "sync"

// Drug 是药品内部状态。
type Drug struct {
	id             string
	box            int
	splittable     bool
	onHand         int // 在库量（含被预留部分）
	reserved       int // 当前有效预留量
	backorderTotal int // 欠药总量（O(1) 查询）
	totalInbound   int // 累计入库量
	bq             boQueue
}

// Line 是处方一行。
type Line struct {
	rx           *Prescription
	drug         *Drug
	idx          int
	demand       int
	reserved     int
	dispensed    int
	backorder    int
	reservations []*Reservation
	bo           *boNode // 非 nil 表示挂在药品欠药队列中
}

// Prescription 是处方内部状态。
type Prescription struct {
	id      string
	patient string
	issue   int
	whole   bool
	status  RxStatus
	lines   []*Line
}

// Reservation 是一次预留。失效时刻为 start+R+1。
type Reservation struct {
	line   *Line
	qty    int
	start  int
	expiry int
	active bool
}

// Stats 是内部开销计数，用于以可验证方式证明再分配复杂度。
type Stats struct {
	EventsProcessed       uint64 // 已处理事件数
	ReservationsReleased  uint64 // 已释放预留数
	BackorderScanSteps    uint64 // 欠药队列扫描步数
	ReservationsAllocated uint64 // 再分配产生的新预留数
}

// Engine 是库存预留与欠药补发引擎。
// 所有公开方法持有同一把互斥锁，并发调用等价于某串行顺序。
type Engine struct {
	mu    sync.Mutex
	r     int
	clock int // 上一次被接受操作的 now

	drugs map[string]*Drug
	rxs   map[string]*Prescription

	heap eventHeap
	seq  int // 事件序号，保证同时刻事件顺序确定

	journal    []func() // 当前操作的回滚日志
	journaling bool

	stats Stats
}

// NewEngine 创建引擎。R 非法时返回参数非法错误。
func NewEngine(cfg Config) (*Engine, error) {
	if cfg.R < 0 || cfg.R > MaxTime {
		return nil, newError(ErrInvalidParam, "取药窗口 R=%d 超出 [0,%d]", cfg.R, MaxTime)
	}
	return &Engine{
		r:     cfg.R,
		drugs: make(map[string]*Drug),
		rxs:   make(map[string]*Prescription),
	}, nil
}

// Stats 返回内部开销计数快照。
func (e *Engine) Stats() Stats {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.stats
}

// ResetStats 清零内部开销计数。
func (e *Engine) ResetStats() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.stats = Stats{}
}

// set 赋值并记录回滚。
func set[T any](e *Engine, p *T, v T) {
	if e.journaling {
		old := *p
		e.journal = append(e.journal, func() { *p = old })
	}
	*p = v
}

// run 是操作骨架：时钟检查 → 事件推进 → 执行 → 失败回滚 / 成功提交并走钟。
// 参数校验在调用 run 之前完成，以保证错误优先级（参数非法 > 时钟回退）。
func (e *Engine) run(now int, fn func() error) error {
	if now < e.clock {
		return newError(ErrClockRollback, "now=%d 小于当前时钟 %d", now, e.clock)
	}
	e.journal = e.journal[:0]
	e.journaling = true
	e.advance(now)
	err := fn()
	e.journaling = false
	if err != nil {
		for i := len(e.journal) - 1; i >= 0; i-- {
			e.journal[i]()
		}
		e.journal = e.journal[:0]
		return err
	}
	e.journal = e.journal[:0]
	e.clock = now
	return nil
}

// advance 按 (时刻, 序号) 顺序处理所有不晚于 now 的事件；再分配可能产生
// 新的、仍不晚于 now 的事件（连锁），由循环自然接续处理。
func (e *Engine) advance(now int) {
	for {
		ev := e.heap.top()
		if ev == nil || ev.time > now {
			return
		}
		if e.journaling {
			e.journal = append(e.journal, func() { e.heap.push(ev) })
		}
		e.heap.pop()
		e.stats.EventsProcessed++
		switch ev.kind {
		case evResExpiry:
			e.onReservationExpiry(ev)
		case evRxExpiry:
			e.onPrescriptionExpiry(ev)
		}
	}
}

func (e *Engine) pushEvent(t int, kind eventKind, res *Reservation, rx *Prescription) {
	ev := &event{time: t, kind: kind, res: res, rx: rx}
	ev.seq = e.seq
	set(e, &e.seq, e.seq+1)
	if e.journaling {
		e.journal = append(e.journal, func() { e.heap.remove(ev) })
	}
	e.heap.push(ev)
}

// createReservation 建立预留并登记其失效事件。
func (e *Engine) createReservation(l *Line, qty, start int) {
	res := &Reservation{line: l, qty: qty, start: start, expiry: start + e.r + 1, active: true}
	oldLen := len(l.reservations)
	if e.journaling {
		e.journal = append(e.journal, func() { l.reservations = l.reservations[:oldLen] })
	}
	l.reservations = append(l.reservations, res)
	set(e, &l.reserved, l.reserved+qty)
	set(e, &l.drug.reserved, l.drug.reserved+qty)
	e.pushEvent(res.expiry, evResExpiry, res, nil)
}

// releaseReservation 使预留失效，库存回到可用量。
func (e *Engine) releaseReservation(res *Reservation) {
	set(e, &res.active, false)
	l := res.line
	set(e, &l.reserved, l.reserved-res.qty)
	set(e, &l.drug.reserved, l.drug.reserved-res.qty)
	e.stats.ReservationsReleased++
}

func (e *Engine) onReservationExpiry(ev *event) {
	res := ev.res
	if !res.active {
		return // 已被取药或随处方失效，惰性跳过
	}
	e.releaseReservation(res)
	e.allocate(res.line.drug, ev.time)
}

func (e *Engine) onPrescriptionExpiry(ev *event) {
	p := ev.rx
	if p.status != RxActive {
		return
	}
	e.terminate(p, RxExpired, ev.time)
}

// terminate 实现处方过期与取消的共同效果：欠药行作废、有效预留立即失效
// 并回到可用量，再按欠药登记次序分配给其他处方。
func (e *Engine) terminate(p *Prescription, status RxStatus, t int) {
	set(e, &p.status, status)
	var affected []*Drug
	for _, l := range p.lines {
		d := l.drug
		if l.backorder > 0 {
			set(e, &d.backorderTotal, d.backorderTotal-l.backorder)
			set(e, &l.backorder, 0)
			if l.bo != nil {
				e.boRemove(l.bo)
				set(e, &l.bo, nil)
			}
		}
		released := false
		for _, res := range l.reservations {
			if res.active {
				e.releaseReservation(res)
				released = true
			}
		}
		if released {
			affected = append(affected, d)
		}
	}
	for _, d := range affected {
		e.allocate(d, t)
	}
}

// reserveAmount 是受理与再分配共用的取整规则：
// 可拆零取 min(d, 可用量)；不可拆零在可用量足够时向上取整到整盒倍数，
// 否则向下取整到整盒倍数（可为零）。
func reserveAmount(box int, splittable bool, d, avail int) int {
	if avail <= 0 {
		return 0
	}
	if splittable {
		if avail < d {
			return avail
		}
		return d
	}
	need := (d + box - 1) / box * box
	if avail >= need {
		return need
	}
	return avail / box * box
}

// allocate 在时刻 t 把药品 d 的可用量按欠药登记次序分配给欠药行。
// 队列中只存在未作废、未满足的欠药行（作废/满足时即摘除），
// 因此开销只与当前欠药行数相关。
func (e *Engine) allocate(d *Drug, t int) {
	for {
		n := d.bq.front()
		if n == nil {
			return
		}
		e.stats.BackorderScanSteps++
		l := n.line
		if l.backorder <= 0 || l.rx.status != RxActive {
			// 防御性清理：正常流程下作废行已被即时摘除。
			e.boRemove(n)
			set(e, &l.bo, nil)
			continue
		}
		avail := d.onHand - d.reserved
		r := reserveAmount(d.box, d.splittable, l.backorder, avail)
		if r <= 0 {
			return // 可用量不足整盒（或为零），后续行同样分不到
		}
		e.createReservation(l, r, t)
		e.stats.ReservationsAllocated++
		newBo := l.backorder - r
		if newBo < 0 {
			newBo = 0 // 向上取整多出部分不算欠药
		}
		set(e, &d.backorderTotal, d.backorderTotal-(l.backorder-newBo))
		set(e, &l.backorder, newBo)
		if newBo == 0 {
			e.boRemove(n)
			set(e, &l.bo, nil)
		}
	}
}

func (e *Engine) boPush(d *Drug, n *boNode) {
	if e.journaling {
		e.journal = append(e.journal, func() { d.bq.unlink(n) })
	}
	d.bq.pushBack(n)
}

func (e *Engine) boRemove(n *boNode) {
	if e.journaling {
		d := n.line.drug
		e.journal = append(e.journal, func() { d.bq.relink(n) })
	}
	n.line.drug.bq.unlink(n)
}

func validTime(t int) bool { return t >= 0 && t <= MaxTime }

func validID(s string) bool { return s != "" }

func validQty(q int) bool { return q >= 1 && q <= MaxQty }
