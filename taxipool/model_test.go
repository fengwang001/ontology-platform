package taxipool

// 朴素逐队列推进模型：用切片与线性扫描独立实现同一套规则，
// 与 treap 实现做随机操作序列对照。每条操作打印输入、输出与判定依据。

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"
)

type nEntry struct {
	id       string
	priority bool
	ts       int64
}

type nVoucher struct {
	issuedAt, expiresAt int64
	strikes             int
}

type nTrip struct {
	dist, departed int64
}

type nDriver struct {
	state       string // idle / queued / dispatched / arrived
	terminal    string
	priority    bool
	enterTS     int64
	deadline    int64
	noShows     int
	bannedUntil int64
	voucher     *nVoucher
	trip        *nTrip
	day         int64
	dayCount    int
}

type naivePool struct {
	cfg   Config
	qs    map[string][]nEntry
	ds    map[string]*nDriver
	clock int64
}

func newNaivePool(cfg Config) *naivePool {
	qs := make(map[string][]nEntry, len(cfg.Terminals))
	for id := range cfg.Terminals {
		qs[id] = nil
	}
	return &naivePool{cfg: cfg, qs: qs, ds: map[string]*nDriver{}}
}

func nEntryLess(a, b nEntry) bool {
	if a.priority != b.priority {
		return a.priority
	}
	if a.ts != b.ts {
		return a.ts < b.ts
	}
	return a.id < b.id
}

func (n *naivePool) enter(id, term string, ts int64) (EnterResult, string, error) {
	if id == "" || term == "" || ts < 0 {
		return EnterResult{}, "参数非法", ErrInvalidParam
	}
	if ts < n.clock {
		return EnterResult{}, fmt.Sprintf("时钟回退 ts=%d<clock=%d", ts, n.clock), ErrClockRollback
	}
	q, ok := n.qs[term]
	if !ok {
		return EnterResult{}, "候机楼不存在", ErrTerminalNotFound
	}
	d, known := n.ds[id]
	if !known {
		d = &nDriver{state: "idle"}
	}
	var why []string
	res := EnterResult{}
	if d.voucher != nil && ts >= d.voucher.expiresAt {
		d.voucher = nil
		why = append(why, "旧凭证已过期丢弃")
	}
	if d.trip != nil {
		tr := *d.trip
		d.trip = nil
		short := tr.dist <= n.cfg.ShortTripDistance
		timely := ts-tr.departed <= n.cfg.ReturnLimit
		switch {
		case !short || !timely:
			why = append(why, fmt.Sprintf("不短途/不及时(距%d,隔%d)", tr.dist, ts-tr.departed))
		case d.voucher != nil:
			why = append(why, "已持有凭证,不再发放")
		default:
			day := dayOf(ts, n.cfg.DayOffsetMinutes)
			if d.day != day {
				d.day = day
				d.dayCount = 0
			}
			if d.dayCount < n.cfg.DailyVoucherLimit {
				d.dayCount++
				d.voucher = &nVoucher{issuedAt: ts, expiresAt: ts + n.cfg.VoucherValidity}
				res.VoucherIssued = true
				why = append(why, fmt.Sprintf("发凭证(距%d<=%d,隔%d<=%d,日%d/%d)",
					tr.dist, n.cfg.ShortTripDistance, ts-tr.departed, n.cfg.ReturnLimit,
					d.dayCount, n.cfg.DailyVoucherLimit))
			} else {
				why = append(why, "日额度已满")
			}
		}
	}
	if !known && d.voucher != nil {
		n.ds[id] = d
	}
	if ts < d.bannedUntil {
		return EnterResult{}, fmt.Sprintf("禁入至%d", d.bannedUntil), ErrDriverBanned
	}
	if d.state == "queued" || d.state == "dispatched" {
		return EnterResult{}, "已在队列或放行中", ErrDriverInQueue
	}
	if len(q) >= n.cfg.Terminals[term] {
		return EnterResult{}, fmt.Sprintf("队列满%d/%d", len(q), n.cfg.Terminals[term]), ErrQueueFull
	}
	prio := false
	if d.voucher != nil {
		pc := 0
		for _, e := range q {
			if e.priority {
				pc++
			}
		}
		if pc < n.cfg.PriorityCap {
			prio = true
			d.voucher = nil
			why = append(why, fmt.Sprintf("凭证入队(名额%d/%d)", pc, n.cfg.PriorityCap))
		} else {
			d.voucher.strikes++
			why = append(why, fmt.Sprintf("名额满,受限第%d次", d.voucher.strikes))
			if d.voucher.strikes >= 2 {
				d.voucher = nil
				why = append(why, "凭证作废")
			}
		}
	}
	e := nEntry{id: id, priority: prio, ts: ts}
	idx := 0
	for idx < len(q) && nEntryLess(q[idx], e) {
		idx++
	}
	q = append(q, nEntry{})
	copy(q[idx+1:], q[idx:])
	q[idx] = e
	n.qs[term] = q
	d.state = "queued"
	d.terminal = term
	d.priority = prio
	d.enterTS = ts
	n.ds[id] = d
	n.clock = ts
	res.Position = idx
	res.Priority = prio
	res.VoucherHeld = d.voucher != nil
	why = append(why, fmt.Sprintf("入队pos=%d", idx))
	return res, strings.Join(why, ";"), nil
}

