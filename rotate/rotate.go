package rotate

import (
	"container/heap"
	"sync"

	"ontology/cert"
)

// 复用 cert 包的哨兵错误。
var (
	ErrInvalid       = cert.ErrInvalid
	ErrClockBack     = cert.ErrClockBack
	ErrDupSerial     = cert.ErrDupSerial
	ErrPendingExists = cert.ErrPendingExists
	ErrTooEarly      = cert.ErrTooEarly
	ErrUnknown       = cert.ErrUnknown
	ErrFinal         = cert.ErrFinal
)

const (
	stPending  = cert.Pending
	stActive   = cert.Active
	stRetiring = cert.Retiring
	stRevoked  = cert.Revoked
	stRetired  = cert.Retired
	stLapsed   = cert.Lapsed
)

type entryKind uint8

const (
	kindLapse  entryKind = iota // Pending -> Lapsed
	kindRetire                  // Retiring -> Retired
)

// entry 是到期堆中的一条迁移；堆内恒无陈旧条目（迁移前必显式删除）。
type entry struct {
	id     int
	at     int64
	dev    string
	serial string
	kind   entryKind
	index  int
}

type entryHeap []*entry

func (h entryHeap) Len() int { return len(h) }
func (h entryHeap) Less(i, j int) bool {
	a, b := h[i], h[j]
	if a.at != b.at {
		return a.at < b.at
	}
	if a.dev != b.dev {
		return a.dev < b.dev
	}
	return a.kind < b.kind // 同时刻同设备：Lapse 先于 Retire
}
func (h entryHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index = i
	h[j].index = j
}
func (h *entryHeap) Push(x any) {
	e := x.(*entry)
	e.index = len(*h)
	*h = append(*h, e)
}
func (h *entryHeap) Pop() any {
	old := *h
	n := len(old)
	e := old[n-1]
	old[n-1] = nil
	*h = old[:n-1]
	e.index = -1
	return e
}

type rec struct {
	cert cert.Cert
	st   cert.State
	// lapseAt/retireAt 仅在对应状态下有效。
	lapseAt  int64
	retireAt int64
	lapseE   *entry
	retireE  *entry
}

// slot 是单设备三张“活”证书的槽位（空串表示无）。
type slot struct {
	active   string
	pending  string
	retiring string
}

// Kicker 在证书落定终态时被调用，返回被踢设备（无则空串）。
// 实现不得回调 Service（Service 持锁期间调用）。
type Kicker func(dev, serial string) string

// SessionSnapper 返回当前会话表的独立副本。
type SessionSnapper func() map[string]string

// SessionRestorer 把会话表原子替换为事务前快照（仅被拒事务调用）。
type SessionRestorer func(snapshot map[string]string)

// Service 是证书轮换状态内核。零值不可用，须经 New 构造。
type Service struct {
	mu      sync.Mutex
	grace   int64
	ttl     int64
	window  int64
	lastNow int64

	bySerial map[string]*rec
	devs     map[string]*slot
	entries  entryHeap
	nextID   int

	kick      Kicker
	snapFn    SessionSnapper
	restoreFn SessionRestorer
	popped    int // 被接受事务的入口结算实际弹出条目数（累计）
}

// New 构造轮换服务；构造参数非法返回 ErrInvalid。
func New(grace, pendingTTL, renewWindow int64) (*Service, error) {
	if !cert.ValidConfig(grace, pendingTTL, renewWindow) {
		return nil, ErrInvalid
	}
	s := &Service{
		grace:    grace,
		ttl:      pendingTTL,
		window:   renewWindow,
		bySerial: map[string]*rec{},
		devs:     map[string]*slot{},
	}
	heap.Init(&s.entries)
	return s, nil
}

// SetHooks 安装会话踢线/回滚钩子（通常由 conn.Manager 调用一次）。
func (s *Service) SetHooks(k Kicker, snap SessionSnapper, restore SessionRestorer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.kick, s.snapFn, s.restoreFn = k, snap, restore
}

