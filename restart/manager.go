// Package restart 提供隧道标签管理器：编排 labelpool（标签区间、空闲与隔离）
// 与 binding（fec 到标签的绑定与属主），实现客户端平滑重启的陈旧标记与
// 管理器自身重启的日志恢复。
//
// 到期是时间的纯函数：每个被接受的操作开始时，先按（时刻，fec）次序落地
// 截止时刻不大于 now 的陈旧移除与占位移除，由此产生的 FREE 时刻取截止时刻；
// 被拒绝的操作不改任何状态、不落地到期、不写日志、不推进时钟。
package restart

import (
	"errors"
	"sync"

	"ontology/binding"
	"ontology/labelpool"
)

var (
	ErrInvalid       = errors.New("restart: invalid argument")
	ErrClock         = errors.New("restart: clock regression")
	ErrClientOffline = errors.New("restart: client offline")
	ErrState         = errors.New("restart: invalid client state")
	ErrNoBinding     = errors.New("restart: binding not found")
	ErrOwnerLimit    = errors.New("restart: owner limit reached")
	ErrExhausted     = errors.New("restart: label exhausted")
	ErrCorrupt       = errors.New("restart: corrupt log")
)

// LogOp 是日志事件类型（只记标签层事件，不记属主）。
type LogOp int

const (
	OpAlloc LogOp = iota + 1 // ALLOC(fec, 标签, 时刻)
	OpFree                   // FREE(fec, 标签, 时刻)
)

// LogEntry 是一条标签层日志事件。
type LogEntry struct {
	Op    LogOp
	Fec   string
	Label int
	Time  int64
}

type clientState int

const (
	stateNormal clientState = iota
	stateOffline
	stateRecovering
)

const (
	maxClient = 10_000
	maxNow    = int64(1_000_000_000_000)
	maxFecLen = 64
)

// expEvent 是一条待落地的到期事件（陈旧属主或占位属主）。
type expEvent struct {
	deadline int64
	fec      string
	client   uint32
}

func expLess(a, b expEvent) bool {
	if a.deadline != b.deadline {
		return a.deadline < b.deadline
	}
	if a.fec != b.fec {
		return a.fec < b.fec
	}
	return a.client < b.client
}

// expiryHeap 是按 (截止时刻, fec, client) 全序的最小堆；失效事件懒删除。
type expiryHeap struct {
	items []expEvent
}

func (h *expiryHeap) len() int       { return len(h.items) }
func (h *expiryHeap) peek() expEvent { return h.items[0] }

func (h *expiryHeap) push(ev expEvent) {
	h.items = append(h.items, ev)
	i := len(h.items) - 1
	for i > 0 {
		p := (i - 1) / 2
		if !expLess(h.items[i], h.items[p]) {
			break
		}
		h.items[i], h.items[p] = h.items[p], h.items[i]
		i = p
	}
}

func (h *expiryHeap) pop() expEvent {
	top := h.items[0]
	last := len(h.items) - 1
	h.items[0] = h.items[last]
	h.items = h.items[:last]
	for i := 0; ; {
		l, r := 2*i+1, 2*i+2
		m := i
		if l < len(h.items) && expLess(h.items[l], h.items[m]) {
			m = l
		}
		if r < len(h.items) && expLess(h.items[r], h.items[m]) {
			m = r
		}
		if m == i {
			break
		}
		h.items[i], h.items[m] = h.items[m], h.items[i]
		i = m
	}
	return top
}

// Manager 是隧道标签管理器。所有方法可并发调用，结果等价于某个串行顺序。
type Manager struct {
	mu      sync.Mutex
	lo, hi  int
	hd, r   int64
	q       int
	pool    *labelpool.Pool
	tbl     *binding.Table
	exp     expiryHeap
	clients map[uint32]clientState
	log     []LogEntry
	maxNow  int64
}