func (n *naivePool) dispatch(term string, ts int64) (DispatchResult, string, error) {
	if term == "" || ts < 0 {
		return DispatchResult{}, "参数非法", ErrInvalidParam
	}
	if ts < n.clock {
		return DispatchResult{}, fmt.Sprintf("时钟回退 ts=%d<clock=%d", ts, n.clock), ErrClockRollback
	}
	if _, ok := n.qs[term]; !ok {
		return DispatchResult{}, "候机楼不存在", ErrTerminalNotFound
	}
	src := term
	transferred := false
	if len(n.qs[term]) == 0 {
		src = ""
		var bestTS int64
		ids := make([]string, 0, len(n.qs))
		for id := range n.qs {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			if len(n.qs[id]) == 0 || n.qs[id][0].priority {
				continue // 空队列或队首为优先司机不参与调剂
			}
			if src == "" || n.qs[id][0].ts < bestTS || (n.qs[id][0].ts == bestTS && id < src) {
				src = id
				bestTS = n.qs[id][0].ts
			}
		}
		if src == "" {
			return DispatchResult{}, "无车可放行", ErrNoCarAvailable
		}
		transferred = true
	}
	head := n.qs[src][0]
	n.qs[src] = n.qs[src][1:]
	d := n.ds[head.id]
	d.state = "dispatched"
	d.terminal = ""
	limit := n.cfg.ArriveLimit
	if transferred {
		limit = n.cfg.TransferLimit
	}
	d.deadline = ts + limit
	n.clock = ts
	reason := fmt.Sprintf("本队放行队首@%d", head.ts)
	if transferred {
		reason = fmt.Sprintf("调剂自%s队首@%d", src, head.ts)
	}
	return DispatchResult{DriverID: head.id, FromTerminal: src, Deadline: d.deadline, Transferred: transferred}, reason, nil
}

func (n *naivePool) arrive(id string, ts int64) (string, error) {
	if id == "" || ts < 0 {
		return "参数非法", ErrInvalidParam
	}
	if ts < n.clock {
		return fmt.Sprintf("时钟回退 ts=%d<clock=%d", ts, n.clock), ErrClockRollback
	}
	d := n.ds[id]
	if d == nil {
		return "司机不存在", ErrDriverNotFound
	}
	if d.state != "dispatched" {
		return "未处于放行中", ErrDriverNotDispatched
	}
	if ts >= d.deadline {
		return fmt.Sprintf("到达逾期 ts=%d>=dl=%d", ts, d.deadline), ErrArrivalOverdue
	}
	dl := d.deadline
	d.state = "arrived"
	d.deadline = 0
	n.clock = ts
	return fmt.Sprintf("按时到达 ts=%d<dl=%d", ts, dl), nil
}

func (n *naivePool) noShow(id string, ts int64) (string, error) {
	if id == "" || ts < 0 {
		return "参数非法", ErrInvalidParam
	}
	if ts < n.clock {
		return fmt.Sprintf("时钟回退 ts=%d<clock=%d", ts, n.clock), ErrClockRollback
	}
	d := n.ds[id]
	if d == nil {
		return "司机不存在", ErrDriverNotFound
	}
	if d.state != "dispatched" {
		return "未处于放行中", ErrDriverNotDispatched
	}
	if ts < d.deadline {
		return fmt.Sprintf("尚未逾期 dl=%d", d.deadline), ErrInvalidParam
	}
	d.state = "idle"
	d.deadline = 0
	d.noShows++
	if d.noShows >= n.cfg.NoShowLimit {
		d.bannedUntil = ts + n.cfg.BanDuration
	}
	n.clock = ts
	return fmt.Sprintf("记爽约%d次,禁入至%d", d.noShows, d.bannedUntil), nil
}

func (n *naivePool) completeTrip(id string, dist int64, ts int64) (string, error) {
	if id == "" || dist < 0 || ts < 0 {
		return "参数非法", ErrInvalidParam
	}
	if ts < n.clock {
		return fmt.Sprintf("时钟回退 ts=%d<clock=%d", ts, n.clock), ErrClockRollback
	}
	d := n.ds[id]
	if d == nil {
		return "司机不存在", ErrDriverNotFound
	}
	if d.state != "arrived" {
		return "未到达不能登记行程", ErrInvalidParam
	}
	d.state = "idle"
	d.trip = &nTrip{dist: dist, departed: ts}
	n.clock = ts
	return fmt.Sprintf("登记行程 距%d", dist), nil
}

