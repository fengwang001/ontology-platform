package booking

// 本文件是一份独立编写的朴素参考模型：用切片线性扫描实现与 Service
// 相同的语义，用于随机操作序列的差分对照。模型不共享生产代码的任何
// 数据结构，刻意保持朴素（O(n) 扫描），以交叉验证优化实现的正确性。

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

type mBooking struct {
	id                string
	ci, co, expiry    int
	status            Status
	amount            int64
	refund, priceDiff int64
	modified          bool
}

type mBlock struct {
	id         string
	start, end int
}

type mListing struct {
	cfg      ListingCfg
	minStay  map[int]int
	bookings []*mBooking
	blocks   []*mBlock
}

type mSvc struct {
	cfg      Config
	lastNow  int
	seq      int
	listings map[string]*mListing
	bookings map[string]*mBooking // id -> booking
	b2l      map[string]string    // booking id -> listing id
}

func newMSvc(cfg Config) *mSvc {
	return &mSvc{
		cfg:      cfg,
		listings: map[string]*mListing{},
		bookings: map[string]*mBooking{},
		b2l:      map[string]string{},
	}
}

func (m *mSvc) nextID(prefix string) string {
	m.seq++
	return fmt.Sprintf("%s-%d", prefix, m.seq)
}

func (m *mSvc) minStayAt(l *mListing, day int) int {
	if v, ok := l.minStay[day]; ok {
		return v
	}
	return l.cfg.DefaultMinStay
}

// purge 模拟"被接受的操作推进时间后自动失效"：仅作用于指定房源。
func (m *mSvc) purge(l *mListing, now int) {
	for _, b := range l.bookings {
		if b.status == StatusHold && b.expiry < now {
			b.status = StatusExpired
		}
	}
}

// mOcc 是 now 下的有效占用（朴素展开）。
type mOcc struct {
	s, e  int
	block bool
	id    string
}

func (m *mSvc) effective(l *mListing, now int) []mOcc {
	var occs []mOcc
	for _, b := range l.bookings {
		if b.status == StatusConfirmed || (b.status == StatusHold && now <= b.expiry) {
			occs = append(occs, mOcc{b.ci, b.co, false, b.id})
		}
	}
	for _, bl := range l.blocks {
		occs = append(occs, mOcc{bl.start, bl.end, true, bl.id})
	}
	sort.Slice(occs, func(i, j int) bool { return occs[i].s < occs[j].s })
	return occs
}

// mJudge 朴素判定 [ci,co) 可订性，返回错误码与判定依据。
func (m *mSvc) mJudge(l *mListing, ci, co, now int) (Code, string) {
	occs := m.effective(l, now)
	// 相交：按起始日升序的第一个相交者（等价于生产实现的 pred 优先）
	for _, o := range occs {
		if o.s < co && ci < o.e {
			if o.block {
				return CodeBlocked, fmt.Sprintf("intersect block %s[%d,%d)", o.id, o.s, o.e)
			}
			return CodeConflict, fmt.Sprintf("intersect booking %s[%d,%d)", o.id, o.s, o.e)
		}
	}
	var pred, next *mOcc
	for i := range occs {
		o := &occs[i]
		if o.e <= ci {
			pred = o // 升序遍历，最后一个满足者即最近前邻
		}
		if o.s >= co {
			next = o // 第一个满足者即最近后邻
			break
		}
	}
	gap := l.cfg.GapDays
	if pred != nil && !pred.block && ci-pred.e < gap {
		return CodeGap, fmt.Sprintf("prev booking %s ends %d, need gap %d", pred.id, pred.e, gap)
	}
	if next != nil && !next.block && next.s-co < gap {
		return CodeGap, fmt.Sprintf("next booking %s starts %d, need gap %d", next.id, next.s, gap)
	}
	if need := m.minStayAt(l, ci); co-ci < need {
		return CodeMinStay, fmt.Sprintf("nights %d < minStay(%d)=%d", co-ci, ci, need)
	}
	if pred != nil && !pred.block {
		if free := ci - pred.e - gap; free > 0 && free < m.minStayAt(l, pred.e+gap) {
			return CodeOrphan, fmt.Sprintf("left orphan %d nights < minStay(%d)=%d",
				free, pred.e+gap, m.minStayAt(l, pred.e+gap))
		}
	}
	if next != nil && !next.block {
		if free := next.s - co - gap; free > 0 && free < m.minStayAt(l, co+gap) {
			return CodeOrphan, fmt.Sprintf("right orphan %d nights < minStay(%d)=%d",
				free, co+gap, m.minStayAt(l, co+gap))
		}
	}
	return CodeOK, "available"
}

