package taxipool

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"
	"time"
)

// 本文件是“独立编写的朴素逐队列推进模型”：
//   - 每条队列只是一个 []naiveEntry，任何查询都从头线性扫描；
//   - 与生产实现（链表 + 凭证台账 + 锁）没有共享代码，只共享配置与错误哨兵。
// 随机生成操作序列，串行施加到两个模型，逐步比对错误类别、各队列快照、
// 司机关键状态（状态/候机楼/爽约数/禁入期/凭证持有），并打印每条操作的
// 输入、输出与判定依据。

type naiveEntry struct {
	driver   string
	priority bool
	enqAt    time.Time
}

type naiveVoucher struct {
	expiresAt          time.Time
	day                string
	restrictedAttempts int
	alive              bool
}

type naiveDriver struct {
	registered bool
	state      string // idle queued dispatched serving
	terminal   string
	enqAt      time.Time
	priority   bool
	deadline   time.Time
	noShows    int
	banUntil   time.Time
	lastLeftAt time.Time
	lastDist   float64
	voucher    *naiveVoucher
}

type naiveModel struct {
	cfg    *Config
	now    time.Time
	drv    map[string]*naiveDriver
	queues map[string][]naiveEntry
	dayUse map[string]map[string]int
	log    strings.Builder
}

func newNaive(cfg *Config) *naiveModel {
	m := &naiveModel{
		cfg:    cfg,
		drv:    map[string]*naiveDriver{},
		queues: map[string][]naiveEntry{},
		dayUse: map[string]map[string]int{},
	}
	for id := range cfg.Terminals {
		m.queues[id] = nil
	}
	return m
}

func (m *naiveModel) nd(id string) *naiveDriver {
	d := m.drv[id]
	if d == nil {
		d = &naiveDriver{state: "idle"}
		m.drv[id] = d
	}
	return d
}

func (m *naiveModel) note(s string, args ...any) {
	fmt.Fprintf(&m.log, "    依据: %s\n", fmt.Sprintf(s, args...))
}

func (m *naiveModel) noShows(at time.Time, skip string) {
	for id, d := range m.drv {
		if id == skip || d.state != "dispatched" {
			continue
		}
		if !at.Before(d.deadline) {
			d.noShows++
			if d.noShows >= m.cfg.NoShowLimit {
				d.banUntil = d.deadline.Add(m.cfg.BanDuration)
				d.noShows = 0
			}
			d.state = "idle"
			d.terminal = ""
			d.priority = false
		}
	}
}

func (m *naiveModel) register(id string, at time.Time) ErrorKind {
	if id == "" {
		return KindInvalidParam
	}
	if !m.now.IsZero() && at.Before(m.now) {
		return KindClockRewind
	}
	m.now = at
	d := m.nd(id)
	d.registered = true
	return 0
}

func (m *naiveModel) join(id, term string, at time.Time) (ErrorKind, bool, bool) {
	if id == "" || term == "" {
		return KindInvalidParam, false, false
	}
	if !m.now.IsZero() && at.Before(m.now) {
		return KindClockRewind, false, false
	}
	capv, ok := m.cfg.Terminals[term]
	if !ok {
		return KindTerminalNotFound, false, false
	}
	d, known := m.drv[id]
	if !known || !d.registered {
		return KindDriverNotFound, false, false
	}
	m.noShows(at, "")
	if d.state != "idle" {
		return KindAlreadyQueued, false, false
	}
	issued := false
	useVoucher := false
	if d.voucher != nil {
		if at.Before(d.voucher.expiresAt) && d.voucher.alive {
			useVoucher = true
		} else {
			d.voucher = nil
		}
	}
	if !useVoucher && d.voucher == nil {
		short := !d.lastLeftAt.IsZero() &&
			at.Sub(d.lastLeftAt) <= m.cfg.ReturnLimit &&
			d.lastDist <= m.cfg.ShortTripMeters
		if short {
			day := m.cfg.dayKey(at)
			if m.dayUse[id] == nil {
				m.dayUse[id] = map[string]int{}
			}
			if m.dayUse[id][day] < m.cfg.DailyVoucherLimit {
				m.dayUse[id][day]++
				d.voucher = &naiveVoucher{
					expiresAt: at.Add(m.cfg.VoucherTTL), day: day, alive: true,
				}
				issued = true
				useVoucher = true
			}
			d.lastLeftAt = time.Time{}
		}
	}
	if !d.banUntil.IsZero() && at.Before(d.banUntil) {
		return KindDriverBanned, issued, false
	}
	if len(m.queues[term]) >= capv {
		return KindQueueFull, issued, false
	}
	prio := useVoucher
	if useVoucher {
		pcount := 0
		for _, e := range m.queues[term] {
			if e.priority {
				pcount++
			}
		}
		if pcount >= m.cfg.PrioritySlots {
			d.voucher.restrictedAttempts++
			if d.voucher.restrictedAttempts >= 2 {
				d.voucher.alive = false
				d.voucher = nil
			}
			prio = false
		} else {
			d.voucher = nil
		}
	}
	m.insertLocked(term, naiveEntry{driver: id, priority: prio, enqAt: at})
	d.state = "queued"
	d.terminal = term
	d.enqAt = at
	d.priority = prio
	m.now = at
	return 0, issued, prio
}

