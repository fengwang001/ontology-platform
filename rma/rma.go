// Package rma 管理退货授权单、可退余量与惰性到期。
package rma

import (
	"container/heap"
	"errors"
	"sort"
	"sync"
)

// 哨兵错误：所有操作的拒绝原因均可用 errors.Is 区分。
var (
	ErrInvalid   = errors.New("rma: invalid argument")
	ErrClockBack = errors.New("rma: clock moved backwards")
	ErrNotFound  = errors.New("rma: not found")
	ErrConflict  = errors.New("rma: conflict")
	ErrWindow    = errors.New("rma: outside return window")
	ErrExpired   = errors.New("rma: authorization expired")
	ErrCapacity  = errors.New("rma: quantity exceeds available capacity")
)

// Line 是订单行的发货信息。
type Line struct {
	Shipped int64
	Paid    int64
}

// Item 是一次授权申请中的一项。
type Item struct {
	Line string
	Qty  int64
}

// Config 为系统构造参数。
type Config struct {
	WindowDays int64 // Wd：退货窗口，1..1e6
	ValidFor   int64 // V：授权有效期，1..1e6
}

// 行状态。receivedA/receivedB 为累计合格收货（C 不计入）；
// reserved 为全部“有效授权单”在该行的未收数量合计。
type lineState struct {
	shipped   int64
	paid      int64
	receivedA int64
	receivedB int64
	reserved  int64
}

type orderRec struct {
	shipAt int64
	lines  map[string]*lineState
}

// 授权单记录：到期后保留，valid 置 false，未收数量已在行上释放。
type authRec struct {
	id    string
	order string
	exp   int64
	seq   int64
	valid bool
	lines map[string]bool  // 原始申请项集合（收货时“是否含此行”的判定依据）
	open  map[string]int64 // 行 -> 未收数量
}

// 到期堆：V 固定、时钟单调，到期次序即建单次序，以 seq 排序即可。
type expiryEntry struct {
	exp int64
	seq int64
	id  string
}

type expiryHeap []expiryEntry

func (h expiryHeap) Len() int           { return len(h) }
func (h expiryHeap) Less(i, j int) bool { return h[i].seq < h[j].seq }
func (h expiryHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *expiryHeap) Push(x any)        { *h = append(*h, x.(expiryEntry)) }
func (h *expiryHeap) Pop() any {
	old := *h
	n := len(old)
	it := old[n-1]
	*h = old[:n-1]
	return it
}

// Store 是退货授权系统的全部状态。零值不可用，须用 New 构造。
type Store struct {
	mu      sync.Mutex
	cfg     Config
	clock   int64
	hasTime bool
	nextSeq int64

	orders map[string]*orderRec
	auths  map[string]*authRec
	expiry expiryHeap

	// touched 统计一次被接受操作真实触碰的授权单记录数。
	// Authorize：弹出的到期单每张 +1，每个项行的余量校验 +1，
	// 新建授权单 +1，故 ≤ 到期单数 + 项数 + 1，与仍有效单总数无关。
	touched int
}

// New 校验构造参数并返回空系统。
func New(cfg Config) (*Store, error) {
	if cfg.WindowDays < 1 || cfg.WindowDays > 1_000_000 ||
		cfg.ValidFor < 1 || cfg.ValidFor > 1_000_000 {
		return nil, ErrInvalid
	}
	s := &Store{
		cfg:    cfg,
		orders: map[string]*orderRec{},
		auths:  map[string]*authRec{},
	}
	heap.Init(&s.expiry)
	return s, nil
}

func validNow(now int64) bool { return now >= 0 && now <= 1_000_000_000 }

