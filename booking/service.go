package booking

import (
	"container/heap"
	"fmt"
	"sort"
	"sync"
)

// expiryItem 是保留按到期时刻排序的堆元素。
type expiryItem struct {
	expiry int
	id     string
}

// expiryHeap 是按到期时刻的小根堆；已确认/已失效的条目为惰性删除的陈旧项。
type expiryHeap []expiryItem

func (h expiryHeap) Len() int           { return len(h) }
func (h expiryHeap) Less(i, j int) bool { return h[i].expiry < h[j].expiry }
func (h expiryHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *expiryHeap) Push(x any)        { *h = append(*h, x.(expiryItem)) }
func (h *expiryHeap) Pop() any {
	old := *h
	n := len(old)
	it := old[n-1]
	*h = old[:n-1]
	return it
}

// listing 是单个房源的全部状态。
type listing struct {
	id       string
	cfg      ListingCfg
	cal      *calendar
	bookings map[string]*Booking
	blocks   map[string]*Block
	exp      expiryHeap
}

// detachExpired 把在 now 下已失效（expiry < now）的保留从日历摘除并出堆。
// 操作被接受时 commitExpired 将其标记为失效；被拒绝时 rollbackExpired
// 原样放回，保证被拒绝的操作不留痕。
func (l *listing) detachExpired(now int) []*Booking {
	var out []*Booking
	for len(l.exp) > 0 {
		top := l.exp[0]
		if top.expiry >= now {
			break
		}
		heap.Pop(&l.exp)
		b := l.bookings[top.id]
		if b == nil || b.Status != StatusHold {
			continue // 陈旧条目，直接丢弃
		}
		l.cal.occ.remove(b.Checkin)
		out = append(out, b)
	}
	return out
}

func (l *listing) commitExpired(expired []*Booking) {
	for _, b := range expired {
		b.Status = StatusExpired
	}
}

func (l *listing) rollbackExpired(expired []*Booking) {
	for _, b := range expired {
		l.cal.occ.insert(ivl{start: b.Checkin, end: b.Checkout, kind: kindBooking, id: b.ID})
		heap.Push(&l.exp, expiryItem{expiry: b.Expiry, id: b.ID})
	}
}

// Service 是可订日历与预订约束服务。所有公开操作可并发调用，
// 全局互斥锁把操作串行化，结果等价于某个串行顺序。
type Service struct {
	mu       sync.Mutex
	cfg      Config
	lastNow  int
	seq      int
	listings map[string]*listing
	bookings map[string]*Booking
}

// NewService 创建服务；H/P/Q 非法时返回参数错误。
func NewService(cfg Config) (*Service, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &Service{
		cfg:      cfg,
		listings: make(map[string]*listing),
		bookings: make(map[string]*Booking),
	}, nil
}

// LastNow 返回上一次被接受操作的 now。
func (s *Service) LastNow() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastNow
}

func (s *Service) nextID(prefix string) string {
	s.seq++
	return fmt.Sprintf("%s-%d", prefix, s.seq)
}

// clockAndListing 按时钟回退 -> 房源存在的次序检查。
func (s *Service) clockAndListing(now int, id string) (*listing, error) {
	if now < s.lastNow {
		return nil, errf(CodeClockRollback, "now=%d is before last accepted now=%d", now, s.lastNow)
	}
	l := s.listings[id]
	if l == nil {
		return nil, errf(CodeNotFound, "listing %q not found", id)
	}
	return l, nil
}

// CreateListing 创建房源。
func (s *Service) CreateListing(now int, id string, cfg ListingCfg) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < 0 || id == "" {
		return errf(CodeInvalidParams, "invalid now=%d or empty listing id", now)
	}
	if err := cfg.validate(); err != nil {
		return err
	}
	if now < s.lastNow {
		return errf(CodeClockRollback, "now=%d is before last accepted now=%d", now, s.lastNow)
	}
	if s.listings[id] != nil {
		return errf(CodeInvalidParams, "listing %q already exists", id)
	}
	s.listings[id] = &listing{
		id:       id,
		cfg:      cfg,
		cal:      newCalendar(cfg.DefaultMinStay, cfg.GapDays),
		bookings: make(map[string]*Booking),
		blocks:   make(map[string]*Block),
	}
	s.lastNow = now
	return nil
}

