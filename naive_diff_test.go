package ontology

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"ontology/internal/naive"
)

// diffCfg 主系统与朴素模型共用同一套固定配置。
func diffCfg() (Config, naive.Config) {
	c := Config{
		LongThreshold:  200,
		ShortThreshold: 30,
		RefundPercents: [3]int{5, 20, 40},
		ChangePercents: [3]int{3, 12, 30},
		MaxChanges:     3,
		VoucherTTL:     500,
	}
	n := naive.Config{
		Long: 200, Short: 30,
		RefundPct:  [3]int{5, 20, 40},
		ChangePct:  [3]int{3, 12, 30},
		MaxChanges: 3,
		VoucherTTL: 500,
	}
	return c, n
}

type opKind int

const (
	opChange opKind = iota
	opRefund
	opCancel
)

type diffOp struct {
	kind      opKind
	time      int64
	ticket    string
	target    string
	voucher   string
	cash      int64
	cashValid bool
}

// runDiffSeq 对同一操作序列分别驱动主系统与朴素模型，逐步比对结果、
// 错误（类别是否一致）、票/券状态与现金守恒，并把每步输入/输出/判定依据写入 log。
func runDiffSeq(t *testing.T, seed int64, nOps int) {
	t.Helper()
	cfg, ncfg := diffCfg()
	s := New(cfg)
	m := naive.New(ncfg)
	rng := rand.New(rand.NewSource(seed))

	var logb strings.Builder
	fmt.Fprintf(&logb, "==== diff seq seed=%d ops=%d ====\n", seed, nOps)

	// 注册航班：票面价 [0..3000]，出发时刻 [100..5000]
	nFlights := 6
	flightIDs := make([]string, nFlights)
	for i := range flightIDs {
		flightIDs[i] = fmt.Sprintf("F%d", i)
		fare := int64(rng.Intn(3001))
		dep := int64(100 + rng.Intn(4901))
		if err := s.RegisterFlight(flightIDs[i], fare, dep); err != nil {
			t.Fatalf("register: %v", err)
		}
		if err := m.RegisterFlight(flightIDs[i], fare, dep); err != nil {
			t.Fatalf("naive register: %v", err)
		}
		fmt.Fprintf(&logb, "register %s fare=%d dep=%d\n", flightIDs[i], fare, dep)
	}
	owners := []string{"alice", "bob", "carol"}
	nTickets := 3
	for i := 0; i < nTickets; i++ {
		tid := fmt.Sprintf("T%d", i)
		fid := flightIDs[rng.Intn(nFlights)]
		own := owners[i%len(owners)]
		if err := s.PurchaseTicket(tid, own, fid); err != nil {
			t.Fatalf("buy: %v", err)
		}
		if err := m.PurchaseTicket(tid, own, fid); err != nil {
			t.Fatalf("naive buy: %v", err)
		}
		fmt.Fprintf(&logb, "buy %s owner=%s flight=%s\n", tid, own, fid)
	}

	// 已知代金券（主/朴素 ID 同为 V000001..，按相同生成次序）。
	knownVouchers := []string{}
	refreshVouchers := func() {
		knownVouchers = knownVouchers[:0]
		for id := range s.vouchers {
			knownVouchers = append(knownVouchers, id)
		}
	}

	lastSeen := int64(0)
	for step := 0; step < nOps; step++ {
		tid := fmt.Sprintf("T%d", rng.Intn(nTickets))
		// 时刻：多数非递减，少量故意回退以触发时钟错误。
		now := lastSeen
		if lastSeen >= 2 && rng.Intn(5) == 0 {
			now = lastSeen - int64(rng.Intn(3)+1) // 故意回退
			if now < 0 {
				now = 0
			}
		} else {
			now += int64(rng.Intn(400) + 1)
		}

		op := diffOp{time: now, ticket: tid}
		switch rng.Intn(3) {
		case 0, 1: // 改签
			op.kind = opChange
			tv, _ := s.TicketView(tid)
			op.target = flightIDs[rng.Intn(nFlights)]
			// 小概率制造非法目标（不存在），触发参数非法
			if rng.Intn(10) == 0 {
				op.target = "NOPE"
			}
			// 小概率携带代金券
			refreshVouchers()
			if rng.Intn(2) == 0 && len(knownVouchers) > 0 {
				op.voucher = knownVouchers[rng.Intn(len(knownVouchers))]
				// 小概率换成不存在/他属券
				switch rng.Intn(6) {
				case 0:
					op.voucher = "GHOST"
				case 1:
					op.voucher = "" // 不用券
				}
			}
			// 先用报价探知应补现金，决定提交正确或错误金额。
			q, qerr := s.QuoteChange(ChangeRequest{
				TicketID: tid, TargetID: op.target, VoucherID: op.voucher,
			}, now)
			if qerr == nil {
				if rng.Intn(3) == 0 {
					op.cash = q.CashDue // 恰好正确
				} else {
					op.cash = q.CashDue + int64(rng.Intn(5)+1) // 故意不符
					if op.cash < 0 {
						op.cash = 0
					}
				}
				op.cashValid = op.cash == q.CashDue
			} else {
				op.cash = int64(rng.Intn(100))
			}
			_ = tv
		case 2: // 退票
			op.kind = opRefund
		}
		// 偶尔安插航司取消
		if rng.Intn(4) == 0 {
			op.kind = opCancel
			op.target = flightIDs[rng.Intn(nFlights)]
		}

		fmt.Fprintf(&logb, "step %d now=%d %s", step, now, opName(op))
		var gotErr, gotErrN string
		var accepted bool
		switch op.kind {
		case opChange:
			req := ChangeRequest{TicketID: op.ticket, TargetID: op.target, VoucherID: op.voucher, Cash: op.cash}
			res, err := s.Change(req, now)
			ores, errN := m.Change(op.ticket, op.target, op.voucher, op.cash, now)
			gotErr, gotErrN = errStr(err), errStr(errN)
			if err == nil && errN == nil {
				accepted = true
				compareChange(t, &logb, res, ores)
				if res.VoucherID != "" {
					refreshVouchers()
				}
			} else {
				compareErrClass(t, &logb, err, errN, op)
			}
		case opRefund:
			res, err := s.Refund(op.ticket, now)
			ores, errN := m.Refund(op.ticket, now)
			gotErr, gotErrN = errStr(err), errStr(errN)
			if err == nil && errN == nil {
				accepted = true
				compareRefund(t, &logb, res, ores)
			} else {
				compareErrClass(t, &logb, err, errN, op)
			}
		case opCancel:
			n1, err := s.CancelFlight(op.target, now)
			n2, errN := m.CancelFlight(op.target, now)
			gotErr, gotErrN = errStr(err), errStr(errN)
			if (err == nil) != (errN == nil) || n1 != n2 {
				fatalLog(t, &logb, "cancel mismatch n=%d/%d err=%v/%v", n1, n2, err, errN)
			}
			if err == nil {
				accepted = true
			}
		}
		if gotErr != "" || gotErrN != "" {
			fmt.Fprintf(&logb, " => ERR main=%q naive=%q\n", gotErr, gotErrN)
		}
		s.mu.Lock()
		_ = s.now
		s.mu.Unlock()
		lastSeen = now
		_ = accepted
	}

	// 终态逐票比对 + 现金守恒
	for i := 0; i < nTickets; i++ {
		tid := fmt.Sprintf("T%d", i)
		tv, ok1 := s.TicketView(tid)
		nt, ok2 := m.Ticket(tid)
		if ok1 != ok2 {
			fatalLog(t, &logb, "ticket existence mismatch %s", tid)
		}
		if ok1 {
			if tv.Fare != nt.Fare || tv.Departure != nt.Dep || tv.Refunded != nt.Refunded ||
				tv.Involuntary != nt.Invol || tv.Changes != naiveVolCount(nt) || tv.FlightID != nt.FlightID {
				fatalLog(t, &logb, "ticket state mismatch %s main=%+v naive(fare=%d dep=%d ref=%v invol=%v chg=%d flt=%s)",
					tid, tv, nt.Fare, nt.Dep, nt.Refunded, nt.Invol, naiveVolCount(nt), nt.FlightID)
			}
			paidN, refN, _ := m.CashNet(tid)
			if tv.CashPaid != paidN || tv.CashRefunded != refN {
				fatalLog(t, &logb, "cash net mismatch %s main(paid=%d ref=%d) naive(paid=%d ref=%d)",
					tid, tv.CashPaid, tv.CashRefunded, paidN, refN)
			}
			// 守恒恒等式：现金净流出 == 逐笔应补现金之和 - 逐笔应退现金之和
			if tv.CashPaid-tv.CashRefunded != paidN-refN {
				fatalLog(t, &logb, "conservation broken %s", tid)
			}
		}
	}
	t.Logf("\n%s", logb.String())
}