// insertLocked：优先段在前、普通段在后，段内按（时刻、标识）有序插入。
func (m *naiveModel) insertLocked(term string, e naiveEntry) {
	q := m.queues[term]
	less := func(a, b naiveEntry) bool {
		if !a.enqAt.Equal(b.enqAt) {
			return a.enqAt.Before(b.enqAt)
		}
		return a.driver < b.driver
	}
	idx := len(q)
	if e.priority {
		// 段内有序：找到第一个严格在其后的优先项。
		for i, x := range q {
			if x.priority && less(e, x) {
				idx = i
				break
			}
			if !x.priority {
				idx = i
				break
			}
		}
	} else {
		for i := len(q) - 1; i >= 0; i-- {
			if q[i].priority || !less(e, q[i]) {
				idx = i + 1
				break
			}
			idx = i
		}
	}
	q = append(q, naiveEntry{})
	copy(q[idx+1:], q[idx:])
	q[idx] = e
	m.queues[term] = q
}

func (m *naiveModel) popFront(term string) (naiveEntry, bool) {
	q := m.queues[term]
	if len(q) == 0 {
		return naiveEntry{}, false
	}
	e := q[0]
	m.queues[term] = q[1:]
	return e, true
}

func (m *naiveModel) front(term string) (naiveEntry, bool) {
	q := m.queues[term]
	if len(q) == 0 {
		return naiveEntry{}, false
	}
	return q[0], true
}

func (m *naiveModel) dispatch(req string, at time.Time) (ErrorKind, string, string, bool, time.Time) {
	if req == "" {
		return KindInvalidParam, "", "", false, time.Time{}
	}
	if !m.now.IsZero() && at.Before(m.now) {
		return KindClockRewind, "", "", false, time.Time{}
	}
	if _, ok := m.cfg.Terminals[req]; !ok {
		return KindTerminalNotFound, "", "", false, time.Time{}
	}
	m.noShows(at, "")
	source := req
	transferred := false
	if _, ok := m.front(req); !ok {
		var donor string
		var best time.Time
		ids := append([]string(nil), func() []string {
			out := make([]string, 0, len(m.queues))
			for id := range m.queues {
				out = append(out, id)
			}
			sort.Strings(out)
			return out
		}()...)
		for _, id := range ids {
			if id == req {
				continue
			}
			f, ok := m.front(id)
			if !ok || f.priority {
				continue
			}
			if donor == "" || f.enqAt.Before(best) ||
				(f.enqAt.Equal(best) && id < donor) {
				donor, best = id, f.enqAt
			}
		}
		if donor == "" {
			return KindNoTaxi, "", "", false, time.Time{}
		}
		source, transferred = donor, true
	}
	e, _ := m.popFront(source)
	d := m.nd(e.driver)
	limit := m.cfg.ArrivalLimit
	if transferred {
		limit = m.cfg.TransferLimit
	}
	d.state = "dispatched"
	d.terminal = source
	d.deadline = at.Add(limit)
	d.priority = e.priority
	m.now = at
	return 0, e.driver, source, transferred, d.deadline
}