// SetDefaultMinStay 设置默认最短入住夜数。
func (s *Service) SetDefaultMinStay(now int, listingID string, nights int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < 0 || nights < 1 {
		return errf(CodeInvalidParams, "invalid now=%d or nights=%d", now, nights)
	}
	l, err := s.clockAndListing(now, listingID)
	if err != nil {
		return err
	}
	expired := l.detachExpired(now)
	l.cal.defaultMinStay = nights
	l.commitExpired(expired)
	s.lastNow = now
	return nil
}

// SetMinStay 设置某一天适用的最短入住夜数。
func (s *Service) SetMinStay(now int, listingID string, day, nights int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < 0 || day < 0 || nights < 1 {
		return errf(CodeInvalidParams, "invalid now=%d day=%d nights=%d", now, day, nights)
	}
	l, err := s.clockAndListing(now, listingID)
	if err != nil {
		return err
	}
	expired := l.detachExpired(now)
	l.cal.minStayByDay[day] = nights
	l.commitExpired(expired)
	s.lastNow = now
	return nil
}

// SetGapDays 设置换客间隙天数。
func (s *Service) SetGapDays(now int, listingID string, days int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < 0 || days < 0 {
		return errf(CodeInvalidParams, "invalid now=%d or days=%d", now, days)
	}
	l, err := s.clockAndListing(now, listingID)
	if err != nil {
		return err
	}
	expired := l.detachExpired(now)
	l.cal.gapDays = days
	l.commitExpired(expired)
	s.lastNow = now
	return nil
}

// AddBlock 新增封锁区间 [start, end)。与任何有效占用相交时被拒绝。
func (s *Service) AddBlock(now int, listingID string, start, end int) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < 0 || start < 0 || end <= start {
		return "", errf(CodeInvalidParams, "invalid now=%d or block range [%d,%d)", now, start, end)
	}
	l, err := s.clockAndListing(now, listingID)
	if err != nil {
		return "", err
	}
	expired := l.detachExpired(now)
	if e := l.cal.overlapErr(start, end); e != nil {
		l.rollbackExpired(expired)
		return "", e
	}
	id := s.nextID("BL")
	l.blocks[id] = &Block{ID: id, Start: start, End: end}
	l.cal.occ.insert(ivl{start: start, end: end, kind: kindBlock, id: id})
	l.commitExpired(expired)
	s.lastNow = now
	return id, nil
}

// ResizeBlock 缩短或延长已有封锁。缩短（含不变）总是允许；
// 延长只对新增部分 [newStart,newEnd) \ [oldStart,oldEnd) 重新判定冲突。
func (s *Service) ResizeBlock(now int, listingID, blockID string, newStart, newEnd int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < 0 || newStart < 0 || newEnd <= newStart {
		return errf(CodeInvalidParams, "invalid now=%d or block range [%d,%d)", now, newStart, newEnd)
	}
	l, err := s.clockAndListing(now, listingID)
	if err != nil {
		return err
	}
	bl := l.blocks[blockID]
	if bl == nil {
		return errf(CodeNotFound, "block %q not found", blockID)
	}
	expired := l.detachExpired(now)
	if newStart >= bl.Start && newEnd <= bl.End {
		// 缩短（或不变）：总是允许
		l.cal.occ.remove(bl.Start)
		bl.Start, bl.End = newStart, newEnd
		l.cal.occ.insert(ivl{start: newStart, end: newEnd, kind: kindBlock, id: bl.ID})
		l.commitExpired(expired)
		s.lastNow = now
		return nil
	}
	// 延长：先摘下旧区间，再检查新增部分与现有占用的冲突
	l.cal.occ.remove(bl.Start)
	var added [][2]int
	if s0, e0 := newStart, min(newEnd, bl.Start); s0 < e0 {
		added = append(added, [2]int{s0, e0})
	}
	if s0, e0 := max(newStart, bl.End), newEnd; s0 < e0 {
		added = append(added, [2]int{s0, e0})
	}
	for _, r := range added {
		if e := l.cal.overlapErr(r[0], r[1]); e != nil {
			l.cal.occ.insert(ivl{start: bl.Start, end: bl.End, kind: kindBlock, id: bl.ID})
			l.rollbackExpired(expired)
			return e
		}
	}
	bl.Start, bl.End = newStart, newEnd
	l.cal.occ.insert(ivl{start: newStart, end: newEnd, kind: kindBlock, id: bl.ID})
	l.commitExpired(expired)
	s.lastNow = now
	return nil
}

