package calendar

// 朴素模型：独立编写的对照实现，只用全量线性扫描，不用任何索引、
// 有界扫描或惰性清理，用于随机操作序列的差分测试。
// 语义与生产实现保持一致：相同的错误次序、退款阶梯、有效期与孤夜规则。

type nStatus int

const (
	nHold nStatus = iota + 1
	nConfirmed
	nCancelled
)

type nBooking struct {
	id        int64
	listing   string
	in, out   int64
	expires   int64
	status    nStatus
	modified  bool
	amount    int64
	priceDiff int64
}

type nBlock struct{ start, end int64 }

type nListing struct {
	price      int64
	minDefault int64
	gap        int64
	overrides  map[int64]int64
	blocks     map[int64]nBlock
}

func (l *nListing) minStayAt(day int64) int64 {
	if v, ok := l.overrides[day]; ok {
		return v
	}
	return l.minDefault
}

type naive struct {
	holdUnits, fullDays, halfDays int64
	listings                      map[string]*nListing
	bookings                      map[int64]*nBooking
	nextBooking                   int64
	nextBlock                     int64
	lastNow                       int64
	hasNow                        bool
}

func newNaive(holdUnits, fullDays, halfDays int64) *naive {
	return &naive{
		holdUnits:   holdUnits,
		fullDays:    fullDays,
		halfDays:    halfDays,
		listings:    map[string]*nListing{},
		bookings:    map[int64]*nBooking{},
		nextBooking: 1,
		nextBlock:   1,
	}
}

func (m *naive) checkClock(now int64) *Error {
	if m.hasNow && now < m.lastNow {
		return errClockRollback(now, m.lastNow)
	}
	return nil
}

func (m *naive) commit(now int64) { m.lastNow, m.hasNow = now, true }

func (m *naive) active(b *nBooking, now, exclude int64) bool {
	if b.id == exclude {
		return false
	}
	return b.status == nConfirmed || (b.status == nHold && now <= b.expires)
}

// avail 用全量扫描判定可订性，检查次序与生产实现一致。
func (m *naive) avail(l *nListing, listingID string, now, in, out, exclude int64) *Error {
	// 1. 封锁
	for _, blk := range l.blocks {
		if blk.start < out && in < blk.end {
			return errNotBookable(ReasonBlocked, "日期 [%d,%d) 与封锁 [%d,%d) 相交", in, out, blk.start, blk.end)
		}
	}
	// 2. 冲突，同时找出前后最近的占用夜
	prevOcc, nextOcc := int64(0), int64(0)
	hasPrev, hasNext := false, false
	for _, b := range m.bookings {
		if b.listing != listingID || !m.active(b, now, exclude) {
			continue
		}
		if b.in < out && in < b.out {
			return errNotBookable(ReasonBookingConflict, "与预订 %d [%d,%d) 相交", b.id, b.in, b.out)
		}
		if b.out <= in && (!hasPrev || b.out-1 > prevOcc) {
			prevOcc, hasPrev = b.out-1, true
		}
		if b.in >= out && (!hasNext || b.in < nextOcc) {
			nextOcc, hasNext = b.in, true
		}
	}
	// 前后最近的封锁夜
	prevBlk, nextBlk := int64(0), int64(0)
	hasPrevBlk, hasNextBlk := false, false
	for _, blk := range l.blocks {
		if blk.end <= in && (!hasPrevBlk || blk.end-1 > prevBlk) {
			prevBlk, hasPrevBlk = blk.end-1, true
		}
		if blk.start >= out && (!hasNextBlk || blk.start < nextBlk) {
			nextBlk, hasNextBlk = blk.start, true
		}
	}
	// 封锁比占用更近时，该侧空档豁免（与封锁相邻）
	prevIsOcc := hasPrev && (!hasPrevBlk || prevOcc > prevBlk)
	nextIsOcc := hasNext && (!hasNextBlk || nextOcc < nextBlk)
	// 3. 换客间隙
	if prevIsOcc && in-prevOcc-1 < l.gap {
		return errNotBookable(ReasonGapTooSmall, "前侧空 %d 天 < 间隙 %d", in-prevOcc-1, l.gap)
	}
	if nextIsOcc && nextOcc-out < l.gap {
		return errNotBookable(ReasonGapTooSmall, "后侧空 %d 天 < 间隙 %d", nextOcc-out, l.gap)
	}
	// 4. 最短入住（入住日取值）
	if out-in < l.minStayAt(in) {
		return errNotBookable(ReasonMinStayTooShort, "%d 夜 < 最短入住 %d", out-in, l.minStayAt(in))
	}
	// 5. 孤夜
	if prevIsOcc {
		if o := in - prevOcc - 1 - l.gap; o > 0 && o < l.minStayAt(prevOcc+1) {
			return errNotBookable(ReasonOrphanNight, "前侧孤夜 %d", o)
		}
	}
	if nextIsOcc {
		if o := nextOcc - out - l.gap; o > 0 && o < l.minStayAt(out) {
			return errNotBookable(ReasonOrphanNight, "后侧孤夜 %d", o)
		}
	}
	return nil
}

