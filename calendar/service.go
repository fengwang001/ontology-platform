package calendar

import "sync"

// Service 是短租房源的可订日历与预订约束服务。
//
// 并发：所有方法共用一把互斥锁，任何并发调用的结果都等价于某个串行顺序。
// 时钟：每个操作携带整数日 now；只有被接受的操作才推进时钟，
// 被拒绝的操作不改变日历、预订状态与时钟。
type Service struct {
	mu sync.Mutex

	holdUnits int64 // H：保留有效期（时刻单位数），now <= CreatedAt+H 内可付款
	fullDays  int64 // P：取消日距入住日不少于 P 天全额退款
	halfDays  int64 // Q：不少于 Q 天且少于 P 天退一半

	listings  map[string]*Listing
	bookings  map[int64]*Booking
	holdQueue []int64 // 保留 ID 按创建顺序排列；now 单调 => 过期时间单调

	nextBooking int64
	nextBlock   int64

	lastNow int64
	hasNow  bool
}

// NewService 创建服务。H 为保留有效期，P/Q 为取消退款阶梯的两档天数。
func NewService(holdUnits, fullRefundDays, halfRefundDays int64) (*Service, error) {
	if holdUnits < 1 {
		return nil, errInvalidParam("保留有效期 H 必须 >= 1，got %d", holdUnits)
	}
	if halfRefundDays < 0 || fullRefundDays < halfRefundDays {
		return nil, errInvalidParam("退款阶梯需满足 P >= Q >= 0，got P=%d Q=%d", fullRefundDays, halfRefundDays)
	}
	return &Service{
		holdUnits:   holdUnits,
		fullDays:    fullRefundDays,
		halfDays:    halfRefundDays,
		listings:    map[string]*Listing{},
		bookings:    map[int64]*Booking{},
		nextBooking: 1,
		nextBlock:   1,
	}, nil
}

// checkClock 在参数校验之后调用：now 不得小于上一次被接受操作的 now。
func (s *Service) checkClock(now int64) *Error {
	if s.hasNow && now < s.lastNow {
		return errClockRollback(now, s.lastNow)
	}
	return nil
}

// commit 在操作被接受后调用：推进时钟，并失效所有在 now 下已过期的保留。
// 由于 now 单调不减，保留的过期时间也单调，队首未过期则后续都未过期。
func (s *Service) commit(now int64) {
	s.lastNow = now
	s.hasNow = true
	for len(s.holdQueue) > 0 {
		b := s.bookings[s.holdQueue[0]]
		if b.Status == StatusHold && now <= b.ExpiresAt {
			break
		}
		if b.Status == StatusHold {
			b.Status = StatusExpired
			l := s.listings[b.ListingID]
			for n := b.CheckIn; n < b.CheckOut; n++ {
				// 该夜可能已被后续预订占用（本保留过期后释放出再被订走）
				if l.occupied[n] == b.ID {
					delete(l.occupied, n)
				}
			}
		}
		s.holdQueue = s.holdQueue[1:]
	}
}