func (m *naiveModel) arrive(id string, at time.Time) ErrorKind {
	if id == "" {
		return KindInvalidParam
	}
	if !m.now.IsZero() && at.Before(m.now) {
		return KindClockRewind
	}
	d, known := m.drv[id]
	if !known || !d.registered {
		return KindDriverNotFound
	}
	m.noShows(at, id)
	if d.state != "dispatched" {
		return KindNotDispatched
	}
	if !at.Before(d.deadline) {
		d.noShows++
		if d.noShows >= m.cfg.NoShowLimit {
			d.banUntil = d.deadline.Add(m.cfg.BanDuration)
			d.noShows = 0
		}
		d.state = "idle"
		d.terminal = ""
		m.now = at
		return KindArrivalOverdue
	}
	d.state = "serving"
	d.priority = false
	m.now = at
	return 0
}

func (m *naiveModel) complete(id string, at time.Time, dist float64) ErrorKind {
	if id == "" || dist < 0 {
		return KindInvalidParam
	}
	if !m.now.IsZero() && at.Before(m.now) {
		return KindClockRewind
	}
	d, known := m.drv[id]
	if !known || !d.registered {
		return KindDriverNotFound
	}
	m.noShows(at, "")
	if d.state != "serving" {
		return KindNotDispatched
	}
	d.state = "idle"
	d.terminal = ""
	d.lastLeftAt = at
	d.lastDist = dist
	m.now = at
	return 0
}

func (m *naiveModel) leave(id string, at time.Time) ErrorKind {
	if id == "" {
		return KindInvalidParam
	}
	if !m.now.IsZero() && at.Before(m.now) {
		return KindClockRewind
	}
	d, known := m.drv[id]
	if !known || !d.registered {
		return KindDriverNotFound
	}
	m.noShows(at, "")
	if d.state == "dispatched" {
		return KindCannotLeaveDispatched
	}
	if d.state != "queued" {
		return KindNotInQueue
	}
	q := m.queues[d.terminal]
	out := q[:0]
	for _, e := range q {
		if e.driver != id {
			out = append(out, e)
		}
	}
	m.queues[d.terminal] = out
	d.state = "idle"
	d.terminal = ""
	d.priority = false
	m.now = at
	return 0
}

type opKind int

const (
	opRegister opKind = iota
	opJoin
	opDispatch
	opArrive
	opComplete
	opLeave
)

type testOp struct {
	kind opKind
	drv  string
	term string
	at   time.Time
	dist float64
}

func diffConfig(rng *rand.Rand) *Config {
	terms := map[string]int{}
	tn := 1 + rng.Intn(3)
	for i := 0; i < tn; i++ {
		terms[fmt.Sprintf("T%d", i+1)] = 1 + rng.Intn(4)
	}
	return &Config{
		Terminals:         terms,
		ArrivalLimit:      time.Duration(3+rng.Intn(6)) * time.Minute,
		TransferLimit:     time.Duration(2+rng.Intn(4)) * time.Minute,
		ShortTripMeters:   float64(1000 + rng.Intn(3000)),
		ReturnLimit:       time.Duration(10+rng.Intn(40)) * time.Minute,
		VoucherTTL:        time.Duration(15+rng.Intn(60)) * time.Minute,
		DailyVoucherLimit: 1 + rng.Intn(2),
		PrioritySlots:     1 + rng.Intn(2),
		NoShowLimit:       1 + rng.Intn(2),
		BanDuration:       time.Duration(30+rng.Intn(120)) * time.Minute,
		TimeZone:          testTZ,
	}
}