func (m *naive) addListing(now int64, id string, price, minStay, gap int64) error {
	if id == "" {
		return errInvalidParam("房源 ID 不能为空")
	}
	if price < 0 {
		return errInvalidParam("每晚价格不能为负，got %d", price)
	}
	if minStay < 1 {
		return errInvalidParam("最短入住必须 >= 1，got %d", minStay)
	}
	if gap < 0 {
		return errInvalidParam("换客间隙不能为负，got %d", gap)
	}
	if _, dup := m.listings[id]; dup {
		return errInvalidParam("房源 %q 已存在", id)
	}
	if err := m.checkClock(now); err != nil {
		return err
	}
	m.listings[id] = &nListing{price: price, minDefault: minStay, gap: gap,
		overrides: map[int64]int64{}, blocks: map[int64]nBlock{}}
	m.commit(now)
	return nil
}

func (m *naive) setMinStayDefault(now int64, listingID string, nights int64) error {
	if nights < 1 {
		return errInvalidParam("最短入住必须 >= 1，got %d", nights)
	}
	if err := m.checkClock(now); err != nil {
		return err
	}
	l := m.listings[listingID]
	if l == nil {
		return errNotFound("房源 %q 不存在", listingID)
	}
	l.minDefault = nights
	m.commit(now)
	return nil
}

func (m *naive) setMinStayForDay(now int64, listingID string, day, nights int64) error {
	if nights < 1 {
		return errInvalidParam("最短入住必须 >= 1，got %d", nights)
	}
	if err := m.checkClock(now); err != nil {
		return err
	}
	l := m.listings[listingID]
	if l == nil {
		return errNotFound("房源 %q 不存在", listingID)
	}
	l.overrides[day] = nights
	m.commit(now)
	return nil
}

func (m *naive) setGap(now int64, listingID string, days int64) error {
	if days < 0 {
		return errInvalidParam("换客间隙不能为负，got %d", days)
	}
	if err := m.checkClock(now); err != nil {
		return err
	}
	l := m.listings[listingID]
	if l == nil {
		return errNotFound("房源 %q 不存在", listingID)
	}
	l.gap = days
	m.commit(now)
	return nil
}

func (m *naive) addBlock(now int64, listingID string, start, end int64) (int64, error) {
	if end <= start {
		return 0, errInvalidParam("封锁区间需满足 start < end，got [%d,%d)", start, end)
	}
	if err := m.checkClock(now); err != nil {
		return 0, err
	}
	l := m.listings[listingID]
	if l == nil {
		return 0, errNotFound("房源 %q 不存在", listingID)
	}
	for _, b := range m.bookings {
		if b.listing == listingID && m.active(b, now, 0) && b.in < end && start < b.out {
			return 0, errNotBookable(ReasonBookingConflict, "封锁 [%d,%d) 与预订 %d 冲突", start, end, b.id)
		}
	}
	id := m.nextBlock
	m.nextBlock++
	l.blocks[id] = nBlock{start: start, end: end}
	m.commit(now)
	return id, nil
}

func (m *naive) modifyBlock(now int64, listingID string, blockID, newStart, newEnd int64) error {
	if blockID <= 0 {
		return errInvalidParam("封锁 ID 必须为正，got %d", blockID)
	}
	if newEnd <= newStart {
		return errInvalidParam("封锁区间需满足 start < end，got [%d,%d)", newStart, newEnd)
	}
	if err := m.checkClock(now); err != nil {
		return err
	}
	l := m.listings[listingID]
	if l == nil {
		return errNotFound("房源 %q 不存在", listingID)
	}
	old, ok := l.blocks[blockID]
	if !ok {
		return errNotFound("房源 %q 上封锁 %d 不存在", listingID, blockID)
	}
	shorten := newStart >= old.start && newEnd <= old.end
	if !shorten {
		for _, b := range m.bookings {
			if b.listing == listingID && m.active(b, now, 0) && b.in < newEnd && newStart < b.out {
				return errNotBookable(ReasonBookingConflict, "封锁延长与预订 %d 冲突", b.id)
			}
		}
	}
	l.blocks[blockID] = nBlock{start: newStart, end: newEnd}
	m.commit(now)
	return nil
}