// AddListing 注册房源，设定默认最短入住与换客间隙。
func (s *Service) AddListing(now int64, id string, nightlyPrice, minStayDefault, gap int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == "" {
		return errInvalidParam("房源 ID 不能为空")
	}
	if nightlyPrice < 0 {
		return errInvalidParam("每晚价格不能为负，got %d", nightlyPrice)
	}
	if minStayDefault < 1 {
		return errInvalidParam("最短入住必须 >= 1，got %d", minStayDefault)
	}
	if gap < 0 {
		return errInvalidParam("换客间隙不能为负，got %d", gap)
	}
	if _, dup := s.listings[id]; dup {
		return errInvalidParam("房源 %q 已存在", id)
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	s.listings[id] = newListing(id, nightlyPrice, minStayDefault, gap)
	s.commit(now)
	return nil
}

// SetMinStayDefault 修改默认最短入住（只影响之后的判定）。
func (s *Service) SetMinStayDefault(now int64, listingID string, nights int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if nights < 1 {
		return errInvalidParam("最短入住必须 >= 1，got %d", nights)
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	l := s.listings[listingID]
	if l == nil {
		return errNotFound("房源 %q 不存在", listingID)
	}
	l.MinStayDefault = nights
	l.recomputeMaxMinStay()
	s.commit(now)
	return nil
}

// SetMinStayForDay 设置某一日适用的最短入住（按入住日取值）。
func (s *Service) SetMinStayForDay(now int64, listingID string, day, nights int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if nights < 1 {
		return errInvalidParam("最短入住必须 >= 1，got %d", nights)
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	l := s.listings[listingID]
	if l == nil {
		return errNotFound("房源 %q 不存在", listingID)
	}
	l.minStayByDay[day] = nights
	l.recomputeMaxMinStay()
	s.commit(now)
	return nil
}

// SetGap 修改换客间隙天数（只影响之后的判定）。
func (s *Service) SetGap(now int64, listingID string, days int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if days < 0 {
		return errInvalidParam("换客间隙不能为负，got %d", days)
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	l := s.listings[listingID]
	if l == nil {
		return errNotFound("房源 %q 不存在", listingID)
	}
	l.Gap = days
	s.commit(now)
	return nil
}

// AddBlock 新增封锁区间 [start, end)。与任何有效预订冲突时拒绝。
func (s *Service) AddBlock(now int64, listingID string, start, end int64) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if end <= start {
		return 0, errInvalidParam("封锁区间需满足 start < end，got [%d,%d)", start, end)
	}
	if err := s.checkClock(now); err != nil {
		return 0, err
	}
	l := s.listings[listingID]
	if l == nil {
		return 0, errNotFound("房源 %q 不存在", listingID)
	}
	for n := start; n < end; n++ {
		if s.activeOccupant(l, n, now, 0) {
			return 0, errNotBookable(ReasonBookingConflict, "封锁 [%d,%d) 与夜 %d 上的预订冲突", start, end, n)
		}
	}
	id := s.nextBlock
	s.nextBlock++
	l.blocks[id] = &Block{ID: id, Start: start, End: end}
	l.blockOrder = append(l.blockOrder, id)
	s.commit(now)
	return id, nil
}

// ModifyBlock 缩短或延长已有封锁。缩短总是允许；延长按新增部分重新判定冲突
// （不变量保证旧区间内无有效预订，故直接检查整个新区间即可）。
func (s *Service) ModifyBlock(now int64, listingID string, blockID, newStart, newEnd int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if blockID <= 0 {
		return errInvalidParam("封锁 ID 必须为正，got %d", blockID)
	}
	if newEnd <= newStart {
		return errInvalidParam("封锁区间需满足 start < end，got [%d,%d)", newStart, newEnd)
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	l := s.listings[listingID]
	if l == nil {
		return errNotFound("房源 %q 不存在", listingID)
	}
	b := l.blocks[blockID]
	if b == nil {
		return errNotFound("房源 %q 上封锁 %d 不存在", listingID, blockID)
	}
	shorten := newStart >= b.Start && newEnd <= b.End
	if !shorten {
		for n := newStart; n < newEnd; n++ {
			if s.activeOccupant(l, n, now, 0) {
				return errNotBookable(ReasonBookingConflict,
					"封锁延长到 [%d,%d) 与夜 %d 上的预订冲突", newStart, newEnd, n)
			}
		}
	}
	b.Start, b.End = newStart, newEnd
	s.commit(now)
	return nil
}

// Hold 创建保留：先判定可订性，成功后占用日历，now+H 内可付款。
func (s *Service) Hold(now int64, listingID string, checkIn, checkOut int64) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if listingID == "" {
		return 0, errInvalidParam("房源 ID 不能为空")
	}
	if checkOut <= checkIn {
		return 0, errInvalidParam("退房日必须大于入住日，got [%d,%d)", checkIn, checkOut)
	}
	if checkIn < now {
		return 0, errInvalidParam("入住日 %d 早于当前时刻 %d", checkIn, now)
	}
	if err := s.checkClock(now); err != nil {
		return 0, err
	}
	l := s.listings[listingID]
	if l == nil {
		return 0, errNotFound("房源 %q 不存在", listingID)
	}
	if err := s.checkAvailability(l, now, checkIn, checkOut, 0); err != nil {
		return 0, err
	}
	id := s.nextBooking
	s.nextBooking++
	s.bookings[id] = &Booking{
		ID:        id,
		ListingID: listingID,
		CheckIn:   checkIn,
		CheckOut:  checkOut,
		CreatedAt: now,
		ExpiresAt: now + s.holdUnits,
		Status:    StatusHold,
		Amount:    (checkOut - checkIn) * l.NightlyPrice,
	}
	s.holdQueue = append(s.holdQueue, id)
	for n := checkIn; n < checkOut; n++ {
		l.occupied[n] = id
	}
	s.commit(now)
	return id, nil
}

// Pay 在保留有效期内付款，转为已确认。有效期最后一刻仍可付款。
func (s *Service) Pay(now int64, bookingID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if bookingID <= 0 {
		return errInvalidParam("预订 ID 必须为正，got %d", bookingID)
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	b := s.bookings[bookingID]
	if b == nil {
		return errNotFound("预订 %d 不存在", bookingID)
	}
	switch b.Status {
	case StatusHold:
		if now > b.ExpiresAt {
			return errInvalidState("保留 %d 已失效（有效期至 %d，当前 %d）", bookingID, b.ExpiresAt, now)
		}
		b.Status = StatusConfirmed
		s.commit(now)
		return nil
	case StatusConfirmed:
		return errInvalidState("预订 %d 已确认，不能重复支付", bookingID)
	default:
		return errInvalidState("预订 %d 状态为 %s，不能支付", bookingID, b.Status)
	}
}

// Cancel 取消已确认预订，按取消日距入住日的天数分档退款，并立即释放日期。
func (s *Service) Cancel(now int64, bookingID int64) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if bookingID <= 0 {
		return 0, errInvalidParam("预订 ID 必须为正，got %d", bookingID)
	}
	if err := s.checkClock(now); err != nil {
		return 0, err
	}
	b := s.bookings[bookingID]
	if b == nil {
		return 0, errNotFound("预订 %d 不存在", bookingID)
	}
	if b.Status != StatusConfirmed {
		return 0, errInvalidState("预订 %d 状态为 %s，仅已确认预订可取消", bookingID, b.Status)
	}
	if now >= b.CheckIn {
		return 0, errInvalidState("入住日 %d 已到（当前 %d），不可取消", b.CheckIn, now)
	}
	d := b.CheckIn - now
	var refund int64
	switch {
	case d >= s.fullDays:
		refund = b.Amount
	case d >= s.halfDays:
		refund = b.Amount / 2
	}
	b.Status = StatusCancelled
	l := s.listings[b.ListingID]
	for n := b.CheckIn; n < b.CheckOut; n++ {
		delete(l.occupied, n)
	}
	s.commit(now)
	return refund, nil
}

// ModifyBooking 修改已确认预订的日期（仅一次）。在原预订释放后的日历上重新判定，
// 判定失败则原预订保持不变；成功不改变原付款，差价只记录不结算。
func (s *Service) ModifyBooking(now int64, bookingID, newCheckIn, newCheckOut int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if bookingID <= 0 {
		return errInvalidParam("预订 ID 必须为正，got %d", bookingID)
	}
	if newCheckOut <= newCheckIn {
		return errInvalidParam("退房日必须大于入住日，got [%d,%d)", newCheckIn, newCheckOut)
	}
	if newCheckIn < now {
		return errInvalidParam("入住日 %d 早于当前时刻 %d", newCheckIn, now)
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	b := s.bookings[bookingID]
	if b == nil {
		return errNotFound("预订 %d 不存在", bookingID)
	}
	if b.Status != StatusConfirmed {
		return errInvalidState("预订 %d 状态为 %s，仅已确认预订可修改", bookingID, b.Status)
	}
	if b.Modified {
		return errInvalidState("预订 %d 已修改过一次，不能再次修改", bookingID)
	}
	if now >= b.CheckIn {
		return errInvalidState("入住日 %d 已到（当前 %d），不可修改", b.CheckIn, now)
	}
	l := s.listings[b.ListingID]
	if err := s.checkAvailability(l, now, newCheckIn, newCheckOut, bookingID); err != nil {
		return err
	}
	for n := b.CheckIn; n < b.CheckOut; n++ {
		delete(l.occupied, n)
	}
	b.PriceDiff = (newCheckOut-newCheckIn)*l.NightlyPrice - b.Amount
	b.CheckIn, b.CheckOut = newCheckIn, newCheckOut
	b.Modified = true
	for n := newCheckIn; n < newCheckOut; n++ {
		l.occupied[n] = bookingID
	}
	s.commit(now)
	return nil
}

// CheckAvailability 查询 [checkIn, checkOut) 在 now 时刻是否可订。
// 只读：反映 now 下应有的状态（含保留自动失效），但不推进时钟、不改变状态。
func (s *Service) CheckAvailability(now int64, listingID string, checkIn, checkOut int64) (bool, *Error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if listingID == "" {
		return false, errInvalidParam("房源 ID 不能为空")
	}
	if checkOut <= checkIn {
		return false, errInvalidParam("退房日必须大于入住日，got [%d,%d)", checkIn, checkOut)
	}
	if checkIn < now {
		return false, errInvalidParam("入住日 %d 早于当前时刻 %d", checkIn, now)
	}
	if err := s.checkClock(now); err != nil {
		return false, err
	}
	l := s.listings[listingID]
	if l == nil {
		return false, errNotFound("房源 %q 不存在", listingID)
	}
	if err := s.checkAvailability(l, now, checkIn, checkOut, 0); err != nil {
		return false, err
	}
	return true, nil
}

// GetBooking 返回预订的快照，供调用方与测试检查状态。
func (s *Service) GetBooking(bookingID int64) (Booking, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b := s.bookings[bookingID]
	if b == nil {
		return Booking{}, false
	}
	return *b, true
}