// mOverlap 朴素相交检查（用于封锁）。
func (m *mSvc) mOverlap(l *mListing, now, s, e int) (Code, string) {
	for _, o := range m.effective(l, now) {
		if o.s < e && s < o.e {
			if o.block {
				return CodeBlocked, fmt.Sprintf("block intersects block %s", o.id)
			}
			return CodeConflict, fmt.Sprintf("block intersects booking %s", o.id)
		}
	}
	return CodeOK, "no overlap"
}

// mRes 是模型操作的结果：错误码、输出（id/退款/差价）与判定依据。
type mRes struct {
	code Code
	out  string
	why  string
}

func mOK(out, why string) mRes     { return mRes{code: CodeOK, out: out, why: why} }
func mErr(c Code, why string) mRes { return mRes{code: c, why: why} }

func (m *mSvc) mCreateListing(now int, id string, cfg ListingCfg) mRes {
	if now < 0 || id == "" {
		return mErr(CodeInvalidParams, "bad now or empty id")
	}
	if err := cfg.validate(); err != nil {
		return mErr(CodeOf(err), "bad listing cfg")
	}
	if now < m.lastNow {
		return mErr(CodeClockRollback, fmt.Sprintf("now %d < lastNow %d", now, m.lastNow))
	}
	if m.listings[id] != nil {
		return mErr(CodeInvalidParams, "duplicate listing")
	}
	m.listings[id] = &mListing{cfg: cfg, minStay: map[int]int{}}
	m.lastNow = now
	return mOK("", "listing created")
}

func (m *mSvc) mClockListing(now int, id string) (*mListing, *mRes) {
	if now < m.lastNow {
		r := mErr(CodeClockRollback, fmt.Sprintf("now %d < lastNow %d", now, m.lastNow))
		return nil, &r
	}
	l := m.listings[id]
	if l == nil {
		r := mErr(CodeNotFound, "listing not found")
		return nil, &r
	}
	return l, nil
}

func (m *mSvc) mSetDefaultMinStay(now int, id string, nights int) mRes {
	if now < 0 || nights < 1 {
		return mErr(CodeInvalidParams, "bad now/nights")
	}
	l, r := m.mClockListing(now, id)
	if r != nil {
		return *r
	}
	l.cfg.DefaultMinStay = nights
	m.purge(l, now)
	m.lastNow = now
	return mOK("", "default min stay set")
}

func (m *mSvc) mSetMinStay(now int, id string, day, nights int) mRes {
	if now < 0 || day < 0 || nights < 1 {
		return mErr(CodeInvalidParams, "bad now/day/nights")
	}
	l, r := m.mClockListing(now, id)
	if r != nil {
		return *r
	}
	l.minStay[day] = nights
	m.purge(l, now)
	m.lastNow = now
	return mOK("", "min stay set")
}

func (m *mSvc) mSetGapDays(now int, id string, days int) mRes {
	if now < 0 || days < 0 {
		return mErr(CodeInvalidParams, "bad now/days")
	}
	l, r := m.mClockListing(now, id)
	if r != nil {
		return *r
	}
	l.cfg.GapDays = days
	m.purge(l, now)
	m.lastNow = now
	return mOK("", "gap set")
}

func (m *mSvc) mAddBlock(now int, id string, start, end int) mRes {
	if now < 0 || start < 0 || end <= start {
		return mErr(CodeInvalidParams, "bad now/range")
	}
	l, r := m.mClockListing(now, id)
	if r != nil {
		return *r
	}
	if c, why := m.mOverlap(l, now, start, end); c != CodeOK {
		return mErr(c, why)
	}
	bid := m.nextID("BL")
	l.blocks = append(l.blocks, &mBlock{id: bid, start: start, end: end})
	m.purge(l, now)
	m.lastNow = now
	return mOK(bid, "block added")
}

