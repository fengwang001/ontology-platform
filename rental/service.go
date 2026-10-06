package rental

import "sync"

// Service 是短租房源的可订日历与预订约束服务。
// 所有操作可并发调用：内部互斥锁把操作串行化，
// 结果等价于某个串行顺序；同一夜不会被两笔有效预订占用。
//
// 时钟约定：每个变更操作携带整数日 now，不得小于上一次
// 被接受操作的 now，否则报时钟回退；被拒绝的操作不推进时钟，
// 也不改变日历与任何预订状态。只读查询不检查也不推进时钟。
type Service struct {
	mu       sync.Mutex
	h        int // 保留有效时刻单位数 H
	p        int // 全额退款的最少提前天数 P
	q        int // 半额退款的最少提前天数 Q
	lastNow  int
	hasNow   bool
	listings map[string]*listing
	nextID   int
}

// NewService 创建服务；holdTTL 为保留有效单位数 H，
// fullDays/halfDays 为退款阶梯的 P、Q，要求 P > Q >= 0、H >= 0。
func NewService(holdTTL, fullDays, halfDays int) (*Service, error) {
	if holdTTL < 0 || halfDays < 0 || fullDays <= halfDays {
		return nil, newErr(CodeInvalidParams, "非法的服务参数：要求 H>=0 且 P>Q>=0")
	}
	return &Service{
		h:        holdTTL,
		p:        fullDays,
		q:        halfDays,
		listings: make(map[string]*listing),
	}, nil
}

func (s *Service) checkClock(now int) *Error {
	if s.hasNow && now < s.lastNow {
		return newErr(CodeClockRollback, "时钟回退：now 小于上一次被接受操作的 now")
	}
	return nil
}

func (s *Service) commit(now int) {
	s.lastNow = now
	s.hasNow = true
}

func (s *Service) findListing(id string) (*listing, *Error) {
	l, ok := s.listings[id]
	if !ok {
		return nil, newErr(CodeNotFound, "房源不存在")
	}
	return l, nil
}

func (s *Service) findBooking(id string, bookingID int) (*listing, *Booking, *Error) {
	l, err := s.findListing(id)
	if err != nil {
		return nil, nil, err
	}
	b, ok := l.bookings[bookingID]
	if !ok {
		return nil, nil, newErr(CodeNotFound, "预订不存在")
	}
	return l, b, nil
}

// CreateListing 创建一份按整数日序号组织的空日历。
func (s *Service) CreateListing(id string, now int) error {
	if id == "" || now < 0 {
		return newErr(CodeInvalidParams, "房源 ID 为空或 now 为负")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return err
	}
	if _, ok := s.listings[id]; ok {
		return newErr(CodeInvalidParams, "房源已存在")
	}
	s.listings[id] = newListing()
	s.commit(now)
	return nil
}

// SetGap 设置换客间隙天数；为 0 时允许前一笔退房日等于后一笔入住日。
func (s *Service) SetGap(listingID string, days, now int) error {
	if days < 0 || now < 0 {
		return newErr(CodeInvalidParams, "间隙天数或 now 非法")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return err
	}
	l, err := s.findListing(listingID)
	if err != nil {
		return err
	}
	l.gap = days
	s.commit(now)
	return nil
}

// SetMinStay 按日设置最短入住夜数；一笔预订须满足其入住日所适用的值。
func (s *Service) SetMinStay(listingID string, day, nights, now int) error {
	if day < 0 || nights < 1 || now < 0 {
		return newErr(CodeInvalidParams, "日期、夜数或 now 非法")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return err
	}
	l, err := s.findListing(listingID)
	if err != nil {
		return err
	}
	l.minStay[day] = nights
	l.maxMinStay = 1
	for _, v := range l.minStay {
		if v > l.maxMinStay {
			l.maxMinStay = v
		}
	}
	s.commit(now)
	return nil
}

// AddBlock 添加封锁区间 [start, end)；与已有有效预订冲突或与已有
// 封锁重叠时被拒绝，且不改变日历。
func (s *Service) AddBlock(listingID string, start, end, now int) (int, error) {
	if start < 0 || end <= start || now < 0 {
		return -1, newErr(CodeInvalidParams, "封锁区间或 now 非法")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return -1, err
	}
	l, err := s.findListing(listingID)
	if err != nil {
		return -1, err
	}
	for d := start; d < end; d++ {
		if r, ok := l.occAt(d); ok && r.kind == occBlock {
			return -1, newErr(CodeBlocked, "与已有封锁重叠")
		}
	}
	for d := start; d < end; d++ {
		if r, ok := l.occAt(d); ok && r.kind == occBooking && l.activeBooking(r.id, now, -1) {
			return -1, newErr(CodeConflict, "封锁与已有预订冲突")
		}
	}
	id := s.nextID
	s.nextID++
	l.blocks[id] = &block{id: id, start: start, end: end}
	l.occupy(start, end, occRef{kind: occBlock, id: id})
	s.commit(now)
	return id, nil
}

