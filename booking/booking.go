package booking

import (
	"container/heap"
	"errors"
	"sync"

	"ontology/credit"
	"ontology/slotpool"
)

// Channel 渠道（与 slotpool 保持同一组取值）。
type Channel int

const (
	Online Channel = iota + 1
	Onsite
)

// 全部业务错误。
var (
	ErrInvalid        = errors.New("invalid argument")
	ErrClockRollback  = errors.New("clock rollback")
	ErrSlotNotFound   = errors.New("slot not found")
	ErrSlotOpen       = errors.New("slot already open")
	ErrDuplicate      = errors.New("patient already holds reservation or waitlist entry")
	ErrBanned         = errors.New("patient is banned")
	ErrNoQuota        = errors.New("no quota available")
	ErrTooEarly       = errors.New("check-in too early")
	ErrNoReservation  = errors.New("no valid reservation")
	ErrAlreadyChecked = errors.New("already checked in")
	ErrCannotWait     = errors.New("cannot join waitlist")
)

// Booking 门诊号源总控。所有方法在同一把锁下串行执行，
// 并发结果等价于某个确定的串行顺序。
type Booking struct {
	r, e, g, c int64
	mu         sync.Mutex
	pool       *slotpool.Pool
	cred       *credit.Store

	now    int64 // 已接受操作的最大时刻
	nextID int64
	slots  map[string]*slot
	resv   map[int64]*reservation
	resvOf map[string]*reservation // patient\x00slot -> 有效预约
	waitOf map[string]bool         // patient\x00slot -> 在候补中
	due    dueHeap
	expire expireHeap

	dueTouched int // 最近一次已提交操作落地时的到期堆取出/窥视数
}

type slot struct {
	name    string
	start   int64
	cap     int
	on      int
	wait    []string // 候补患者（加入序 FIFO）
	expVer  int
	expItem *expireItem
}

type reservation struct {
	id        int64
	patient   string
	slot      string
	ch        Channel
	checkedIn bool
	due       *dueItem
}

// dueItem 到期堆条目，按 (dueAt, id) 升序。
type dueItem struct {
	dueAt int64
	id    int64
	idx   int
}

type dueHeap []*dueItem

func (h dueHeap) Len() int { return len(h) }
func (h dueHeap) Less(i, j int) bool {
	if h[i].dueAt != h[j].dueAt {
		return h[i].dueAt < h[j].dueAt
	}
	return h[i].id < h[j].id
}
func (h dueHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].idx = i
	h[j].idx = j
}
func (h *dueHeap) Push(x any) {
	it := x.(*dueItem)
	it.idx = len(*h)
	*h = append(*h, it)
}
func (h *dueHeap) Pop() any {
	old := *h
	n := len(old)
	it := old[n-1]
	old[n-1] = nil
	*h = old[:n-1]
	return it
}

// expireItem 候补作废堆条目（at = start+G）。
type expireItem struct {
	at      int64
	slot    string
	version int
	idx     int
}

type expireHeap []*expireItem

func (h expireHeap) Len() int { return len(h) }
func (h expireHeap) Less(i, j int) bool {
	if h[i].at != h[j].at {
		return h[i].at < h[j].at
	}
	if h[i].slot != h[j].slot {
		return h[i].slot < h[j].slot
	}
	return h[i].version < h[j].version
}
func (h expireHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].idx = i
	h[j].idx = j
}
func (h *expireHeap) Push(x any) {
	it := x.(*expireItem)
	it.idx = len(*h)
	*h = append(*h, it)
}
func (h *expireHeap) Pop() any {
	old := *h
	n := len(old)
	it := old[n-1]
	old[n-1] = nil
	*h = old[:n-1]
	return it
}

// New 创建总控，参数 R/E/G/C/K/W 含义见题目。
func New(r, e, g, c int64, k int, w int64) *Booking {
	return &Booking{
		r:      r,
		e:      e,
		g:      g,
		c:      c,
		pool:   slotpool.New(r),
		cred:   credit.New(k, w),
		nextID: 1,
		slots:  map[string]*slot{},
		resv:   map[int64]*reservation{},
		resvOf: map[string]*reservation{},
		waitOf: map[string]bool{},
	}
}