func (m *mSvc) mResizeBlock(now int, id, blockID string, newStart, newEnd int) mRes {
	if now < 0 || newStart < 0 || newEnd <= newStart {
		return mErr(CodeInvalidParams, "bad now/range")
	}
	l, r := m.mClockListing(now, id)
	if r != nil {
		return *r
	}
	var bl *mBlock
	for _, b := range l.blocks {
		if b.id == blockID {
			bl = b
			break
		}
	}
	if bl == nil {
		return mErr(CodeNotFound, "block not found")
	}
	if newStart >= bl.start && newEnd <= bl.end {
		bl.start, bl.end = newStart, newEnd
		m.purge(l, now)
		m.lastNow = now
		return mOK("", "block shortened")
	}
	// 延长：新增部分 = 新区间 \ 旧区间，对其做相交检查（占用中排除本封锁）
	oldS, oldE := bl.start, bl.end
	var added [][2]int
	if s0, e0 := newStart, min(newEnd, oldS); s0 < e0 {
		added = append(added, [2]int{s0, e0})
	}
	if s0, e0 := max(newStart, oldE), newEnd; s0 < e0 {
		added = append(added, [2]int{s0, e0})
	}
	var filtered []mOcc
	for _, o := range m.effective(l, now) {
		if o.block && o.id == bl.id {
			continue
		}
		filtered = append(filtered, o)
	}
	for _, rg := range added {
		for _, o := range filtered {
			if o.s < rg[1] && rg[0] < o.e {
				if o.block {
					return mErr(CodeBlocked, fmt.Sprintf("extension intersects block %s", o.id))
				}
				return mErr(CodeConflict, fmt.Sprintf("extension intersects booking %s", o.id))
			}
		}
	}
	bl.start, bl.end = newStart, newEnd
	m.purge(l, now)
	m.lastNow = now
	return mOK("", "block extended")
}

func (m *mSvc) mCreateHold(now int, id string, ci, co int) mRes {
	if now < 0 || co <= ci || ci < now {
		return mErr(CodeInvalidParams, "bad now/stay")
	}
	l, r := m.mClockListing(now, id)
	if r != nil {
		return *r
	}
	if c, why := m.mJudge(l, ci, co, now); c != CodeOK {
		return mErr(c, why)
	}
	bid := m.nextID("BK")
	b := &mBooking{
		id: bid, ci: ci, co: co, expiry: now + m.cfg.HoldWindow,
		status: StatusHold, amount: int64(co-ci) * l.cfg.NightlyPrice,
	}
	l.bookings = append(l.bookings, b)
	m.bookings[bid] = b
	m.b2l[bid] = id
	m.purge(l, now)
	m.lastNow = now
	return mOK(bid, "hold created")
}

func (m *mSvc) mPay(now int, bookingID string) mRes {
	if now < 0 {
		return mErr(CodeInvalidParams, "bad now")
	}
	if now < m.lastNow {
		return mErr(CodeClockRollback, fmt.Sprintf("now %d < lastNow %d", now, m.lastNow))
	}
	b := m.bookings[bookingID]
	if b == nil {
		return mErr(CodeNotFound, "booking not found")
	}
	if b.status != StatusHold {
		return mErr(CodeInvalidState, fmt.Sprintf("status %s", b.status))
	}
	if now > b.expiry {
		return mErr(CodeInvalidState, fmt.Sprintf("hold expired at %d", b.expiry))
	}
	b.status = StatusConfirmed
	m.purge(m.listings[m.b2l[bookingID]], now)
	m.lastNow = now
	return mOK("", "paid")
}