func (s *Service) getSlot(dev string) *slot {
	sl := s.devs[dev]
	if sl == nil {
		sl = &slot{}
		s.devs[dev] = sl
	}
	return sl
}

// pushEntry 在堆中登记一条到期迁移并挂到记录上。
func (s *Service) pushEntry(r *rec, k entryKind, at int64) *entry {
	s.nextID++
	e := &entry{id: s.nextID, at: at, dev: r.cert.Dev, serial: r.cert.Serial, kind: k}
	heap.Push(&s.entries, e)
	if k == kindLapse {
		r.lapseE = e
	} else {
		r.retireE = e
	}
	return e
}

// dropEntry 主动删除一条迁移（确认/吊销/替换时），保证堆内无陈旧项。
func (s *Service) dropEntry(e *entry) {
	if e == nil || e.index < 0 {
		return
	}
	heap.Remove(&s.entries, e.index)
}

// Tx 是一次操作事务：入口已按 (now, dev) 结算到期迁移并推进逻辑时钟。
// 所有方法必须在持有 Service 锁时调用；判定拒绝后调用 Abort，成功调用 Commit。
type Tx struct {
	s       *Service
	now     int64
	prevNow int64
	snap    map[string]string // 会话表事务前快照（由钩子提供）
	undos   []func()
	settleK []string // 入口结算造成的踢线（堆序）
	opKick  []string // 本操作造成的踢线（追加）
	settleN int      // 入口结算弹出的条目数
}

// Snapshot 是一张证书的只读视图。
type Snapshot struct {
	Cert cert.Cert
	St   cert.State
}

// Enter 开启事务：先校验时钟回退（此时尚未结算），再结算所有到期迁移。
func (s *Service) Enter(now int64) (*Tx, error) {
	s.mu.Lock()
	if !cert.ValidTime(now) {
		s.mu.Unlock()
		return nil, ErrInvalid
	}
	if now < s.lastNow {
		s.mu.Unlock()
		return nil, ErrClockBack
	}
	tx := &Tx{s: s, now: now}
	tx.prevNow = s.lastNow
	if s.snapFn != nil {
		tx.snap = s.snapFn()
	}
	tx.settle()
	s.lastNow = now
	return tx, nil
}

// settle 弹出所有 at <= now 的条目并落定迁移；记录撤销日志与踢线。
func (tx *Tx) settle() {
	s := tx.s
	for len(s.entries) > 0 && s.entries[0].at <= tx.now {
		e := heap.Pop(&s.entries).(*entry)
		tx.settleN++
		r := s.bySerial[e.serial]
		prev := r.st
		sl := s.devs[r.cert.Dev]

		if e.kind == kindLapse {
			r.lapseE = nil
			r.st = stLapsed
			if sl.pending == r.cert.Serial {
				sl.pending = ""
			}
		} else {
			r.retireE = nil
			r.st = stRetired
			if sl.retiring == r.cert.Serial {
				sl.retiring = ""
			}
		}
		serial := r.cert.Serial
		tx.undos = append(tx.undos, func() {
			r.st = prev
			if e.kind == kindLapse {
				r.lapseE = e
				sl.pending = serial
			} else {
				r.retireE = e
				sl.retiring = serial
			}
			heap.Push(&s.entries, e)
		})

		// Pending 理论上无会话（确认即离开 Pending），仍无条件询问钩子，
		// 与 Revoked/Retired 语义一致：终态落定即踢，钩子自身判断会话归属。
		if s.kick != nil {
			if d := s.kick(r.cert.Dev, r.cert.Serial); d != "" {
				tx.settleK = append(tx.settleK, d)
			}
		}
	}
}

// Now 返回事务时刻。
func (tx *Tx) Now() int64 { return tx.now }

// Lookup 返回证书结算后的状态；不存在返回 ErrUnknown。
func (tx *Tx) Lookup(serial string) (cert.Cert, cert.State, error) {
	r := tx.s.bySerial[serial]
	if r == nil {
		return cert.Cert{}, 0, ErrUnknown
	}
	return r.cert, r.st, nil
}