func (n *naivePool) leave(id string, ts int64) (string, error) {
	if id == "" || ts < 0 {
		return "参数非法", ErrInvalidParam
	}
	if ts < n.clock {
		return fmt.Sprintf("时钟回退 ts=%d<clock=%d", ts, n.clock), ErrClockRollback
	}
	d := n.ds[id]
	if d == nil || d.state != "queued" {
		return "司机不在队列中", ErrDriverNotFound
	}
	q := n.qs[d.terminal]
	for i, e := range q {
		if e.id == id {
			n.qs[d.terminal] = append(q[:i], q[i+1:]...)
			break
		}
	}
	d.state = "idle"
	d.terminal = ""
	n.clock = ts
	return "离队(不耗凭证不计爽约)", nil
}

func (n *naivePool) position(id string) (int, string, error) {
	d := n.ds[id]
	if d == nil || d.state != "queued" {
		return 0, "司机不在队列中", ErrDriverNotFound
	}
	for i, e := range n.qs[d.terminal] {
		if e.id == id {
			return i, fmt.Sprintf("线性扫描第%d位", i), nil
		}
	}
	return 0, "不一致", ErrDriverNotFound
}

// debugState 与 Pool.debugState 输出格式完全一致。
func (n *naivePool) debugState() string {
	var b strings.Builder
	fmt.Fprintf(&b, "clock=%d", n.clock)
	termIDs := make([]string, 0, len(n.qs))
	for id := range n.qs {
		termIDs = append(termIDs, id)
	}
	sort.Strings(termIDs)
	for _, id := range termIDs {
		q := n.qs[id]
		pc := 0
		for _, e := range q {
			if e.priority {
				pc++
			}
		}
		fmt.Fprintf(&b, " | %s[%d/%d pri=%d]:", id, len(q), n.cfg.Terminals[id], pc)
		for _, e := range q {
			mark := "n"
			if e.priority {
				mark = "P"
			}
			fmt.Fprintf(&b, " %s:%s@%d", e.id, mark, e.ts)
		}
	}
	driverIDs := make([]string, 0, len(n.ds))
	for id := range n.ds {
		driverIDs = append(driverIDs, id)
	}
	sort.Strings(driverIDs)
	for _, id := range driverIDs {
		d := n.ds[id]
		fmt.Fprintf(&b, " | %s:%s", id, d.state)
		if d.state == "queued" {
			fmt.Fprintf(&b, "@%s", d.terminal)
		}
		if d.state == "dispatched" {
			fmt.Fprintf(&b, " dl=%d", d.deadline)
		}
		fmt.Fprintf(&b, " ns=%d ban=%d day=%d:%d", d.noShows, d.bannedUntil, d.day, d.dayCount)
		if d.voucher != nil {
			fmt.Fprintf(&b, " v=(%d,%d,s%d)", d.voucher.issuedAt, d.voucher.expiresAt, d.voucher.strikes)
		}
		if d.trip != nil {
			fmt.Fprintf(&b, " trip=(%d,%d)", d.trip.dist, d.trip.departed)
		}
	}
	return b.String()
}

// 随机操作序列对照：每个种子下逐操作比较 treap 实现与朴素模型的
// 错误、返回值与全量可观测状态，并打印输入、输出与判定依据。
func TestModelComparison(t *testing.T) {
	type scenario struct {
		cfg     Config
		drivers []string
		terms   []string
	}
	scenarios := map[string]scenario{
		"standard": {
			cfg: Config{
				Terminals:         map[string]int{"T1": 2, "T2": 3, "T3": 2},
				ArriveLimit:       60,
				TransferLimit:     80,
				NoShowLimit:       2,
				BanDuration:       40,
				ShortTripDistance: 50,
				ReturnLimit:       60,
				VoucherValidity:   80,
				DailyVoucherLimit: 2,
				PriorityCap:       1,
				DayOffsetMinutes:  480, // 自然日按 UTC+8 切分
			},
			drivers: []string{"alpha", "bravo", "charlie", "delta"},
			terms:   []string{"T1", "T2", "T3"},
		},
		// 小容量 + 短时效：集中命中队列满、优先名额受限、凭证作废与过期
		"tight": {
			cfg: Config{
				Terminals:         map[string]int{"T1": 1, "T2": 1, "T3": 1},
				ArriveLimit:       60,
				TransferLimit:     80,
				NoShowLimit:       2,
				BanDuration:       40,
				ShortTripDistance: 50,
				ReturnLimit:       60,
				VoucherValidity:   30,
				DailyVoucherLimit: 4,
				PriorityCap:       1,
				DayOffsetMinutes:  480,
			},
			drivers: []string{"alpha", "bravo", "charlie", "delta"},
			terms:   []string{"T1", "T2", "T3"},
		},
		// 高额度 + 长时效 + 单主队列：集中命中优先名额受限与凭证作废
		"cap": {
			cfg: Config{
				Terminals:         map[string]int{"T1": 3, "T2": 1},
				ArriveLimit:       60,
				TransferLimit:     80,
				NoShowLimit:       2,
				BanDuration:       40,
				ShortTripDistance: 50,
				ReturnLimit:       60,
				VoucherValidity:   200,
				DailyVoucherLimit: 10,
				PriorityCap:       1,
				DayOffsetMinutes:  480,
			},
			drivers: []string{"alpha", "bravo", "charlie"},
			terms:   []string{"T1", "T1", "T2"}, // 偏向 T1
		},
	}
	dists := []int64{49, 50, 50, 50, 51, 200}
	for name, sc := range scenarios {
		for seed := int64(1); seed <= 5; seed++ {
			t.Run(fmt.Sprintf("%s/seed=%d", name, seed), func(t *testing.T) {
				runModelComparison(t, sc.cfg, sc.drivers, sc.terms, dists, seed)
			})
		}
	}
}