func generateOps(rng *rand.Rand, cfg *Config, n int) []testOp {
	var terms []string
	for id := range cfg.Terminals {
		terms = append(terms, id)
	}
	sort.Strings(terms)
	drivers := []string{"d01", "d02", "d03", "d04", "d05", "d06"}
	ops := make([]testOp, 0, n)
	base := time.Date(2026, 10, 6, 8, 0, 0, 0, testTZ)
	cur := base
	for i := 0; i < n; i++ {
		// 时间非递减，偶尔落在同一时刻以覆盖字典序规则；偶尔大跳步覆盖逾期。
		step := []int{0, 0, 1, 1, 2, 5, 12}[rng.Intn(7)]
		cur = cur.Add(time.Duration(step) * time.Minute)
		op := testOp{drv: drivers[rng.Intn(len(drivers))], at: cur}
		switch rng.Intn(100) {
		case 0, 1, 2, 3, 4, 5:
			op.kind = opRegister
		case 6, 7, 8, 9, 40, 41, 42, 43, 44, 45, 46, 47, 48, 49:
			op.kind = opJoin
			op.term = terms[rng.Intn(len(terms))]
			op.dist = float64(rng.Intn(5000)) // 由 Complete 写入，Join 不读
		case 50, 51, 52, 53, 54, 55, 56, 57, 58, 59, 60, 61, 62, 63, 64:
			op.kind = opDispatch
			op.term = terms[rng.Intn(len(terms))]
		case 65, 66, 67, 68, 69, 70, 71, 72, 73, 74:
			op.kind = opArrive
		case 75, 76, 77, 78, 79, 80, 81, 82, 83, 84:
			op.kind = opComplete
			// 距离围绕阈值取值，含恰等于阈值。
			op.dist = cfg.ShortTripMeters - float64(rng.Intn(3)-1)*1000
			if op.dist < 0 {
				op.dist = 0
			}
			if rng.Intn(3) == 0 {
				op.dist = cfg.ShortTripMeters
			}
		default:
			op.kind = opLeave
		}
		ops = append(ops, op)
	}
	return ops
}

func queuesEqual(t *testing.T, p *Pool, m *naiveModel, terms []string, step int, logb *strings.Builder) {
	t.Helper()
	for _, term := range terms {
		snap, err := p.Snapshot(term)
		if err != nil {
			t.Fatalf("step %d Snapshot(%s): %v\n%s", step, term, err, logb.String())
		}
		nq := m.queues[term]
		if len(snap) != len(nq) {
			t.Fatalf("step %d 队列 %s 长度不一致: %d vs %d\n%s",
				step, term, len(snap), len(nq), logb.String())
		}
		for i := range snap {
			if snap[i].Driver != nq[i].driver || snap[i].Priority != nq[i].priority ||
				!snap[i].EnqAt.Equal(nq[i].enqAt) {
				t.Fatalf("step %d 队列 %s 位置 %d 不一致: (%s,%v,%v) vs (%s,%v,%v)\n%s",
					step, term, i,
					snap[i].Driver, snap[i].Priority, snap[i].EnqAt,
					nq[i].driver, nq[i].priority, nq[i].enqAt, logb.String())
			}
		}
	}
	// 司机状态比对。
	for id, nd := range m.drv {
		if !nd.registered {
			continue
		}
		v, err := p.Inspect(id)
		if err != nil {
			t.Fatalf("step %d Inspect(%s): %v\n%s", step, id, err, logb.String())
		}
		if v.State != nd.state || v.Terminal != nd.terminal ||
			v.NoShows != nd.noShows || !v.BanUntil.Equal(nd.banUntil) ||
			v.HasVoucher != (nd.voucher != nil && nd.voucher.alive) {
			t.Fatalf("step %d 司机 %s 状态不一致: real=%+v naive=%+v\n%s",
				step, id, v, nd, logb.String())
		}
		// 队列位置也必须一致（前方人数）。
		if nd.state == "queued" {
			pos, perr := p.Position(id)
			if perr != nil {
				t.Fatalf("step %d Position(%s): %v", step, id, perr)
			}
			np := 0
			for _, e := range m.queues[nd.terminal] {
				if e.driver == id {
					break
				}
				np++
			}
			if pos != np {
				t.Fatalf("step %d Position(%s): real=%d naive=%d\n%s",
					step, id, pos, np, logb.String())
			}
		}
	}
}