func (tx *Tx) slot(dev string) *slot { return tx.s.getSlot(dev) }

// fireKick 在本操作阶段踢掉使用某张证书的会话。
func (tx *Tx) fireKick(dev, serial string) {
	if tx.s.kick != nil {
		if d := tx.s.kick(dev, serial); d != "" {
			tx.opKick = append(tx.opKick, d)
		}
	}
}

// DoIssue 在事务内执行签发判定与落库。
func (tx *Tx) DoIssue(c cert.Cert) error {
	s := tx.s
	if _, dup := s.bySerial[c.Serial]; dup {
		return ErrDupSerial
	}
	sl := tx.slot(c.Dev)
	if sl.pending != "" {
		return ErrPendingExists
	}

	r := &rec{cert: c}
	if sl.active == "" {
		// 首次签发，或 Active 已被吊销：直接 Active，不产生 Retiring。
		r.st = stActive
		sl.active = c.Serial
		tx.undos = append(tx.undos, func() {
			sl.active = ""
			delete(s.bySerial, c.Serial)
		})
		s.bySerial[c.Serial] = r
		return nil
	}

	old := s.bySerial[sl.active]
	if !cert.WithinRenew(old.cert.Na, tx.now, s.window) {
		return ErrTooEarly
	}
	at := cert.LapseAt(c.Nb, tx.now, s.ttl)
	r.st = stPending
	r.lapseAt = at
	e := s.pushEntry(r, kindLapse, at)
	sl.pending = c.Serial
	tx.undos = append(tx.undos, func() {
		sl.pending = ""
		s.dropEntry(e)
		delete(s.bySerial, c.Serial)
	})
	s.bySerial[c.Serial] = r
	return nil
}

// DoConfirm 执行“Pending 首次通过准入”的对调。调用前已完成准入七连判。
// 仅当目标为 Pending 时产生迁移；Active/Retiring 直接沿用。
func (tx *Tx) DoConfirm(serial string) {
	s := tx.s
	r := s.bySerial[serial]
	if r.st != stPending {
		return
	}
	dev := r.cert.Dev
	sl := tx.slot(dev)

	// Pending 上位为 Active，并撤销其待确认截止。
	lapseE := r.lapseE
	s.dropEntry(lapseE)
	r.lapseE = nil
	r.st = stActive
	oldActive := sl.active
	sl.active = serial
	sl.pending = ""

	if oldActive == "" {
		// 原 Active 已被吊销：不产生 Retiring。
		tx.undos = append(tx.undos, func() {
			r.st = stPending
			r.lapseE = s.pushEntry(r, kindLapse, r.lapseAt)
			sl.active = ""
			sl.pending = serial
		})
		return
	}

	old := s.bySerial[oldActive]
	// 若还存在一张更老的 Retiring，它立即 Retired（同设备，排在本操作踢线最前）。
	if prevRet := sl.retiring; prevRet != "" {
		pr := s.bySerial[prevRet]
		pe := pr.retireE
		s.dropEntry(pe)
		pr.retireE = nil
		pr.st = stRetired
		sl.retiring = ""
		tx.fireKick(dev, prevRet)
		tx.undos = append(tx.undos, func() {
			pr.st = stRetiring
			pr.retireE = s.pushEntry(pr, kindRetire, pr.retireAt)
			sl.retiring = prevRet
		})
	}

	// 原 Active 转 Retiring，retireAt = min(now+G, na)。
	at := cert.RetireAt(old.cert.Na, tx.now, s.grace)
	old.st = stRetiring
	old.retireAt = at
	oldE := s.pushEntry(old, kindRetire, at)
	sl.retiring = oldActive

	tx.undos = append(tx.undos, func() {
		s.dropEntry(oldE)
		old.st = stActive
		sl.retiring = ""
		sl.active = oldActive
		sl.pending = serial
		r.st = stPending
		r.lapseE = s.pushEntry(r, kindLapse, r.lapseAt)
	})
}