func (m *mSvc) mCancel(now int, bookingID string) mRes {
	if now < 0 {
		return mErr(CodeInvalidParams, "bad now")
	}
	if now < m.lastNow {
		return mErr(CodeClockRollback, fmt.Sprintf("now %d < lastNow %d", now, m.lastNow))
	}
	b := m.bookings[bookingID]
	if b == nil {
		return mErr(CodeNotFound, "booking not found")
	}
	if b.status == StatusCancelled {
		return mErr(CodeInvalidState, "already cancelled")
	}
	if b.status != StatusConfirmed {
		return mErr(CodeInvalidState, fmt.Sprintf("status %s", b.status))
	}
	if now >= b.ci {
		return mErr(CodeInvalidState, "check-in day arrived")
	}
	var refund int64
	switch d := b.ci - now; {
	case d >= m.cfg.RefundFullDays:
		refund = b.amount
	case d >= m.cfg.RefundHalfDays:
		refund = b.amount / 2
	}
	b.status = StatusCancelled
	b.refund = refund
	m.purge(m.listings[m.b2l[bookingID]], now)
	m.lastNow = now
	return mOK(fmt.Sprint(refund), fmt.Sprintf("refund %d", refund))
}

func (m *mSvc) mModify(now int, bookingID string, newCI, newCO int) mRes {
	if now < 0 || newCO <= newCI || newCI < now {
		return mErr(CodeInvalidParams, "bad now/stay")
	}
	if now < m.lastNow {
		return mErr(CodeClockRollback, fmt.Sprintf("now %d < lastNow %d", now, m.lastNow))
	}
	b := m.bookings[bookingID]
	if b == nil {
		return mErr(CodeNotFound, "booking not found")
	}
	if b.status == StatusCancelled {
		return mErr(CodeInvalidState, "already cancelled")
	}
	if b.status != StatusConfirmed {
		return mErr(CodeInvalidState, fmt.Sprintf("status %s", b.status))
	}
	if b.modified {
		return mErr(CodeInvalidState, "already modified")
	}
	if now >= b.ci {
		return mErr(CodeInvalidState, "check-in day arrived")
	}
	l := m.listings[m.b2l[bookingID]]
	// 在原预订释放后的日历上判定：临时摘除本预订
	b.status = StatusExpired // 临时置为不占用日历的状态
	code, why := m.mJudge(l, newCI, newCO, now)
	if code != CodeOK {
		b.status = StatusConfirmed
		return mErr(code, why)
	}
	b.status = StatusConfirmed
	diff := int64(newCO-newCI)*l.cfg.NightlyPrice - b.amount
	b.ci, b.co = newCI, newCO
	b.priceDiff = diff
	b.modified = true
	m.purge(l, now)
	m.lastNow = now
	return mOK(fmt.Sprint(diff), fmt.Sprintf("modified, diff %d", diff))
}

func (m *mSvc) mCheckAvail(now int, id string, ci, co int) mRes {
	if now < 0 || co <= ci || ci < now {
		return mErr(CodeInvalidParams, "bad now/stay")
	}
	l, r := m.mClockListing(now, id)
	if r != nil {
		return *r
	}
	// 只读：不推进时钟、不清除失效保留
	if c, why := m.mJudge(l, ci, co, now); c != CodeOK {
		return mErr(c, why)
	}
	return mOK("", "available")
}

func (m *mSvc) mSnapshot(id string) ListingSnapshot {
	l := m.listings[id]
	snap := ListingSnapshot{LastNow: m.lastNow}
	for _, b := range l.bookings {
		if b.status == StatusHold || b.status == StatusConfirmed {
			snap.Intervals = append(snap.Intervals, IntervalInfo{
				Start: b.ci, End: b.co, Kind: "booking", ID: b.id,
			})
		}
	}
	for _, bl := range l.blocks {
		snap.Intervals = append(snap.Intervals, IntervalInfo{
			Start: bl.start, End: bl.end, Kind: "block", ID: bl.id,
		})
	}
	sort.Slice(snap.Intervals, func(i, j int) bool {
		return snap.Intervals[i].Start < snap.Intervals[j].Start
	})
	for _, b := range l.bookings {
		snap.Bookings = append(snap.Bookings, BookingInfo{
			ID: b.id, Checkin: b.ci, Checkout: b.co, Expiry: b.expiry,
			Status: b.status, Amount: b.amount, Refund: b.refund,
			PriceDiff: b.priceDiff, Modified: b.modified,
		})
	}
	sort.Slice(snap.Bookings, func(i, j int) bool { return snap.Bookings[i].ID < snap.Bookings[j].ID })
	return snap
}

// rndOp 描述一步随机操作。
type rndOp struct {
	now     int
	kind    string
	listing string
	target  string
	a, b    int
	desc    string
}