func holdKey(patient []byte, slotName string) string {
	return string(patient) + "\x00" + slotName
}

func validChannel(ch Channel) bool { return ch == Online || ch == Onsite }

// tx 是一次操作的 undo 日志。被接受操作先落地、后判定；
// 一旦判定失败，落地与本操作的全部变更按逆序回滚，
// 从而被拒不落地、不推进时钟。
type tx struct {
	b     *Booking
	undos []func()
	pops  int
	peek  bool
}

func (t *tx) undo(fn func()) { t.undos = append(t.undos, fn) }

func (t *tx) rollback() {
	for i := len(t.undos) - 1; i >= 0; i-- {
		t.undos[i]()
	}
}

func (t *tx) reject(err error) error {
	t.rollback()
	return err
}

// touched = 实际落地数 +（是否窥视到首个未到期堆顶），≤ 落地数 + 1。
func (t *tx) touched() int {
	n := t.pops
	if t.peek {
		n++
	}
	return n
}

// settle 落地所有 now 已到期（dueAt < now，恰等不算）的未签到预约，
// 并作废已过 grace（at < now）的候补队。
// 到期堆每轮只窥视堆顶一次，读取条目数与预约总数无关。
func (t *tx) settle(now int64) {
	b := t.b
	if len(b.due) > 0 {
		t.peek = true
	}
	for len(b.due) > 0 {
		top := b.due[0]
		if top.dueAt >= now {
			break
		}
		heap.Pop(&b.due)
		t.pops++
		r := b.resv[top.id]
		r.due = nil
		t.landOne(r, top.dueAt, now)
	}
	for len(b.expire) > 0 && b.expire[0].at < now {
		it := heap.Pop(&b.expire).(*expireItem)
		sl := b.slots[it.slot]
		if sl == nil || it.version != sl.expVer {
			continue
		}
		waiters := sl.wait
		sl.wait = nil
		sl.expItem = nil
		oldVer := sl.expVer
		sl.expVer++
		for _, p := range waiters {
			delete(b.waitOf, p+"\x00"+it.slot)
		}
		slotName := it.slot
		t.undo(func() {
			sl2 := b.slots[slotName]
			sl2.wait = waiters
			sl2.expVer = oldVer
			sl2.expItem = it
			for _, p := range waiters {
				b.waitOf[p+"\x00"+slotName] = true
			}
			heap.Push(&b.expire, it)
		})
	}
}

// landOne 把预约落地为爽约（时刻取 dueAt=start+G），随后队首候补立即递补。
func (t *tx) landOne(r *reservation, dueAt, now int64) {
	b := t.b
	sl := b.slots[r.slot]
	delete(b.resv, r.id)
	delete(b.resvOf, r.patient+"\x00"+r.slot)
	b.pool.Release(r.slot, slotpool.Channel(r.ch))
	b.cred.Add([]byte(r.patient), dueAt)
	patient, slotName, ch, rid := r.patient, r.slot, r.ch, r.id
	t.undo(func() {
		b.cred.PopLast([]byte(patient))
		// 递补闭包先于本闭包回滚，已让出占用名额，故此处必能占回。
		if err := b.pool.Book(slotName, now, slotpool.Channel(ch)); err != nil {
			panic(err)
		}
		b.resv[rid] = r
		b.resvOf[patient+"\x00"+slotName] = r
		heap.Push(&b.due, &dueItem{dueAt: dueAt, id: rid})
	})
	t.promote(sl, now)
}