// DoRevoke 在事务内执行吊销判定与落库。
func (tx *Tx) DoRevoke(serial string) error {
	s := tx.s
	r := s.bySerial[serial]
	if r == nil {
		return ErrUnknown
	}
	if r.st.Terminal() {
		return ErrFinal
	}
	prev := r.st
	sl := tx.slot(r.cert.Dev)

	var removedE *entry
	switch prev {
	case stPending:
		removedE = r.lapseE
		s.dropEntry(removedE)
		r.lapseE = nil
		sl.pending = ""
	case stRetiring:
		removedE = r.retireE
		s.dropEntry(removedE)
		r.retireE = nil
		sl.retiring = ""
	case stActive:
		// 吊销 Active：Pending 保留、Retiring 不回升。
		sl.active = ""
	}
	r.st = stRevoked
	tx.fireKick(r.cert.Dev, serial)

	tx.undos = append(tx.undos, func() {
		r.st = prev
		switch prev {
		case stPending:
			r.lapseE = s.pushEntry(r, kindLapse, r.lapseAt)
			sl.pending = serial
		case stRetiring:
			r.retireE = s.pushEntry(r, kindRetire, r.retireAt)
			sl.retiring = serial
		case stActive:
			sl.active = serial
		}
	})
	return nil
}

// Commit 提交事务并返回 Kicked（先结算踢出，后本操作踢出）。
func (tx *Tx) Commit() []string {
	s := tx.s
	s.popped += tx.settleN
	out := append(append([]string{}, tx.settleK...), tx.opKick...)
	tx.undos = nil
	s.mu.Unlock()
	return out
}

// Abort 回滚事务：撤销本操作与入口结算的全部效果，时钟不推进。
func (tx *Tx) Abort() {
	s := tx.s
	for i := len(tx.undos) - 1; i >= 0; i-- {
		tx.undos[i]()
	}
	tx.undos = nil
	s.lastNow = tx.prevNow
	if s.restoreFn != nil {
		s.restoreFn(tx.snap)
	}
	s.mu.Unlock()
}

func validateIssue(dev, serial string, nb, na, now int64) (cert.Cert, bool) {
	c := cert.Cert{Serial: serial, Dev: dev, Nb: nb, Na: na}
	return c, c.Valid() && cert.ValidTime(now)
}

// Issue 签发一张新证书，返回本次 Kicked 清单。
func (s *Service) Issue(dev, serial string, nb, na, now int64) ([]string, error) {
	c, ok := validateIssue(dev, serial, nb, na, now)
	if !ok {
		return nil, ErrInvalid
	}
	tx, err := s.Enter(now)
	if err != nil {
		return nil, err
	}
	if err := tx.DoIssue(c); err != nil {
		tx.Abort()
		return nil, err
	}
	return tx.Commit(), nil
}

// Revoke 吊销证书，返回本次 Kicked 清单。
func (s *Service) Revoke(serial string, now int64) ([]string, error) {
	if serial == "" || !cert.ValidTime(now) {
		return nil, ErrInvalid
	}
	tx, err := s.Enter(now)
	if err != nil {
		return nil, err
	}
	if err := tx.DoRevoke(serial); err != nil {
		tx.Abort()
		return nil, err
	}
	return tx.Commit(), nil
}

// Snapshot 返回所有证书的只读视图（序列号字典序），供测试与观测。
func (s *Service) Snapshot() []Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Snapshot, 0, len(s.bySerial))
	for _, r := range s.bySerial {
		out = append(out, Snapshot{Cert: r.cert, St: r.st})
	}
	sortSnap(out)
	return out
}

// Popped 返回被接受事务入口结算累计弹出的条目数。
func (s *Service) Popped() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.popped
}

func sortSnap(out []Snapshot) {
	// 小范围切片，插入排序即可，避免再引 sort 到公开行为。
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1].Cert.Serial > out[j].Cert.Serial; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
}