type opResult struct {
	code Code
	out  string
}

func applySvc(svc *Service, op rndOp) opResult {
	switch op.kind {
	case "setMinStay":
		return opResult{code: CodeOf(svc.SetMinStay(op.now, op.listing, op.a, op.b))}
	case "setDefaultMinStay":
		return opResult{code: CodeOf(svc.SetDefaultMinStay(op.now, op.listing, op.a))}
	case "setGap":
		return opResult{code: CodeOf(svc.SetGapDays(op.now, op.listing, op.a))}
	case "addBlock":
		id, err := svc.AddBlock(op.now, op.listing, op.a, op.b)
		return opResult{code: CodeOf(err), out: id}
	case "resizeBlock":
		return opResult{code: CodeOf(svc.ResizeBlock(op.now, op.listing, op.target, op.a, op.b))}
	case "createHold":
		id, err := svc.CreateHold(op.now, op.listing, op.a, op.b)
		return opResult{code: CodeOf(err), out: id}
	case "pay":
		return opResult{code: CodeOf(svc.Pay(op.now, op.target))}
	case "cancel":
		r, err := svc.Cancel(op.now, op.target)
		return opResult{code: CodeOf(err), out: fmt.Sprint(r)}
	case "modify":
		d, err := svc.ModifyBooking(op.now, op.target, op.a, op.b)
		return opResult{code: CodeOf(err), out: fmt.Sprint(d)}
	case "checkAvail":
		return opResult{code: CodeOf(svc.CheckAvailability(op.now, op.listing, op.a, op.b))}
	}
	panic("unknown op " + op.kind)
}

func applyModel(m *mSvc, op rndOp) mRes {
	switch op.kind {
	case "setMinStay":
		return m.mSetMinStay(op.now, op.listing, op.a, op.b)
	case "setDefaultMinStay":
		return m.mSetDefaultMinStay(op.now, op.listing, op.a)
	case "setGap":
		return m.mSetGapDays(op.now, op.listing, op.a)
	case "addBlock":
		return m.mAddBlock(op.now, op.listing, op.a, op.b)
	case "resizeBlock":
		return m.mResizeBlock(op.now, op.listing, op.target, op.a, op.b)
	case "createHold":
		return m.mCreateHold(op.now, op.listing, op.a, op.b)
	case "pay":
		return m.mPay(op.now, op.target)
	case "cancel":
		return m.mCancel(op.now, op.target)
	case "modify":
		return m.mModify(op.now, op.target, op.a, op.b)
	case "checkAvail":
		return m.mCheckAvail(op.now, op.listing, op.a, op.b)
	}
	panic("unknown op " + op.kind)
}