func (m *naive) hold(now int64, listingID string, in, out int64) (int64, error) {
	if listingID == "" {
		return 0, errInvalidParam("房源 ID 不能为空")
	}
	if out <= in {
		return 0, errInvalidParam("退房日必须大于入住日，got [%d,%d)", in, out)
	}
	if in < now {
		return 0, errInvalidParam("入住日 %d 早于当前时刻 %d", in, now)
	}
	if err := m.checkClock(now); err != nil {
		return 0, err
	}
	l := m.listings[listingID]
	if l == nil {
		return 0, errNotFound("房源 %q 不存在", listingID)
	}
	if err := m.avail(l, listingID, now, in, out, 0); err != nil {
		return 0, err
	}
	id := m.nextBooking
	m.nextBooking++
	m.bookings[id] = &nBooking{
		id: id, listing: listingID, in: in, out: out,
		expires: now + m.holdUnits, status: nHold, amount: (out - in) * l.price,
	}
	m.commit(now)
	return id, nil
}

func (m *naive) pay(now, bookingID int64) error {
	if bookingID <= 0 {
		return errInvalidParam("预订 ID 必须为正，got %d", bookingID)
	}
	if err := m.checkClock(now); err != nil {
		return err
	}
	b := m.bookings[bookingID]
	if b == nil {
		return errNotFound("预订 %d 不存在", bookingID)
	}
	switch b.status {
	case nHold:
		if now > b.expires {
			return errInvalidState("保留 %d 已失效", bookingID)
		}
		b.status = nConfirmed
		m.commit(now)
		return nil
	case nConfirmed:
		return errInvalidState("预订 %d 已确认，不能重复支付", bookingID)
	default:
		return errInvalidState("预订 %d 已取消，不能支付", bookingID)
	}
}

func (m *naive) cancel(now, bookingID int64) (int64, error) {
	if bookingID <= 0 {
		return 0, errInvalidParam("预订 ID 必须为正，got %d", bookingID)
	}
	if err := m.checkClock(now); err != nil {
		return 0, err
	}
	b := m.bookings[bookingID]
	if b == nil {
		return 0, errNotFound("预订 %d 不存在", bookingID)
	}
	if b.status != nConfirmed {
		return 0, errInvalidState("预订 %d 仅已确认预订可取消", bookingID)
	}
	if now >= b.in {
		return 0, errInvalidState("入住日 %d 已到，不可取消", b.in)
	}
	d := b.in - now
	var refund int64
	switch {
	case d >= m.fullDays:
		refund = b.amount
	case d >= m.halfDays:
		refund = b.amount / 2
	}
	b.status = nCancelled
	m.commit(now)
	return refund, nil
}

func (m *naive) modifyBooking(now, bookingID, newIn, newOut int64) error {
	if bookingID <= 0 {
		return errInvalidParam("预订 ID 必须为正，got %d", bookingID)
	}
	if newOut <= newIn {
		return errInvalidParam("退房日必须大于入住日，got [%d,%d)", newIn, newOut)
	}
	if newIn < now {
		return errInvalidParam("入住日 %d 早于当前时刻 %d", newIn, now)
	}
	if err := m.checkClock(now); err != nil {
		return err
	}
	b := m.bookings[bookingID]
	if b == nil {
		return errNotFound("预订 %d 不存在", bookingID)
	}
	if b.status != nConfirmed {
		return errInvalidState("预订 %d 仅已确认预订可修改", bookingID)
	}
	if b.modified {
		return errInvalidState("预订 %d 已修改过一次", bookingID)
	}
	if now >= b.in {
		return errInvalidState("入住日 %d 已到，不可修改", b.in)
	}
	l := m.listings[b.listing]
	if err := m.avail(l, b.listing, now, newIn, newOut, bookingID); err != nil {
		return err
	}
	b.priceDiff = (newOut-newIn)*l.price - b.amount
	b.in, b.out = newIn, newOut
	b.modified = true
	m.commit(now)
	return nil
}

func (m *naive) checkAvailability(now int64, listingID string, in, out int64) (bool, *Error) {
	if listingID == "" {
		return false, errInvalidParam("房源 ID 不能为空")
	}
	if out <= in {
		return false, errInvalidParam("退房日必须大于入住日，got [%d,%d)", in, out)
	}
	if in < now {
		return false, errInvalidParam("入住日 %d 早于当前时刻 %d", in, now)
	}
	if err := m.checkClock(now); err != nil {
		return false, err
	}
	l := m.listings[listingID]
	if l == nil {
		return false, errNotFound("房源 %q 不存在", listingID)
	}
	if err := m.avail(l, listingID, now, in, out, 0); err != nil {
		return false, err
	}
	return true, nil
}