// New 创建管理器：标签区间 [lo,hi]（16≤lo≤hi≤1048575），隔离期 hd 与
// 重启恢复期 r（0 到 1e9 毫秒），每客户端属主关系上限 q（1 到 1e6）。
func New(lo, hi int, hd, r int64, q int) (*Manager, error) {
	if lo < labelpool.MinLabel || hi > labelpool.MaxLabel || lo > hi {
		return nil, ErrInvalid
	}
	if hd < 0 || hd > labelpool.MaxHold || r < 0 || r > labelpool.MaxHold {
		return nil, ErrInvalid
	}
	if q < 1 || q > binding.MaxQ {
		return nil, ErrInvalid
	}
	pool, err := labelpool.New(lo, hi, hd)
	if err != nil {
		return nil, ErrInvalid
	}
	tbl, err := binding.New(q)
	if err != nil {
		return nil, ErrInvalid
	}
	return &Manager{
		lo: lo, hi: hi, hd: hd, r: r, q: q,
		pool:    pool,
		tbl:     tbl,
		clients: make(map[uint32]clientState),
	}, nil
}

func validFec(fec []byte) bool  { return len(fec) >= 1 && len(fec) <= maxFecLen }
func validClient(c uint32) bool { return c >= 1 && c <= maxClient }
func validNow(now int64) bool   { return now >= 0 && now <= maxNow }

func (m *Manager) clientState(c uint32) clientState { return m.clients[c] }

// validEvent 校验弹出的到期事件是否仍对应一条现存关系（懒删除）。
func (m *Manager) validEvent(ev expEvent) bool {
	ent, ok := m.tbl.Get(ev.fec)
	if !ok {
		return false
	}
	ow, ok := ent.Owners[ev.client]
	return ok && ow.Stale && ow.Deadline == ev.deadline
}

// collectExpiries 弹出全部截止时刻不大于 now 的有效到期事件，
// 按 (时刻, fec, client) 次序返回；被拒绝的操作须用 pushback 原样放回。
func (m *Manager) collectExpiries(now int64) []expEvent {
	var out []expEvent
	for m.exp.len() > 0 {
		ev := m.exp.peek()
		if ev.deadline > now {
			break
		}
		m.exp.pop()
		if !m.validEvent(ev) {
			continue
		}
		out = append(out, ev)
	}
	return out
}

// pushback 把试算过但被拒绝的到期事件放回堆（不落地）。
func (m *Manager) pushback(events []expEvent) {
	for _, ev := range events {
		m.exp.push(ev)
	}
}

// commitExpiries 落地到期事件：移除属主关系，删空的绑定释放标签，
// FREE 时刻取截止时刻。
func (m *Manager) commitExpiries(events []expEvent) {
	for _, ev := range events {
		label, last, _ := m.tbl.RemoveOwner(ev.fec, ev.client)
		if last {
			m.pool.Free(ev.fec, label, ev.deadline)
			m.log = append(m.log, LogEntry{Op: OpFree, Fec: ev.fec, Label: label, Time: ev.deadline})
		}
	}
}

// expiryView 是到期事件的纯函数试算结果，用于在不改状态的前提下做拒绝判定。
type expiryView struct {
	removed   map[string]map[uint32]bool // fec -> 被移除的属主
	remaining map[string]int             // fec -> 落地后剩余属主数
	freedTime map[string]int64           // 被删空的 fec -> 释放时刻（截止时刻）
}

func (m *Manager) buildView(events []expEvent) *expiryView {
	v := &expiryView{
		removed:   make(map[string]map[uint32]bool),
		remaining: make(map[string]int),
		freedTime: make(map[string]int64),
	}
	for _, ev := range events {
		set := v.removed[ev.fec]
		if set == nil {
			set = make(map[uint32]bool)
			v.removed[ev.fec] = set
		}
		set[ev.client] = true
	}
	for fec, set := range v.removed {
		ent, ok := m.tbl.Get(fec)
		if !ok {
			continue
		}
		left := len(ent.Owners) - len(set)
		v.remaining[fec] = left
		if left == 0 {
			var d int64
			for _, ev := range events {
				if ev.fec == fec && ev.deadline > d {
					d = ev.deadline
				}
			}
			v.freedTime[fec] = d
		}
	}
	return v
}

// released 报告 fec 的绑定是否会被本次落地删空。
func (v *expiryView) released(fec string) bool {
	_, ok := v.freedTime[fec]
	return ok
}

// removedOwner 报告 client 在 fec 上的关系是否会被本次落地移除。
func (v *expiryView) removedOwner(fec string, client uint32) bool {
	return v.removed[fec][client]
}

// removedFor 返回 client 会被本次落地移除的关系数。
func (v *expiryView) removedFor(client uint32) int {
	n := 0
	for _, set := range v.removed {
		if set[client] {
			n++
		}
	}
	return n
}