// CreateHold 创建保留：判定可订性，成功则占用日历，有效期至 now+H（含）。
func (s *Service) CreateHold(now int, listingID string, checkin, checkout int) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < 0 || checkout <= checkin || checkin < now {
		return "", errf(CodeInvalidParams, "invalid now=%d or stay [%d,%d)", now, checkin, checkout)
	}
	l, err := s.clockAndListing(now, listingID)
	if err != nil {
		return "", err
	}
	expired := l.detachExpired(now)
	if e := l.cal.judge(checkin, checkout); e != nil {
		l.rollbackExpired(expired)
		return "", e
	}
	id := s.nextID("BK")
	b := &Booking{
		ID:        id,
		ListingID: l.id,
		Checkin:   checkin,
		Checkout:  checkout,
		Expiry:    now + s.cfg.HoldWindow,
		Status:    StatusHold,
		Amount:    int64(checkout-checkin) * l.cfg.NightlyPrice,
	}
	l.bookings[id] = b
	s.bookings[id] = b
	l.cal.occ.insert(ivl{start: checkin, end: checkout, kind: kindBooking, id: id})
	heap.Push(&l.exp, expiryItem{expiry: b.Expiry, id: id})
	l.commitExpired(expired)
	s.lastNow = now
	return id, nil
}

// Pay 在保留有效期内付款，保留转为已确认。有效期最后一刻仍可付款。
func (s *Service) Pay(now int, bookingID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < 0 {
		return errf(CodeInvalidParams, "invalid now=%d", now)
	}
	if now < s.lastNow {
		return errf(CodeClockRollback, "now=%d is before last accepted now=%d", now, s.lastNow)
	}
	b := s.bookings[bookingID]
	if b == nil {
		return errf(CodeNotFound, "booking %q not found", bookingID)
	}
	if b.Status != StatusHold {
		return errf(CodeInvalidState, "booking %s is %s, not a pending hold", bookingID, b.Status)
	}
	if now > b.Expiry {
		return errf(CodeInvalidState, "hold %s expired at %d, now=%d", bookingID, b.Expiry, now)
	}
	l := s.listings[b.ListingID]
	expired := l.detachExpired(now)
	b.Status = StatusConfirmed
	l.commitExpired(expired)
	s.lastNow = now
	return nil
}

// Cancel 取消已确认预订，按取消日距入住日的天数分档退款并释放日历。
func (s *Service) Cancel(now int, bookingID string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < 0 {
		return 0, errf(CodeInvalidParams, "invalid now=%d", now)
	}
	if now < s.lastNow {
		return 0, errf(CodeClockRollback, "now=%d is before last accepted now=%d", now, s.lastNow)
	}
	b := s.bookings[bookingID]
	if b == nil {
		return 0, errf(CodeNotFound, "booking %q not found", bookingID)
	}
	if b.Status == StatusCancelled {
		return 0, errf(CodeInvalidState, "booking %s already cancelled", bookingID)
	}
	if b.Status != StatusConfirmed {
		return 0, errf(CodeInvalidState, "booking %s is %s, not confirmed", bookingID, b.Status)
	}
	if now >= b.Checkin {
		return 0, errf(CodeInvalidState, "booking %s check-in day %d has arrived, now=%d",
			bookingID, b.Checkin, now)
	}
	l := s.listings[b.ListingID]
	expired := l.detachExpired(now)
	var refund int64
	switch d := b.Checkin - now; {
	case d >= s.cfg.RefundFullDays:
		refund = b.Amount
	case d >= s.cfg.RefundHalfDays:
		refund = b.Amount / 2
	}
	b.Status = StatusCancelled
	b.Refund = refund
	l.cal.occ.remove(b.Checkin)
	l.commitExpired(expired)
	s.lastNow = now
	return refund, nil
}