// Txn 在持锁、时钟已校验的上下文中执行 fn。
// fn 返回非 nil 错误时，本次操作不做任何改动（时钟不推进、到期不落地）。
// 约定：fn 内必须先完成全部只读校验，再调用落地方法。
func (s *Store) Txn(now int64, fn func(t *Txn) error) error {
	if !validNow(now) {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.hasTime && now < s.clock {
		return ErrClockBack
	}
	s.touched = 0
	t := &Txn{s: s, now: now}
	if err := fn(t); err != nil {
		return err
	}
	s.clock = now
	s.hasTime = true
	return nil
}

// Txn 是持锁操作上下文。
type Txn struct {
	s   *Store
	now int64
}

// Now 返回本事务的操作时刻。
func (t *Txn) Now() int64 { return t.now }

// Order 返回订单（不存在返回 ErrNotFound）。
func (t *Txn) Order(id string) (*orderRec, error) {
	o := t.s.orders[id]
	if o == nil {
		return nil, ErrNotFound
	}
	return o, nil
}

// Line 返回订单行状态（不存在返回 ErrNotFound）。
func (t *Txn) Line(orderID, line string) (*lineState, error) {
	o, err := t.Order(orderID)
	if err != nil {
		return nil, err
	}
	ls := o.lines[line]
	if ls == nil {
		return nil, ErrNotFound
	}
	return ls, nil
}

// Auth 返回授权单（不存在返回 ErrNotFound）。
func (t *Txn) Auth(id string) (*authRec, error) {
	a := t.s.auths[id]
	if a == nil {
		return nil, ErrNotFound
	}
	return a, nil
}

// OrderID 返回授权单所属订单。
func (a *authRec) OrderID() string { return a.order }

// Valid 返回授权单是否仍有效（未到期）。
func (a *authRec) Valid() bool { return a.valid }

// Exp 返回授权单的到期时刻。
func (a *authRec) Exp() int64 { return a.exp }

// Open 返回授权单中某行的未收数量（不含该行则为 0）。
func (a *authRec) Open(line string) int64 { return a.open[line] }

// Contains 返回该行是否在授权单的原始申请项中（即使已全部收完也算含）。
func (a *authRec) Contains(line string) bool { return a.lines[line] }

// ExpireDue 失效所有 exp<=now 的授权单，仅释放其未收预占；
// 已收合格数与退款历史不受影响。返回被失效的授权单 id（按到期次序）。
func (t *Txn) ExpireDue() []string {
	var ids []string
	for t.s.expiry.Len() > 0 && t.s.expiry[0].exp <= t.now {
		e := heap.Pop(&t.s.expiry).(expiryEntry)
		a := t.s.auths[e.id]
		if a == nil || !a.valid {
			continue
		}
		a.valid = false
		for ln, q := range a.open {
			t.s.orders[a.order].lines[ln].reserved -= q
			delete(a.open, ln)
		}
		ids = append(ids, a.id)
		t.s.touched++ // 真实触碰一张到期授权单记录
	}
	return ids
}

// CreateAuth 落地新授权单并预占数量，返回 exp。调用前须已过期落地并完成校验。
func (t *Txn) CreateAuth(id, orderID string, items []Item) int64 {
	a := &authRec{
		id:    id,
		order: orderID,
		exp:   t.now + t.s.cfg.ValidFor,
		seq:   t.s.nextSeq,
		valid: true,
		lines: map[string]bool{},
		open:  map[string]int64{},
	}
	t.s.nextSeq++
	for _, it := range items {
		t.s.orders[orderID].lines[it.Line].reserved += it.Qty
		a.open[it.Line] += it.Qty
		a.lines[it.Line] = true
		t.s.touched++ // 触碰一个项行的余量记录
	}
	t.s.auths[id] = a
	heap.Push(&t.s.expiry, expiryEntry{exp: a.exp, seq: a.seq, id: id})
	t.s.touched++ // 新建授权单记录本身
	return a.exp
}

// PrepareAuth 在不改状态的前提下完成 Authorize 的全部预检：
// 订单/行存在、授权单号冲突、窗口、余量（含本轮将到期单的释放量）。
// 通过后调用方应在同一事务内立即 ExpireDue + CreateAuth。
func (t *Txn) PrepareAuth(id, orderID string, items []Item) (*orderRec, error) {
	o, err := t.Order(orderID)
	if err != nil {
		return nil, err
	}
	for _, it := range items {
		if o.lines[it.Line] == nil {
			return nil, ErrNotFound
		}
	}
	if t.s.auths[id] != nil {
		return nil, ErrConflict
	}
	if t.now-o.shipAt > t.s.cfg.WindowDays {
		return nil, ErrWindow
	}
	// 在到期堆副本上模拟本轮会释放的未收数量，不改动任何状态。
	dry := append(expiryHeap(nil), t.s.expiry...)
	releasing := map[string]int64{}
	for dry.Len() > 0 {
		top := dry[0]
		if top.exp > t.now {
			break
		}
		e := heap.Pop(&dry).(expiryEntry)
		a := t.s.auths[e.id]
		if a != nil && a.valid {
			for ln, q := range a.open {
				releasing[a.order+"\x00"+ln] += q
			}
		}
	}
	need := map[string]int64{}
	for _, it := range items {
		ls := o.lines[it.Line]
		need[it.Line] += it.Qty
		free := ls.shipped - ls.receivedA - ls.receivedB - ls.reserved +
			releasing[orderID+"\x00"+it.Line]
		if need[it.Line] > free {
			return nil, ErrCapacity
		}
	}
	return o, nil
}

// ReceiveData 为一次收货落地后回传的结算所需数据。
type ReceiveData struct {
	Shipped, Paid          int64
	OldA, OldB, NewA, NewB int64
}

// Receive 落地一次收货：扣减授权未收；qualified（A/B）计入合格数，C 不计入。
func (t *Txn) Receive(a *authRec, line string, qty int64, gradeA bool) ReceiveData {
	ls := t.s.orders[a.order].lines[line]
	d := ReceiveData{
		Shipped: ls.shipped,
		Paid:    ls.paid,
		OldA:    ls.receivedA,
		OldB:    ls.receivedB,
	}
	a.open[line] -= qty
	ls.reserved -= qty
	if a.open[line] == 0 {
		delete(a.open, line)
	}
	if gradeA {
		ls.receivedA += qty
	} else {
		ls.receivedB += qty
	}
	d.NewA = ls.receivedA
	d.NewB = ls.receivedB
	return d
}

// ReceiveC 落地一次 C 级收货：只扣授权未收与行预占，不计合格数。
func (t *Txn) ReceiveC(a *authRec, line string, qty int64) {
	ls := t.s.orders[a.order].lines[line]
	a.open[line] -= qty
	ls.reserved -= qty
	if a.open[line] == 0 {
		delete(a.open, line)
	}
}

// Touched 返回最近一次被接受操作触碰的授权单记录数。
func (s *Store) Touched() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.touched
}