// freedAvailable 报告本次落地释放的标签中是否有 now 时刻可用的
// （释放时刻+Hd ≤ now，恰等即可用）。
func (v *expiryView) freedAvailable(hd, now int64) bool {
	for _, d := range v.freedTime {
		if d+hd <= now {
			return true
		}
	}
	return false
}

// Bind 为 fec 绑定标签并把 client 加入属主集合，返回标签。
// 拒绝次序：参数非法 > 时钟回退 > 客户端离线 >（已是属主或陈旧属主则直接
// 成功）> 属主关系数已达 Q > 标签耗尽。
func (m *Manager) Bind(fec []byte, client uint32, now int64) (int, error) {
	if !validFec(fec) || !validClient(client) || !validNow(now) {
		return 0, ErrInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if now < m.maxNow {
		return 0, ErrClock
	}
	if m.clientState(client) == stateOffline {
		return 0, ErrClientOffline
	}
	f := string(fec)
	events := m.collectExpiries(now)
	view := m.buildView(events)

	ent, bound := m.tbl.Get(f)
	if bound && view.released(f) {
		bound = false
		ent = nil
	}
	shortcut := false
	if bound {
		if _, ok := ent.Owners[client]; ok && !view.removedOwner(f, client) {
			shortcut = true // 已是属主或陈旧属主，直接成功
		}
	}
	if !shortcut {
		if m.tbl.Count(client)-view.removedFor(client) >= m.q {
			m.pushback(events)
			return 0, ErrOwnerLimit
		}
		if !bound {
			avail := m.pool.CanAlloc(f, now) || view.released(f) || view.freedAvailable(m.hd, now)
			if !avail {
				m.pushback(events)
				return 0, ErrExhausted
			}
		}
	}
	m.commitExpiries(events)
	m.maxNow = now

	ent, bound = m.tbl.Get(f)
	if bound {
		if ow, ok := ent.Owners[client]; ok {
			if ow.Stale {
				m.tbl.SetNormal(f, client) // 陈旧属主转为正常
			}
			return ent.Label, nil
		}
		if _, ph := ent.Owners[binding.Placeholder]; ph {
			delete(ent.Owners, binding.Placeholder) // 认领：移除占位属主
		}
		m.tbl.AddOwner(f, client, binding.Owner{})
		return ent.Label, nil
	}
	label, ok := m.pool.Alloc(f, now)
	if !ok { // 视图已保证可取到，此处不可达
		return 0, ErrExhausted
	}
	m.tbl.Create(f, label)
	m.tbl.AddOwner(f, client, binding.Owner{})
	m.log = append(m.log, LogEntry{Op: OpAlloc, Fec: f, Label: label, Time: now})
	return label, nil
}

// Unbind 移除 client 在 fec 上的属主关系；最后一个属主离开时标签释放，
// FREE 时刻取 now。
func (m *Manager) Unbind(fec []byte, client uint32, now int64) error {
	if !validFec(fec) || !validClient(client) || !validNow(now) {
		return ErrInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if now < m.maxNow {
		return ErrClock
	}
	if m.clientState(client) == stateOffline {
		return ErrClientOffline
	}
	f := string(fec)
	events := m.collectExpiries(now)
	view := m.buildView(events)

	ent, bound := m.tbl.Get(f)
	owner := false
	if bound && !view.released(f) {
		if _, ok := ent.Owners[client]; ok && !view.removedOwner(f, client) {
			owner = true
		}
	}
	if !owner {
		m.pushback(events)
		return ErrNoBinding
	}
	m.commitExpiries(events)
	m.maxNow = now

	label, last, _ := m.tbl.RemoveOwner(f, client)
	if last {
		m.pool.Free(f, label, now)
		m.log = append(m.log, LogEntry{Op: OpFree, Fec: f, Label: label, Time: now})
	}
	return nil
}

// ClientDown 把 client 当前全部正常属主关系标为陈旧（截止 now+R，
// 此前已陈旧的保持原截止时刻），客户端转入离线。
func (m *Manager) ClientDown(client uint32, now int64) error {
	if !validClient(client) || !validNow(now) {
		return ErrInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if now < m.maxNow {
		return ErrClock
	}
	if m.clientState(client) == stateOffline {
		return ErrState
	}
	events := m.collectExpiries(now)
	m.commitExpiries(events)
	m.maxNow = now

	deadline := now + m.r
	for _, f := range m.tbl.FecsOf(client) {
		if m.tbl.MarkStale(f, client, deadline) {
			m.exp.push(expEvent{deadline: deadline, fec: f, client: client})
		}
	}
	m.clients[client] = stateOffline
	return nil
}

// ClientUp 使离线客户端转入恢复中；其后 Bind 把陈旧关系转为正常。
func (m *Manager) ClientUp(client uint32, now int64) error {
	if !validClient(client) || !validNow(now) {
		return ErrInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if now < m.maxNow {
		return ErrClock
	}
	if m.clientState(client) != stateOffline {
		return ErrState
	}
	events := m.collectExpiries(now)
	m.commitExpiries(events)
	m.maxNow = now

	m.clients[client] = stateRecovering
	return nil
}

// EndOfRib 把 client 仍陈旧的属主关系全部移除（FREE 时刻取 now）并回到正常态。
func (m *Manager) EndOfRib(client uint32, now int64) error {
	if !validClient(client) || !validNow(now) {
		return ErrInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if now < m.maxNow {
		return ErrClock
	}
	if m.clientState(client) == stateOffline {
		return ErrClientOffline
	}
	if m.clientState(client) != stateRecovering {
		return ErrState
	}
	events := m.collectExpiries(now)
	m.commitExpiries(events)
	m.maxNow = now

	for _, f := range m.tbl.FecsOf(client) {
		ent, ok := m.tbl.Get(f)
		if !ok {
			continue
		}
		if ow, ok := ent.Owners[client]; ok && ow.Stale {
			label, last, _ := m.tbl.RemoveOwner(f, client)
			if last {
				m.pool.Free(f, label, now)
				m.log = append(m.log, LogEntry{Op: OpFree, Fec: f, Label: label, Time: now})
			}
		}
	}
	m.clients[client] = stateNormal
	return nil
}

// Log 返回标签层日志（ALLOC/FREE）的副本。
func (m *Manager) Log() []LogEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]LogEntry(nil), m.log...)
}

// Restore 由日志重建管理器状态（管理器自身重启）：now 不得小于日志中的
// 最大时刻。仍绑定的 fec 各挂一个截止于 now+R 的占位属主，所有客户端回到
// 正常态；隔离与亲和按日志中的 FREE 时刻照常计算。日志的任意前缀都可重建，
// 且重建出的 fec→标签映射等于写完该前缀时的映射。
// 校验失败（含重放不一致）时不改任何现有状态。
func (m *Manager) Restore(log []LogEntry, now int64) error {
	if !validNow(now) {
		return ErrInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var maxT int64
	for _, e := range log {
		if e.Op != OpAlloc && e.Op != OpFree {
			return ErrInvalid
		}
		if len(e.Fec) < 1 || len(e.Fec) > maxFecLen {
			return ErrInvalid
		}
		if e.Label < m.lo || e.Label > m.hi {
			return ErrInvalid
		}
		if e.Time < 0 || e.Time > maxNow {
			return ErrInvalid
		}
		if e.Time > maxT {
			maxT = e.Time
		}
	}
	if now < maxT {
		return ErrClock
	}
	pool, err := labelpool.New(m.lo, m.hi, m.hd)
	if err != nil {
		return ErrInvalid
	}
	tbl, err := binding.New(m.q)
	if err != nil {
		return ErrInvalid
	}
	var exp expiryHeap
	for _, e := range log {
		switch e.Op {
		case OpAlloc:
			if _, ok := tbl.Get(e.Fec); ok {
				return ErrCorrupt
			}
			if err := pool.AllocRaw(e.Fec, e.Label); err != nil {
				return ErrCorrupt
			}
			tbl.Create(e.Fec, e.Label)
		case OpFree:
			ent, ok := tbl.Get(e.Fec)
			if !ok || ent.Label != e.Label {
				return ErrCorrupt
			}
			tbl.Release(e.Fec)
			pool.Free(e.Fec, e.Label, e.Time)
		}
	}
	for _, f := range tbl.Fecs() {
		tbl.AddPlaceholder(f, now+m.r)
		exp.push(expEvent{deadline: now + m.r, fec: f, client: binding.Placeholder})
	}
	m.pool = pool
	m.tbl = tbl
	m.exp = exp
	m.log = append([]LogEntry(nil), log...)
	m.clients = make(map[uint32]clientState)
	m.maxNow = now
	return nil
}