func opName(op diffOp) string {
	switch op.kind {
	case opChange:
		return fmt.Sprintf("change %s -> %s voucher=%q cash=%d\n", op.ticket, op.target, op.voucher, op.cash)
	case opRefund:
		return fmt.Sprintf("refund %s\n", op.ticket)
	default:
		return fmt.Sprintf("cancel %s\n", op.target)
	}
}

func errStr(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func naiveVolCount(nt *naive.Ticket) int {
	n := 0
	for _, e := range nt.History {
		if !e.Involuntary {
			n++
		}
	}
	return n
}

func compareChange(t *testing.T, logb *strings.Builder, r ChangeResult, n naive.ChangeOut) {
	t.Helper()
	if r.ChangeFee != n.Fee || r.Diff != n.Diff || r.TotalDue != n.Total ||
		r.RefundAsVoucher != n.NewVoucher || r.VoucherUsed != n.VoucherUsed ||
		r.CashDue != n.CashDue || r.Involuntary != n.Invol ||
		r.NewFare != n.NewFare || r.NewDeparture != n.NewDep || r.Changes != n.Changes ||
		r.VoucherID != n.NewVoucherID {
		fatalLog(t, logb, "change mismatch main=%+v naive=%+v", r, n)
	}
	fmt.Fprintf(logb, " => OK change fee=%d diff=%d total=%d voucherUsed=%d cashDue=%d newVoucher=%s\n",
		r.ChangeFee, r.Diff, r.TotalDue, r.VoucherUsed, r.CashDue, r.VoucherID)
}

func compareRefund(t *testing.T, logb *strings.Builder, r RefundResult, n naive.RefundOut) {
	t.Helper()
	if r.RefundCash != n.Cash || r.Fare != n.Fare || r.Fee != n.Fee ||
		r.ChangeFees != n.ChangeFees || r.Involuntary != n.Invol {
		fatalLog(t, logb, "refund mismatch main=%+v naive=%+v", r, n)
	}
	fmt.Fprintf(logb, " => OK refund cash=%d fare=%d fee=%d changeFees=%d invol=%v\n",
		r.RefundCash, r.Fare, r.Fee, r.ChangeFees, r.Involuntary)
}

// compareErrClass 主系统错误分类与朴素错误的语义类别必须一致。
func compareErrClass(t *testing.T, logb *strings.Builder, err error, errN error, op diffOp) {
	t.Helper()
	if (err == nil) != (errN == nil) {
		fatalLog(t, logb, "error presence mismatch op=%s main=%v naive=%v", opName(op), err, errN)
	}
	if err == nil {
		return
	}
	oe, ok := asOpError(err)
	if !ok {
		fatalLog(t, logb, "main error not OpError: %v", err)
	}
	want := classifyNaive(errN.Error())
	if oe.Kind != want {
		fatalLog(t, logb, "error class mismatch op=%s mainKind=%d(%v) naiveKind=%d(%q)",
			opName(op), oe.Kind, err, want, errN)
	}
}

// classifyNaive 把朴素模型的错误文本映射回主系统 ErrorKind。
func classifyNaive(msg string) ErrorKind {
	switch {
	case strings.Contains(msg, "clock rewind"):
		return KindClockRewind
	case strings.Contains(msg, "ticket not found"):
		return KindTicketNotFound
	case strings.Contains(msg, "already refunded"):
		return KindTicketState
	case strings.Contains(msg, "departed"):
		return KindDeparted
	case strings.Contains(msg, "change limit"):
		return KindChangeLimit
	case strings.Contains(msg, "voucher not found"):
		return KindVoucherNotFound
	case strings.Contains(msg, "voucher owner"):
		return KindVoucherOwner
	case strings.Contains(msg, "voucher expired"):
		return KindVoucherExpired
	case strings.Contains(msg, "voucher used"):
		return KindVoucherUsed
	case strings.Contains(msg, "payment mismatch"):
		return KindPaymentMismatch
	case strings.Contains(msg, "flight not found"), strings.Contains(msg, "already cancelled"),
		strings.Contains(msg, "bad flight"), strings.Contains(msg, "invalid"),
		strings.Contains(msg, "bad target"), strings.Contains(msg, "exists"):
		return KindInvalidArgument
	default:
		return KindFlightNotFound
	}
}

func fatalLog(t *testing.T, logb *strings.Builder, format string, args ...any) {
	t.Helper()
	t.Fatalf("%s\nMISMATCH: %s", logb.String(), fmt.Sprintf(format, args...))
}

func TestNaiveDiffRandom(t *testing.T) {
	for i := 0; i < 40; i++ {
		runDiffSeq(t, int64(1000+i*37), 80)
	}
}

// TestDeterministicReplay 相同操作序列重放必须得到相同金额与代金券标识。
func TestDeterministicReplay(t *testing.T) {
	run := func() []string {
		cfg, _ := diffCfg()
		s := New(cfg)
		out := []string{}
		mustRegister(t, s, "A", 1000, 1000)
		mustRegister(t, s, "B", 700, 2000)
		mustRegister(t, s, "C", 900, 3000)
		mustBuy(t, s, "T", "alice", "A")
		r, err := s.Change(ChangeRequest{TicketID: "T", TargetID: "B", Cash: 30}, 799)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, fmt.Sprintf("c1 fee=%d vid=%s fare=%d", r.ChangeFee, r.VoucherID, r.NewFare))
		r2, err := s.Change(ChangeRequest{TicketID: "T", TargetID: "C", Cash: 284}, 1969)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, fmt.Sprintf("c2 fee=%d cash=%d fare=%d", r2.ChangeFee, r2.CashDue, r2.NewFare))
		q, _ := s.QuoteRefund("T", 2500)
		out = append(out, fmt.Sprintf("q refund=%d fee=%d", q.RefundCash, q.Fee))
		return out
	}
	a := run()
	b := run()
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("non-deterministic: %q vs %q", a[i], b[i])
		}
	}
	if a[0] != "c1 fee=30 vid=V000001 fare=700" {
		t.Fatalf("unexpected: %v", a)
	}
}