// LineInfo 是行状态的只读快照。
type LineInfo struct {
	Shipped, Paid, A, B, Reserved int64
}

// AuthInfo 是授权单的只读快照。
type AuthInfo struct {
	Order string
	Exp   int64
	Valid bool
	Open  map[string]int64
}

// InspectLine 返回订单行的当前状态（不推进时钟、不落地到期）。
func (s *Store) InspectLine(orderID, line string) (LineInfo, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o := s.orders[orderID]
	if o == nil {
		return LineInfo{}, false
	}
	ls := o.lines[line]
	if ls == nil {
		return LineInfo{}, false
	}
	return LineInfo{
		Shipped: ls.shipped, Paid: ls.paid,
		A: ls.receivedA, B: ls.receivedB, Reserved: ls.reserved,
	}, true
}

// InspectAuth 返回授权单快照（不推进时钟、不落地到期）。
func (s *Store) InspectAuth(id string) (AuthInfo, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.auths[id]
	if a == nil {
		return AuthInfo{}, false
	}
	open := make(map[string]int64, len(a.open))
	for k, v := range a.open {
		open[k] = v
	}
	return AuthInfo{Order: a.order, Exp: a.exp, Valid: a.valid, Open: open}, true
}

// ValidAuthCount 返回记录中仍有效（未惰性落地到期）的授权单数量，供测试对照。
func (s *Store) ValidAuthCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, a := range s.auths {
		if a.valid {
			n++
		}
	}
	return n
}

// AddOrder 登记一张已发货订单。
// 拒绝次序：参数非法 > 时钟回退 > 冲突（订单号重复）。
func (s *Store) AddOrder(orderID string, shipAt int64, lines map[string]Line, now int64) error {
	if orderID == "" || shipAt < 0 || shipAt > 1_000_000_000 ||
		len(lines) < 1 || len(lines) > 100 {
		return ErrInvalid
	}
	names := make([]string, 0, len(lines))
	for name, ln := range lines {
		if name == "" || ln.Shipped < 1 || ln.Shipped > 1_000_000 ||
			ln.Paid < 0 || ln.Paid > 1_000_000_000_000 {
			return ErrInvalid
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return s.Txn(now, func(t *Txn) error {
		if s.orders[orderID] != nil {
			return ErrConflict
		}
		o := &orderRec{shipAt: shipAt, lines: map[string]*lineState{}}
		for _, name := range names {
			ln := lines[name]
			o.lines[name] = &lineState{shipped: ln.Shipped, paid: ln.Paid}
		}
		s.orders[orderID] = o
		return nil
	})
}

// Authorize 按全有或全无方式创建授权单，返回到期时刻。
// 拒绝次序：参数非法 > 时钟回退 > 不存在（订单、行）> 冲突（单号重复）
// > 超出窗口 > 余量不足（报下标最小的失败项）。
func (s *Store) Authorize(id, orderID string, items []Item, now int64) (int64, error) {
	return s.authorize(id, orderID, items, now, nil)
}

func (s *Store) authorize(id, orderID string, items []Item, now int64,
	hook func(exp int64)) (int64, error) {
	if id == "" || len(items) < 1 || len(items) > 100 {
		return 0, ErrInvalid
	}
	seen := map[string]bool{}
	for _, it := range items {
		if it.Line == "" || it.Qty < 1 || seen[it.Line] {
			return 0, ErrInvalid
		}
		seen[it.Line] = true
	}
	var exp int64
	err := s.Txn(now, func(t *Txn) error {
		if _, err := t.PrepareAuth(id, orderID, items); err != nil {
			return err
		}
		// 全部通过：惰性落地到期，再建单预占。
		t.ExpireDue()
		exp = t.CreateAuth(id, orderID, items)
		if hook != nil {
			hook(exp)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return exp, nil
}

// AuthorizeHook 与 Authorize 相同，但建单成功、事务提交前在锁内回调 hook，
// 供外部账本（如手续费开户）随授权原子生效；hook 内不得回改 rma 状态。
func (s *Store) AuthorizeHook(id, orderID string, items []Item, now int64,
	hook func(exp int64)) (int64, error) {
	return s.authorize(id, orderID, items, now, hook)
}
