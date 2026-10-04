// Package cert 维护设备证书的状态机、到期结算与在线会话。
//
// 状态机：Pending / Active / Retiring 为活跃态，Revoked / Retired / Lapsed
// 为终态。到期迁移（Pending→Lapsed、Retiring→Retired）是 now 的纯函数，
// 在每个被接受操作的入口统一结算；被拒绝的操作整体回滚，不留下任何痕迹。
package cert

import (
	"container/heap"
	"errors"
	"sort"
	"sync"
)

// MaxTime 是合法时间的上界（含），下界为 0。
const MaxTime = int64(1_000_000_000_000)

// State 为证书状态。
type State int

const (
	Pending State = iota
	Active
	Retiring
	Revoked
	Retired
	Lapsed
)

func (s State) String() string {
	switch s {
	case Pending:
		return "Pending"
	case Active:
		return "Active"
	case Retiring:
		return "Retiring"
	case Revoked:
		return "Revoked"
	case Retired:
		return "Retired"
	case Lapsed:
		return "Lapsed"
	}
	return "Unknown"
}

// Final 报告状态是否为终态。
func (s State) Final() bool { return s == Revoked || s == Retired || s == Lapsed }

var (
	ErrInvalid       = errors.New("cert: invalid argument")
	ErrClockBack     = errors.New("cert: clock moved backwards")
	ErrDupSerial     = errors.New("cert: duplicate serial")
	ErrPendingExists = errors.New("cert: device already has a pending certificate")
	ErrTooEarly      = errors.New("cert: outside renewal window")
	ErrUnknown       = errors.New("cert: unknown serial")
	ErrMismatch      = errors.New("cert: certificate belongs to another device")
	ErrRevoked       = errors.New("cert: certificate revoked")
	ErrRetired       = errors.New("cert: certificate retired")
	ErrLapsed        = errors.New("cert: pending certificate lapsed")
	ErrNotYet        = errors.New("cert: certificate not yet valid")
	ErrExpired       = errors.New("cert: certificate expired")
	ErrNoSession     = errors.New("cert: device has no session")
	ErrFinal         = errors.New("cert: certificate already in final state")
)

// Cert 描述一张证书。LapseAt 仅在 Pending 时有效，RetireAt 仅在 Retiring 时有效。
type Cert struct {
	Serial   string
	Dev      string
	Nb       int64
	Na       int64
	State    State
	LapseAt  int64
	RetireAt int64
}

// expiryItem 是到期堆条目，键为 (at, dev, serial)。
type expiryItem struct {
	at     int64
	dev    string
	serial string
	index  int
}

type expiryHeap []*expiryItem

func (h expiryHeap) Len() int { return len(h) }

func (h expiryHeap) Less(i, j int) bool {
	a, b := h[i], h[j]
	if a.at != b.at {
		return a.at < b.at
	}
	if a.dev != b.dev {
		return a.dev < b.dev
	}
	return a.serial < b.serial
}

func (h expiryHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index = i
	h[j].index = j
}

func (h *expiryHeap) Push(x any) {
	it := x.(*expiryItem)
	it.index = len(*h)
	*h = append(*h, it)
}

func (h *expiryHeap) Pop() any {
	old := *h
	n := len(old)
	it := old[n-1]
	old[n-1] = nil
	*h = old[:n-1]
	return it
}

// Store 持有全部证书、会话与到期结构，由单把互斥锁串行化。
type Store struct {
	mu       sync.Mutex
	g        int64
	t        int64
	w        int64
	now      int64
	certs    map[string]*Cert
	slots    map[string]map[State]string // dev -> 活跃态 -> serial
	sessions map[string]string           // dev -> serial
	exp      expiryHeap
	expIdx   map[string]*expiryItem // serial -> 到期条目（仅 Pending/Retiring）
	popped   int                    // 入口结算从到期堆弹出的条目数
}

// NewStore 构造存储；g、t、w 均须在 [1, 1e9]。
func NewStore(g, t, w int64) (*Store, error) {
	const maxParam = int64(1_000_000_000)
	if g < 1 || g > maxParam || t < 1 || t > maxParam || w < 1 || w > maxParam {
		return nil, ErrInvalid
	}
	return &Store{
		g:        g,
		t:        t,
		w:        w,
		certs:    make(map[string]*Cert),
		slots:    make(map[string]map[State]string),
		sessions: make(map[string]string),
		expIdx:   make(map[string]*expiryItem),
	}, nil
}

// G 返回旧证宽限。
func (s *Store) G() int64 { return s.g }

// T 返回待确认时限。
func (s *Store) T() int64 { return s.t }

// W 返回续期窗口。
func (s *Store) W() int64 { return s.w }

// Now 返回已接受的最大 now。
func (s *Store) Now() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.now
}

// Lookup 按序列号查询证书快照。
func (s *Store) Lookup(serial string) (Cert, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.certs[serial]
	if !ok {
		return Cert{}, false
	}
	return *c, true
}

// SessionOf 返回设备当前会话所用序列号。
func (s *Store) SessionOf(dev string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	serial, ok := s.sessions[dev]
	return serial, ok
}

// Dump 导出全量状态（按序列号排序），供测试对照。
func (s *Store) Dump() ([]Cert, map[string]string, int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	certs := make([]Cert, 0, len(s.certs))
	for _, c := range s.certs {
		certs = append(certs, *c)
	}
	sort.Slice(certs, func(i, j int) bool { return certs[i].Serial < certs[j].Serial })
	sessions := make(map[string]string, len(s.sessions))
	for k, v := range s.sessions {
		sessions[k] = v
	}
	return certs, sessions, s.now
}