func genOp(r *rand.Rand, m *mSvc, now int, listingIDs []string) rndOp {
	listing := listingIDs[r.Intn(len(listingIDs))]
	if r.Intn(20) == 0 {
		listing = "NOPE"
	}
	bookingTarget := func() string {
		if r.Intn(12) == 0 || len(m.bookings) == 0 {
			return "BK-999999"
		}
		ids := make([]string, 0, len(m.bookings))
		for id := range m.bookings {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		return ids[r.Intn(len(ids))]
	}
	blockTarget := func() string {
		l := m.listings[listing]
		if l == nil || len(l.blocks) == 0 || r.Intn(12) == 0 {
			return "BL-999999"
		}
		return l.blocks[r.Intn(len(l.blocks))].id
	}
	stay := func() (int, int) {
		ci := now + r.Intn(18) - 1 // 偶尔 ci < now 触发参数非法
		return ci, ci + 1 + r.Intn(6)
	}
	ci, co := stay()
	switch w := r.Intn(100); {
	case w < 28:
		return rndOp{now, "createHold", listing, "", ci, co,
			fmt.Sprintf("CreateHold(%s,[%d,%d))", listing, ci, co)}
	case w < 42:
		return rndOp{now, "checkAvail", listing, "", ci, co,
			fmt.Sprintf("CheckAvailability(%s,[%d,%d))", listing, ci, co)}
	case w < 52:
		t := bookingTarget()
		return rndOp{now, "pay", "", t, 0, 0, fmt.Sprintf("Pay(%s)", t)}
	case w < 61:
		t := bookingTarget()
		return rndOp{now, "cancel", "", t, 0, 0, fmt.Sprintf("Cancel(%s)", t)}
	case w < 70:
		t := bookingTarget()
		return rndOp{now, "modify", "", t, ci, co, fmt.Sprintf("Modify(%s,[%d,%d))", t, ci, co)}
	case w < 78:
		start := now + r.Intn(18)
		return rndOp{now, "addBlock", listing, "", start, start + 1 + r.Intn(5),
			fmt.Sprintf("AddBlock(%s,[%d,%d))", listing, start, start+1)}
	case w < 84:
		t := blockTarget()
		ns := now + r.Intn(20) - 2
		ne := ns + r.Intn(8) // 偶尔 ne <= ns 触发参数非法
		return rndOp{now, "resizeBlock", listing, t, ns, ne,
			fmt.Sprintf("ResizeBlock(%s,%s,[%d,%d))", listing, t, ns, ne)}
	case w < 91:
		return rndOp{now, "setMinStay", listing, "", r.Intn(40), 1 + r.Intn(4),
			fmt.Sprintf("SetMinStay(%s,day)", listing)}
	case w < 96:
		return rndOp{now, "setDefaultMinStay", listing, "", 1 + r.Intn(4), 0,
			fmt.Sprintf("SetDefaultMinStay(%s)", listing)}
	default:
		return rndOp{now, "setGap", listing, "", r.Intn(3), 0,
			fmt.Sprintf("SetGapDays(%s)", listing)}
	}
}

// TestRandomAgainstModel 用随机操作序列对照生产实现与朴素模型，
// 每步打印输入、输出与判定依据，并逐步比对快照。
func TestRandomAgainstModel(t *testing.T) {
	for _, seed := range []int64{1, 7, 42, 2026, 99173, 31337, 555, 80808, 123456789, 271828} {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			r := rand.New(rand.NewSource(seed))
			cfg := Config{HoldWindow: 4, RefundFullDays: 7, RefundHalfDays: 3}
			svc, err := NewService(cfg)
			if err != nil {
				t.Fatalf("NewService: %v", err)
			}
			m := newMSvc(cfg)
			listingIDs := []string{"L1", "L2"}
			for _, id := range listingIDs {
				lcfg := ListingCfg{
					NightlyPrice:   int64(50 + r.Intn(150)),
					DefaultMinStay: 1 + r.Intn(3),
					GapDays:        r.Intn(2),
				}
				if err := svc.CreateListing(0, id, lcfg); err != nil {
					t.Fatalf("CreateListing: %v", err)
				}
				if res := m.mCreateListing(0, id, lcfg); res.code != CodeOK {
					t.Fatalf("model CreateListing: %v", res)
				}
			}
			now := 0
			for step := 0; step < 1200; step++ {
				switch r.Intn(20) {
				case 0:
					now -= r.Intn(4) // 时钟回退尝试
				case 1:
					now += 8 + r.Intn(10) // 大跳变，促使保留过期
				default:
					now += r.Intn(3)
				}
				if now < 0 {
					now = 0
				}
				op := genOp(r, m, now, listingIDs)
				got := applySvc(svc, op)
				want := applyModel(m, op)
				t.Logf("step=%03d now=%d %s => svc(%s,%q) model(%s,%q) why=%s",
					step, now, op.desc, got.code, got.out, want.code, want.out, want.why)
				if got.code != want.code || (got.code == CodeOK && got.out != want.out) {
					t.Fatalf("step %d divergence on %s:\n svc=(%s,%q)\n model=(%s,%q) why=%s",
						step, op.desc, got.code, got.out, want.code, want.out, want.why)
				}
				for _, id := range listingIDs {
					ss, err := svc.Snapshot(id)
					if err != nil {
						t.Fatalf("Snapshot: %v", err)
					}
					if ms := m.mSnapshot(id); !reflect.DeepEqual(ss, ms) {
						t.Fatalf("step %d snapshot divergence on %s after %s:\n svc=%+v\n model=%+v",
							step, id, op.desc, ss, ms)
					}
				}
			}
		})
	}
}