// promote 让队首候补立即递补，计入 us；now>start 直接已签到。
func (t *tx) promote(sl *slot, now int64) {
	b := t.b
	if len(sl.wait) == 0 {
		return
	}
	slotName := sl.name
	patient := sl.wait[0]
	rest := sl.wait[1:]
	sl.wait = rest
	delete(b.waitOf, patient+"\x00"+slotName)
	var removedExpire *expireItem
	if sl.expItem != nil && len(rest) == 0 {
		removedExpire = sl.expItem
		heap.Remove(&b.expire, sl.expItem.idx)
		sl.expItem = nil
	}
	if err := b.pool.Book(slotName, now, slotpool.Onsite); err != nil {
		panic(err)
	}
	id := b.nextID
	b.nextID++
	nr := &reservation{id: id, patient: patient, slot: slotName, ch: Onsite}
	b.resv[id] = nr
	b.resvOf[patient+"\x00"+slotName] = nr
	directCheckIn := now > sl.start
	if directCheckIn {
		nr.checkedIn = true
	} else {
		nr.due = &dueItem{dueAt: sl.start + b.g, id: id}
		heap.Push(&b.due, nr.due)
	}
	t.undo(func() {
		if !directCheckIn {
			heap.Remove(&b.due, nr.due.idx)
		}
		delete(b.resv, id)
		delete(b.resvOf, patient+"\x00"+slotName)
		b.pool.Release(slotName, slotpool.Onsite)
		b.nextID = id
		sl.wait = append([]string{patient}, sl.wait...)
		b.waitOf[patient+"\x00"+slotName] = true
		if removedExpire != nil {
			sl.expItem = removedExpire
			heap.Push(&b.expire, removedExpire)
		}
	})
}