func TestRandomDifferential(t *testing.T) {
	if testing.Verbose() {
		t.Log("随机对照：每条操作的输入/输出/判定依据均打印到 -v 日志")
	}
	for seed := int64(1); seed <= 40; seed++ {
		rng := rand.New(rand.NewSource(seed))
		cfg := diffConfig(rng)
		p, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		m := newNaive(cfg)
		ops := generateOps(rng, cfg, 300)
		var terms []string
		for id := range cfg.Terminals {
			terms = append(terms, id)
		}
		sort.Strings(terms)
		var logb strings.Builder
		fmt.Fprintf(&logb, "==== seed=%d terms=%v cfg=%+v ====\n", seed, terms, cfg)
		for i, op := range ops {
			fmt.Fprintf(&logb, "[%03d] %s drv=%s term=%s at=%s",
				i, opName(op.kind), op.drv, op.term, op.at.Format("15:04:05"))
			if op.kind == opComplete {
				fmt.Fprintf(&logb, " dist=%.0f", op.dist)
			}
			logb.WriteString("\n")
			switch op.kind {
			case opRegister:
				e1 := p.RegisterDriver(op.drv, op.at)
				k2 := m.register(op.drv, op.at)
				assertKind(t, i, e1, k2, &logb)
			case opJoin:
				r1, e1 := p.Join(op.drv, op.term, op.at)
				k2, issued2, prio2 := m.join(op.drv, op.term, op.at)
				assertKind(t, i, e1, k2, &logb)
				if e1 == nil {
					if r1.VoucherIssued != issued2 || r1.Priority != prio2 {
						t.Fatalf("step %d Join 结果不一致: real=%+v naive(issued=%v,prio=%v)\n%s",
							i, r1, issued2, prio2, logb.String())
					}
					fmt.Fprintf(&logb, "    输出: pos=%d priority=%v issued=%v used=%v\n",
						r1.Position, r1.Priority, r1.VoucherIssued, r1.VoucherUsed)
				}
			case opDispatch:
				r1, e1 := p.Dispatch(op.term, op.at)
				k2, d2, src2, tr2, dl2 := m.dispatch(op.term, op.at)
				assertKind(t, i, e1, k2, &logb)
				if e1 == nil {
					if r1.Driver != d2 || r1.SourceTerminal != src2 ||
						r1.Transferred != tr2 || !r1.Deadline.Equal(dl2) {
						t.Fatalf("step %d Dispatch 不一致: real=%+v naive=(%s,%s,%v,%v)\n%s",
							i, r1, d2, src2, tr2, dl2, logb.String())
					}
					fmt.Fprintf(&logb, "    输出: driver=%s from=%s transferred=%v deadline=%s\n",
						r1.Driver, r1.SourceTerminal, r1.Transferred,
						r1.Deadline.Format("15:04:05"))
				}
			case opArrive:
				e1 := p.Arrive(op.drv, op.at)
				k2 := m.arrive(op.drv, op.at)
				assertKind(t, i, e1, k2, &logb)
			case opComplete:
				e1 := p.CompleteTrip(op.drv, op.at, op.dist)
				k2 := m.complete(op.drv, op.at, op.dist)
				assertKind(t, i, e1, k2, &logb)
			case opLeave:
				e1 := p.Leave(op.drv, op.at)
				k2 := m.leave(op.drv, op.at)
				assertKind(t, i, e1, k2, &logb)
			}
			queuesEqual(t, p, m, terms, i, &logb)
		}
		if testing.Verbose() {
			t.Log("\n" + logb.String())
		}
	}
}

func opName(k opKind) string {
	switch k {
	case opRegister:
		return "Register"
	case opJoin:
		return "Join"
	case opDispatch:
		return "Dispatch"
	case opArrive:
		return "Arrive"
	case opComplete:
		return "Complete"
	default:
		return "Leave"
	}
}

func assertKind(t *testing.T, step int, realErr error, naiveKind ErrorKind, logb *strings.Builder) {
	t.Helper()
	k1 := KindOf(realErr)
	if k1 != naiveKind {
		got, want := "成功", "成功"
		if realErr != nil {
			got = KindOf(realErr).Sentinel().Error()
		}
		if naiveKind != 0 {
			want = naiveKind.Sentinel().Error()
		}
		t.Fatalf("step %d 错误类别不一致: real=%s naive=%s\n%s", step, got, want, logb.String())
	}
	if realErr != nil {
		fmt.Fprintf(logb, "    输出: 拒绝(%s)\n", KindOf(realErr).Sentinel())
	} else if naiveKind == 0 {
		fmt.Fprintf(logb, "    输出: 接受\n")
	}
}