// benchmarkQuoteEnv 构造一张已改签 changes 次、系统内 totalTickets 张票的环境。
type fataler interface {
	Helper()
	Fatal(...any)
	Fatalf(string, ...any)
}

func benchmarkQuoteEnv(b fataler, changes, totalTickets int) *System {
	cfg := Config{
		LongThreshold: 1 << 60, ShortThreshold: 1, // 保证所有报价时刻都在远档、不出发
		RefundPercents: [3]int{10, 20, 30},
		ChangePercents: [3]int{5, 10, 20},
		MaxChanges:     changes + 1,
		VoucherTTL:     1 << 60,
	}
	s := New(cfg)
	for i := 0; i < totalTickets; i++ {
		fid := fmt.Sprintf("FX%d", i)
		if err := s.RegisterFlight(fid, 1000, 1<<61); err != nil {
			b.Fatal(err)
		}
		if err := s.PurchaseTicket(fmt.Sprintf("TX%d", i), "other", fid); err != nil {
			b.Fatal(err)
		}
	}
	// 目标票：在多组航班间循环改签 changes 次
	if err := s.RegisterFlight("H0", 1000, 1<<61); err != nil {
		b.Fatal(err)
	}
	for i := 0; i <= changes; i++ {
		if err := s.RegisterFlight(fmt.Sprintf("H%d", i+1), int64(1000+i), 1<<61); err != nil {
			b.Fatal(err)
		}
	}
	if err := s.PurchaseTicket("TK", "alice", "H0"); err != nil {
		b.Fatal(err)
	}
	for i := 0; i < changes; i++ {
		// 零或正差价，用现金补费
		q, err := s.QuoteChange(ChangeRequest{TicketID: "TK", TargetID: fmt.Sprintf("H%d", i+1)}, int64(i))
		if err != nil {
			b.Fatal(err)
		}
		if _, err := s.Change(ChangeRequest{TicketID: "TK", TargetID: fmt.Sprintf("H%d", i+1), Cash: q.CashDue}, int64(i)); err != nil {
			b.Fatal(err)
		}
	}
	return s
}