// AddSlot 新增时段。
func (b *Booking) AddSlot(now int64, slotName string, start int64, cap, on int) error {
	if now < 0 || slotName == "" || start <= now || cap < 1 || cap > 1000 || on < 0 || on > cap {
		return ErrInvalid
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if now < b.now {
		return ErrClockRollback
	}
	if _, ok := b.slots[slotName]; ok {
		return ErrInvalid
	}
	t := &tx{b: b}
	t.settle(now)
	if err := b.pool.Add(slotName, start, cap, on); err != nil {
		return t.reject(ErrInvalid)
	}
	b.slots[slotName] = &slot{name: slotName, start: start, cap: cap, on: on}
	name := slotName
	t.undo(func() { delete(b.slots, name) })
	b.now = now
	b.dueTouched = t.touched()
	return nil
}

// Book 订号，成功返回全局递增的预约序号。
func (b *Booking) Book(now int64, patient []byte, slotName string, ch Channel) (int64, error) {
	if now < 0 || len(patient) == 0 || slotName == "" || !validChannel(ch) {
		return 0, ErrInvalid
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if now < b.now {
		return 0, ErrClockRollback
	}
	sl, ok := b.slots[slotName]
	if !ok {
		return 0, ErrSlotNotFound
	}
	if now >= sl.start {
		return 0, ErrSlotOpen
	}
	t := &tx{b: b}
	t.settle(now)
	key := holdKey(patient, slotName)
	if _, ok := b.resvOf[key]; ok || b.waitOf[key] {
		return 0, t.reject(ErrDuplicate)
	}
	if ch == Online && b.cred.Banned(now, patient) {
		return 0, t.reject(ErrBanned)
	}
	if !b.pool.CanBook(slotName, now, slotpool.Channel(ch)) {
		return 0, t.reject(ErrNoQuota)
	}
	if err := b.pool.Book(slotName, now, slotpool.Channel(ch)); err != nil {
		return 0, t.reject(ErrNoQuota)
	}
	t.undo(func() { b.pool.Release(slotName, slotpool.Channel(ch)) })
	id := b.nextID
	b.nextID++
	r := &reservation{id: id, patient: string(patient), slot: slotName, ch: ch}
	b.resv[id] = r
	b.resvOf[key] = r
	r.due = &dueItem{dueAt: sl.start + b.g, id: id}
	heap.Push(&b.due, r.due)
	t.undo(func() {
		heap.Remove(&b.due, r.due.idx)
		delete(b.resv, id)
		delete(b.resvOf, key)
		b.nextID = id
	})
	b.now = now
	b.dueTouched = t.touched()
	return id, nil
}

// CheckIn 签到：start-E <= now <= start+G，两端取等均可。
func (b *Booking) CheckIn(now int64, patient []byte, slotName string) error {
	if now < 0 || len(patient) == 0 || slotName == "" {
		return ErrInvalid
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if now < b.now {
		return ErrClockRollback
	}
	sl, ok := b.slots[slotName]
	if !ok {
		return ErrSlotNotFound
	}
	t := &tx{b: b}
	t.settle(now)
	if now < sl.start-b.e {
		return t.reject(ErrTooEarly)
	}
	r, ok := b.resvOf[holdKey(patient, slotName)]
	if !ok {
		return t.reject(ErrNoReservation)
	}
	if r.checkedIn {
		return t.reject(ErrAlreadyChecked)
	}
	r.checkedIn = true
	heap.Remove(&b.due, r.due.idx)
	r.due = nil
	t.undo(func() {
		r.checkedIn = false
		r.due = &dueItem{dueAt: sl.start + b.g, id: r.id}
		heap.Push(&b.due, r.due)
	})
	b.now = now
	b.dueTouched = t.touched()
	return nil
}

// Cancel 仅对未签到的有效预约：now <= start-C 免责，其后迟退并记爽约。
func (b *Booking) Cancel(now int64, patient []byte, slotName string) error {
	if now < 0 || len(patient) == 0 || slotName == "" {
		return ErrInvalid
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if now < b.now {
		return ErrClockRollback
	}
	sl, ok := b.slots[slotName]
	if !ok {
		return ErrSlotNotFound
	}
	t := &tx{b: b}
	t.settle(now)
	key := holdKey(patient, slotName)
	r, ok := b.resvOf[key]
	if !ok {
		return t.reject(ErrNoReservation)
	}
	if r.checkedIn {
		return t.reject(ErrAlreadyChecked)
	}
	if r.due != nil {
		heap.Remove(&b.due, r.due.idx)
	}
	delete(b.resv, r.id)
	delete(b.resvOf, key)
	b.pool.Release(slotName, slotpool.Channel(r.ch))
	late := now > sl.start-b.c
	if late {
		b.cred.Add(patient, now)
	}
	t.undo(func() {
		if late {
			b.cred.PopLast(patient)
		}
		if err := b.pool.Book(slotName, now, slotpool.Channel(r.ch)); err != nil {
			panic(err)
		}
		b.resv[r.id] = r
		b.resvOf[key] = r
		r.due = &dueItem{dueAt: sl.start + b.g, id: r.id}
		heap.Push(&b.due, r.due)
	})
	t.promote(sl, now)
	b.now = now
	b.dueTouched = t.touched()
	return nil
}

// JoinWait 现场患者在 start-R <= now <= start+G 且满员时加入候补。
func (b *Booking) JoinWait(now int64, patient []byte, slotName string, ch Channel) error {
	if now < 0 || len(patient) == 0 || slotName == "" || !validChannel(ch) {
		return ErrInvalid
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if now < b.now {
		return ErrClockRollback
	}
	sl, ok := b.slots[slotName]
	if !ok {
		return ErrSlotNotFound
	}
	t := &tx{b: b}
	t.settle(now)
	key := holdKey(patient, slotName)
	if _, ok := b.resvOf[key]; ok || b.waitOf[key] {
		return t.reject(ErrDuplicate)
	}
	if ch != Onsite || now < sl.start-b.r || now > sl.start+b.g {
		return t.reject(ErrCannotWait)
	}
	// 满员当且仅当两渠道都订不进（释放前分账、释放后公共池，该等价均成立）。
	if b.pool.CanBook(slotName, now, slotpool.Online) ||
		b.pool.CanBook(slotName, now, slotpool.Onsite) {
		return t.reject(ErrCannotWait)
	}
	sl.wait = append(sl.wait, string(patient))
	b.waitOf[key] = true
	if sl.expItem == nil {
		item := &expireItem{at: sl.start + b.g, slot: slotName, version: sl.expVer}
		sl.expItem = item
		heap.Push(&b.expire, item)
	}
	t.undo(func() {
		sl.wait = sl.wait[:len(sl.wait)-1]
		delete(b.waitOf, key)
		if sl.expItem != nil && len(sl.wait) == 0 {
			heap.Remove(&b.expire, sl.expItem.idx)
			sl.expItem = nil
		}
	})
	b.now = now
	b.dueTouched = t.touched()
	return nil
}

// Banned 只读判定，不落地、不推进时钟。
func (b *Booking) Banned(now int64, patient []byte) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.cred.Banned(now, patient)
}

// DueTouched 返回最近一次已接受操作落地时从到期结构取出/窥视的条目数。
func (b *Booking) DueTouched() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.dueTouched
}

// CreditTouched 返回最近一次禁约判定读取的爽约记录条数。
func (b *Booking) CreditTouched() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.cred.Touched()
}