func runModelComparison(t *testing.T, cfg Config, drivers, terms []string, dists []int64, seed int64) {
	{
		real := mustPool(t, cfg)
		naive := newNaivePool(cfg)
		rng := rand.New(rand.NewSource(seed))
		var ts int64
		for i := 0; i < 1500; i++ {
			switch r := rng.Intn(10); {
			case r < 4: // 同一时刻
			case r < 9:
				ts += 1 + rng.Int63n(8)
			default: // 时钟回退尝试
				if ts > 0 {
					ts--
				}
			}
			var desc, reason string
			var rerr, nerr error
			var rres, nres string
			switch op := rng.Intn(100); {
			case op < 30:
				d, tm := drivers[rng.Intn(len(drivers))], terms[rng.Intn(len(terms))]
				desc = fmt.Sprintf("enter %s %s", d, tm)
				rr, re := real.EnterPool(d, tm, ts)
				nr, rs, ne := naive.enter(d, tm, ts)
				reason = rs
				rerr, nerr = re, ne
				rres, nres = fmt.Sprintf("%+v", rr), fmt.Sprintf("%+v", nr)
			case op < 46:
				tm := terms[rng.Intn(len(terms))]
				desc = fmt.Sprintf("dispatch %s", tm)
				rr, re := real.Dispatch(tm, ts)
				nr, rs, ne := naive.dispatch(tm, ts)
				reason = rs
				rerr, nerr = re, ne
				rres, nres = fmt.Sprintf("%+v", rr), fmt.Sprintf("%+v", nr)
			case op < 60:
				d := drivers[rng.Intn(len(drivers))]
				desc = fmt.Sprintf("arrive %s", d)
				rerr = real.Arrive(d, ts)
				reason, nerr = naive.arrive(d, ts)
			case op < 66:
				d := drivers[rng.Intn(len(drivers))]
				desc = fmt.Sprintf("noshow %s", d)
				rerr = real.NoShow(d, ts)
				reason, nerr = naive.noShow(d, ts)
			case op < 72:
				d := drivers[rng.Intn(len(drivers))]
				desc = fmt.Sprintf("leave %s", d)
				rerr = real.Leave(d, ts)
				reason, nerr = naive.leave(d, ts)
			case op < 90:
				d := drivers[rng.Intn(len(drivers))]
				dist := dists[rng.Intn(len(dists))]
				desc = fmt.Sprintf("trip %s dist=%d", d, dist)
				rerr = real.CompleteTrip(d, dist, ts)
				reason, nerr = naive.completeTrip(d, dist, ts)
			default:
				d := drivers[rng.Intn(len(drivers))]
				desc = fmt.Sprintf("position %s", d)
				rp, re := real.Position(d)
				np, rs, ne := naive.position(d)
				reason = rs
				rerr, nerr = re, ne
				rres, nres = fmt.Sprintf("%d", rp), fmt.Sprintf("%d", np)
			}
			if rerr != nerr {
				t.Fatalf("op %d %s: error mismatch real=%v naive=%v", i, desc, rerr, nerr)
			}
			if rres != nres {
				t.Fatalf("op %d %s: result mismatch real=%s naive=%s", i, desc, rres, nres)
			}
			if got, want := real.debugState(), naive.debugState(); got != want {
				t.Fatalf("op %d %s: state diverged\nreal:  %s\nnaive: %s", i, desc, got, want)
			}
			t.Logf("op=%03d ts=%d %-24s -> err=%v res=%s | 依据: %s", i, ts, desc, rerr, rres, reason)
		}
	}
}