// ModifyBooking 修改已确认预订的日期一次。在原预订释放后的日历上重新判定，
// 判定失败则原预订保持不变；成功不改变原付款，差价只记录不结算。
func (s *Service) ModifyBooking(now int, bookingID string, newCheckin, newCheckout int) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < 0 || newCheckout <= newCheckin || newCheckin < now {
		return 0, errf(CodeInvalidParams, "invalid now=%d or stay [%d,%d)", now, newCheckin, newCheckout)
	}
	if now < s.lastNow {
		return 0, errf(CodeClockRollback, "now=%d is before last accepted now=%d", now, s.lastNow)
	}
	b := s.bookings[bookingID]
	if b == nil {
		return 0, errf(CodeNotFound, "booking %q not found", bookingID)
	}
	if b.Status == StatusCancelled {
		return 0, errf(CodeInvalidState, "booking %s already cancelled", bookingID)
	}
	if b.Status != StatusConfirmed {
		return 0, errf(CodeInvalidState, "booking %s is %s, not confirmed", bookingID, b.Status)
	}
	if b.Modified {
		return 0, errf(CodeInvalidState, "booking %s already modified once", bookingID)
	}
	if now >= b.Checkin {
		return 0, errf(CodeInvalidState, "booking %s check-in day %d has arrived, now=%d",
			bookingID, b.Checkin, now)
	}
	l := s.listings[b.ListingID]
	expired := l.detachExpired(now)
	l.cal.occ.remove(b.Checkin)
	if e := l.cal.judge(newCheckin, newCheckout); e != nil {
		l.cal.occ.insert(ivl{start: b.Checkin, end: b.Checkout, kind: kindBooking, id: b.ID})
		l.rollbackExpired(expired)
		return 0, e
	}
	diff := int64(newCheckout-newCheckin)*l.cfg.NightlyPrice - b.Amount
	b.Checkin, b.Checkout = newCheckin, newCheckout
	b.PriceDiff = diff
	b.Modified = true
	l.cal.occ.insert(ivl{start: newCheckin, end: newCheckout, kind: kindBooking, id: b.ID})
	l.commitExpired(expired)
	s.lastNow = now
	return diff, nil
}

// CheckAvailability 查询 [checkin, checkout) 在 now 下是否可订。
// 查询是只读操作：校验时钟但不推进时钟，也不改变任何状态。
func (s *Service) CheckAvailability(now int, listingID string, checkin, checkout int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < 0 || checkout <= checkin || checkin < now {
		return errf(CodeInvalidParams, "invalid now=%d or stay [%d,%d)", now, checkin, checkout)
	}
	l, err := s.clockAndListing(now, listingID)
	if err != nil {
		return err
	}
	expired := l.detachExpired(now)
	defer l.rollbackExpired(expired)
	if e := l.cal.judge(checkin, checkout); e != nil {
		return e
	}
	return nil
}

// Snapshot 返回房源日历与预订的确定性快照。
func (s *Service) Snapshot(listingID string) (ListingSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	l := s.listings[listingID]
	if l == nil {
		return ListingSnapshot{}, errf(CodeNotFound, "listing %q not found", listingID)
	}
	snap := ListingSnapshot{LastNow: s.lastNow}
	for _, v := range l.cal.occ.inorder() {
		kind := "booking"
		if v.kind == kindBlock {
			kind = "block"
		}
		snap.Intervals = append(snap.Intervals, IntervalInfo{
			Start: v.start, End: v.end, Kind: kind, ID: v.id,
		})
	}
	for _, b := range l.bookings {
		snap.Bookings = append(snap.Bookings, BookingInfo{
			ID: b.ID, Checkin: b.Checkin, Checkout: b.Checkout, Expiry: b.Expiry,
			Status: b.Status, Amount: b.Amount, Refund: b.Refund,
			PriceDiff: b.PriceDiff, Modified: b.Modified,
		})
	}
	sort.Slice(snap.Bookings, func(i, j int) bool { return snap.Bookings[i].ID < snap.Bookings[j].ID })
	return snap, nil
}