// BeginOp 开启一个操作：校验时钟并在入口结算到期迁移。
// 调用方必须以 Commit 或 Abort 结束，期间持锁。
func (s *Store) BeginOp(now int64) (*Op, error) {
	s.mu.Lock()
	if now < s.now {
		s.mu.Unlock()
		return nil, ErrClockBack
	}
	op := &Op{s: s, now: now, poppedBase: s.popped}
	s.settle(op)
	return op, nil
}

// settle 弹出所有到期的条目并执行迁移，踢线按 (时刻, 设备字节序) 记录。
func (s *Store) settle(op *Op) {
	for len(s.exp) > 0 && s.exp[0].at <= op.now {
		it := heap.Pop(&s.exp).(*expiryItem)
		s.popped++
		delete(s.expIdx, it.serial)
		c := s.certs[it.serial]
		var next State
		switch c.State {
		case Pending:
			next = Lapsed
		case Retiring:
			next = Retired
		default:
			continue // 条目随状态迁移即时移除，不会走到这里
		}
		op.Transition(c, next, 0)
		if next == Retired {
			op.KickIfUsed(c)
		}
	}
}

// Op 是一次进行中的操作，记录结算与自身改动以便回滚。
type Op struct {
	s          *Store
	now        int64
	kicked     []string
	undos      []func()
	poppedBase int
	done       bool
}

func (o *Op) record(f func()) { o.undos = append(o.undos, f) }

// Cert 按序列号查询证书（未找到返回 nil）。
func (o *Op) Cert(serial string) *Cert { return o.s.certs[serial] }

// Slot 返回设备处于指定活跃态的证书（无则 nil）。
func (o *Op) Slot(dev string, st State) *Cert {
	if m, ok := o.s.slots[dev]; ok {
		if serial, ok := m[st]; ok {
			return o.s.certs[serial]
		}
	}
	return nil
}

// Session 返回设备当前会话所用序列号。
func (o *Op) Session(dev string) (string, bool) {
	serial, ok := o.s.sessions[dev]
	return serial, ok
}

func (s *Store) addSlot(c *Cert) {
	if c.State.Final() {
		return
	}
	m, ok := s.slots[c.Dev]
	if !ok {
		m = make(map[State]string)
		s.slots[c.Dev] = m
	}
	m[c.State] = c.Serial
}

func (s *Store) delSlot(c *Cert) {
	if c.State.Final() {
		return
	}
	if m, ok := s.slots[c.Dev]; ok {
		if m[c.State] == c.Serial {
			delete(m, c.State)
		}
		if len(m) == 0 {
			delete(s.slots, c.Dev)
		}
	}
}

func (s *Store) addExpiry(c *Cert) {
	var at int64
	switch c.State {
	case Pending:
		at = c.LapseAt
	case Retiring:
		at = c.RetireAt
	default:
		return
	}
	it := &expiryItem{at: at, dev: c.Dev, serial: c.Serial}
	heap.Push(&s.exp, it)
	s.expIdx[c.Serial] = it
}

func (s *Store) delExpiry(c *Cert) {
	if it, ok := s.expIdx[c.Serial]; ok {
		heap.Remove(&s.exp, it.index)
		delete(s.expIdx, c.Serial)
	}
}

// Insert 加入一张新证书（状态须为 Pending 或 Active）。
func (o *Op) Insert(c *Cert) {
	s := o.s
	s.certs[c.Serial] = c
	s.addSlot(c)
	s.addExpiry(c)
	o.record(func() {
		s.delSlot(c)
		s.delExpiry(c)
		delete(s.certs, c.Serial)
	})
}

// Transition 迁移证书状态；at 在目标为 Pending/Retiring 时分别为 lapseAt/retireAt。
func (o *Op) Transition(c *Cert, st State, at int64) {
	s := o.s
	old := *c
	s.delSlot(c)
	s.delExpiry(c)
	c.State = st
	if st == Pending {
		c.LapseAt = at
	}
	if st == Retiring {
		c.RetireAt = at
	}
	s.addSlot(c)
	s.addExpiry(c)
	o.record(func() {
		s.delSlot(c)
		s.delExpiry(c)
		*c = old
		s.addSlot(c)
		s.addExpiry(c)
	})
}

// KickIfUsed 若设备会话正在使用 c，则断开并记入 Kicked。
func (o *Op) KickIfUsed(c *Cert) {
	s := o.s
	if serial, ok := s.sessions[c.Dev]; ok && serial == c.Serial {
		delete(s.sessions, c.Dev)
		o.kicked = append(o.kicked, c.Dev)
		o.record(func() {
			s.sessions[c.Dev] = serial
			o.kicked = o.kicked[:len(o.kicked)-1]
		})
	}
}

// SetSession 建立设备会话（接管旧会话，旧会话不进入踢线清单）。
func (o *Op) SetSession(dev, serial string) {
	s := o.s
	old, had := s.sessions[dev]
	s.sessions[dev] = serial
	o.record(func() {
		if had {
			s.sessions[dev] = old
		} else {
			delete(s.sessions, dev)
		}
	})
}

// EndSession 结束设备会话。
func (o *Op) EndSession(dev string) {
	s := o.s
	old, had := s.sessions[dev]
	delete(s.sessions, dev)
	o.record(func() {
		if had {
			s.sessions[dev] = old
		}
	})
}

// Commit 提交操作：推进时钟并返回 Kicked 清单。
func (o *Op) Commit() []string {
	o.s.now = o.now
	o.done = true
	kicked := o.kicked
	o.s.mu.Unlock()
	return kicked
}

// Abort 回滚操作（时钟不推进、结算不落地）并返回给定错误。
func (o *Op) Abort(err error) error {
	s := o.s
	for i := len(o.undos) - 1; i >= 0; i-- {
		o.undos[i]()
	}
	s.popped = o.poppedBase
	o.done = true
	s.mu.Unlock()
	return err
}