// ModifyBlock 缩短或延长已有封锁。缩短（新区间不超出旧区间）总是
// 允许；延长只对新增部分重新判定与预订、其他封锁的冲突。
func (s *Service) ModifyBlock(listingID string, blockID, start, end, now int) error {
	if start < 0 || end <= start || now < 0 {
		return newErr(CodeInvalidParams, "封锁区间或 now 非法")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return err
	}
	l, err := s.findListing(listingID)
	if err != nil {
		return err
	}
	blk, ok := l.blocks[blockID]
	if !ok {
		return newErr(CodeNotFound, "封锁不存在")
	}
	added := func(d int) bool { return d < blk.start || d >= blk.end }
	for d := start; d < end; d++ {
		if added(d) {
			if r, ok := l.occAt(d); ok && r.kind == occBlock {
				return newErr(CodeBlocked, "延长部分与已有封锁重叠")
			}
		}
	}
	for d := start; d < end; d++ {
		if added(d) {
			if r, ok := l.occAt(d); ok && r.kind == occBooking && l.activeBooking(r.id, now, -1) {
				return newErr(CodeConflict, "延长部分与已有预订冲突")
			}
		}
	}
	l.release(blk.start, blk.end, occRef{kind: occBlock, id: blockID})
	blk.start, blk.end = start, end
	l.occupy(start, end, occRef{kind: occBlock, id: blockID})
	s.commit(now)
	return nil
}

// Hold 创建保留：先通过可订性判定，随后在 H 个时刻单位内有效，
// 期间占用日历但未付款；now <= 创建时刻+H 均可付款。
func (s *Service) Hold(listingID string, cin, cout, now int) (int, error) {
	if cin < 0 || cout <= cin || now < 0 {
		return -1, newErr(CodeInvalidParams, "入住/退房日或 now 非法")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return -1, err
	}
	l, err := s.findListing(listingID)
	if err != nil {
		return -1, err
	}
	if err := l.check(cin, cout, now, -1); err != nil {
		return -1, err
	}
	id := s.nextID
	s.nextID++
	l.bookings[id] = &Booking{
		ID:        id,
		ListingID: listingID,
		Checkin:   cin,
		Checkout:  cout,
		State:     StateHold,
		ExpiresAt: now + s.h,
	}
	l.occupy(cin, cout, occRef{kind: occBooking, id: id})
	s.commit(now)
	return id, nil
}

// Pay 在保留有效期内付款，保留转为已确认；最后一刻仍可付款，
// 超过有效期则报状态错误，保留自动失效并释放日历。
func (s *Service) Pay(listingID string, bookingID, now int) error {
	if now < 0 {
		return newErr(CodeInvalidParams, "now 非法")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return err
	}
	_, b, err := s.findBooking(listingID, bookingID)
	if err != nil {
		return err
	}
	switch {
	case b.State == StateCancelled:
		return newErr(CodeInvalidState, "预订已取消")
	case b.State == StateConfirmed:
		return newErr(CodeInvalidState, "预订已确认，无需重复付款")
	case now > b.ExpiresAt:
		return newErr(CodeInvalidState, "保留已失效")
	}
	b.State = StateConfirmed
	s.commit(now)
	return nil
}

// Cancel 取消已确认预订并按阶梯退款；入住日当天及之后不可取消。
// 取消后释放的日期立即可订。
func (s *Service) Cancel(listingID string, bookingID, now int) (Refund, error) {
	if now < 0 {
		return RefundNone, newErr(CodeInvalidParams, "now 非法")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return RefundNone, err
	}
	l, b, err := s.findBooking(listingID, bookingID)
	if err != nil {
		return RefundNone, err
	}
	if b.State != StateConfirmed {
		return RefundNone, newErr(CodeInvalidState, "仅已确认预订可取消")
	}
	if now >= b.Checkin {
		return RefundNone, newErr(CodeInvalidState, "入住日已到，不可取消")
	}
	refund := refundTier(b.Checkin-now, s.p, s.q)
	b.State = StateCancelled
	l.release(b.Checkin, b.Checkout, occRef{kind: occBooking, id: bookingID})
	s.commit(now)
	return refund, nil
}

// Modify 修改已确认预订的日期，仅允许一次。修改视为在原预订
// 释放后的日历上重新判定可订性；判定失败时原预订保持不变，
// 也不消耗修改次数。成功不改变付款，夜数差价只记录不结算。
func (s *Service) Modify(listingID string, bookingID, cin, cout, now int) error {
	if cin < 0 || cout <= cin || now < 0 {
		return newErr(CodeInvalidParams, "入住/退房日或 now 非法")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return err
	}
	l, b, err := s.findBooking(listingID, bookingID)
	if err != nil {
		return err
	}
	if b.State != StateConfirmed {
		return newErr(CodeInvalidState, "仅已确认预订可修改")
	}
	if b.Modified {
		return newErr(CodeInvalidState, "预订已修改过，不可再次修改")
	}
	if now >= b.Checkin {
		return newErr(CodeInvalidState, "入住日已到，不可修改")
	}
	if err := l.check(cin, cout, now, bookingID); err != nil {
		return err
	}
	l.release(b.Checkin, b.Checkout, occRef{kind: occBooking, id: bookingID})
	b.NightDiff = (cout - cin) - (b.Checkout - b.Checkin)
	b.Checkin, b.Checkout = cin, cout
	b.Modified = true
	l.occupy(cin, cout, occRef{kind: occBooking, id: bookingID})
	s.commit(now)
	return nil
}

// CheckAvailability 是只读查询：按 now 下应有的状态（含保留失效）
// 判定可订性，返回 nil 表示可订，否则返回首要不可订原因。
// 查询不检查也不推进时钟，不改变任何状态。
func (s *Service) CheckAvailability(listingID string, cin, cout, now int) error {
	if cin < 0 || cout <= cin || now < 0 {
		return newErr(CodeInvalidParams, "入住/退房日或 now 非法")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	l, err := s.findListing(listingID)
	if err != nil {
		return err
	}
	return l.check(cin, cout, now, -1)
}