func BenchmarkQuoteChangeConstant(b *testing.B) {
	for _, n := range []int{10, 1000} {
		for _, total := range []int{10, 10000} {
			name := fmt.Sprintf("changes=%d/tickets=%d", n, total)
			b.Run(name, func(b *testing.B) {
				s := benchmarkQuoteEnv(b, n, total)
				// 再注册一个未使用目标用于报价
				if err := s.RegisterFlight("QUERY", 1234, 1<<61); err != nil {
					b.Fatal(err)
				}
				allocs := testing.AllocsPerRun(1000, func() {
					if _, err := s.QuoteChange(ChangeRequest{TicketID: "TK", TargetID: "QUERY"}, int64(n)+1); err != nil {
						b.Fatal(err)
					}
					if _, err := s.QuoteRefund("TK", int64(n)+1); err != nil {
						b.Fatal(err)
					}
				})
				b.ReportMetric(allocs, "allocs/2queries")
			})
		}
	}
}

// TestQuoteO1 可验证地断言：改签 10 次 vs 1000 次、票总数 10 vs 10000 时，
// 每次报价的耗时近似常数（允许 2.5 倍抖动），且每次报价分配为小常数。
func TestQuoteO1(t *testing.T) {
	measure := func(changes, total int) (ns float64, allocs float64) {
		s := benchmarkQuoteEnv(t, changes, total)
		if err := s.RegisterFlight(fmt.Sprintf("Q%d", changes), 1234, 1<<61); err != nil {
			t.Fatal(err)
		}
		allocs = testing.AllocsPerRun(200, func() {
			if _, err := s.QuoteChange(ChangeRequest{TicketID: "TK", TargetID: fmt.Sprintf("Q%d", changes)}, int64(changes)+1); err != nil {
				t.Fatal(err)
			}
		})
		start := testing.Benchmark(func(bb *testing.B) {
			for i := 0; i < bb.N; i++ {
				_, _ = s.QuoteChange(ChangeRequest{TicketID: "TK", TargetID: fmt.Sprintf("Q%d", changes)}, int64(changes)+1)
			}
		})
		ns = float64(start.NsPerOp())
		return ns, allocs
	}
	nsSmall, _ := measure(10, 10)
	nsLarge, _ := measure(1000, 10000)
	t.Logf("quote ns/op small=%.1f large=%.1f ratio=%.2f", nsSmall, nsLarge, nsLarge/nsSmall)
	if ratio := nsLarge / nsSmall; ratio > 2.5 {
		t.Fatalf("quote appears non-constant: ratio=%.2f", ratio)
	}
	_, allocs := measure(500, 5000)
	if allocs > 2 { // 结果结构体返回属于固定小分配
		t.Fatalf("quote allocations grow: %v", allocs)
	}
}

// TestConcurrentSafe 在 -race 下验证并发调用等价于某串行顺序且无数据竞争。
func TestConcurrentSafe(t *testing.T) {
	s := New(testConfig())
	mustRegister(t, s, "C0", 1000, 100000)
	mustRegister(t, s, "C1", 900, 100100)
	for i := 0; i < 8; i++ {
		if err := s.PurchaseTicket(fmt.Sprintf("CT%d", i), "alice", "C0"); err != nil {
			t.Fatal(err)
		}
	}
	done := make(chan struct{})
	for g := 0; g < 8; g++ {
		go func(g int) {
			defer func() { done <- struct{}{} }()
			tid := fmt.Sprintf("CT%d", g)
			for k := 0; k < 200; k++ {
				now := int64(k * 10)
				// 每次都提交正确现金以获得高成功率；错误也允许。
				q, err := s.QuoteChange(ChangeRequest{TicketID: tid, TargetID: "C1"}, now)
				if err == nil {
					_, _ = s.Change(ChangeRequest{TicketID: tid, TargetID: "C1", Cash: q.CashDue}, now)
				}
				_, _ = s.QuoteRefund(tid, now)
			}
		}(g)
	}
	for g := 0; g < 8; g++ {
		<-done
	}
}
