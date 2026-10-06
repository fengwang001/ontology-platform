package settle_test

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/instr"
	"ontology/settle"
)

// --- 朴素模拟：按规则逐步直写，与被测实现相互对照。 ---

type simInstr struct {
	id, seller, buyer, sym  string
	qty, amount             int64
	sd, seq                 int
	d, paid                 int64
	status                  instr.Status
	sellerFault, buyerFault bool
}

type sim struct {
	U, rs, rb   int64
	A           int
	maxDay      int
	settledDays map[int]bool
	holdings    map[string]map[string]int64
	cash        map[string]int64
	payable     map[string]int64
	receivable  map[string]int64
	prices      map[string]int64
	order       []*simInstr
	byID        map[string]*simInstr
}

func newSim(U, rs, rb int64, A int) *sim {
	return &sim{
		U: U, rs: rs, rb: rb, A: A, maxDay: -1,
		settledDays: map[int]bool{},
		holdings:    map[string]map[string]int64{},
		cash:        map[string]int64{},
		payable:     map[string]int64{},
		receivable:  map[string]int64{},
		prices:      map[string]int64{},
		byID:        map[string]*simInstr{},
	}
}

func (s *sim) checkDay(day int) error {
	if day < 0 || day > 1_000_000 {
		return instr.ErrParam
	}
	if day < s.maxDay {
		return instr.ErrDate
	}
	return nil
}

func (s *sim) advance(day int) {
	if day > s.maxDay {
		s.maxDay = day
	}
}

func (s *sim) holding(acct, sym string) int64 {
	return s.holdings[acct][sym]
}

func (s *sim) addHolding(acct, sym string, delta int64) {
	h, ok := s.holdings[acct]
	if !ok {
		h = map[string]int64{}
		s.holdings[acct] = h
	}
	h[sym] += delta
}

func (s *sim) credit(day int, acct, sym string, qty int64) error {
	if acct == "" || sym == "" || qty < 1 || qty > 1_000_000_000_000 {
		return instr.ErrParam
	}
	if err := s.checkDay(day); err != nil {
		return err
	}
	s.advance(day)
	s.addHolding(acct, sym, qty)
	return nil
}

func (s *sim) creditCash(day int, acct string, amt int64) error {
	if acct == "" || amt < 1 || amt > 1_000_000_000_000 {
		return instr.ErrParam
	}
	if err := s.checkDay(day); err != nil {
		return err
	}
	s.advance(day)
	s.cash[acct] += amt
	return nil
}

func (s *sim) setPrice(day int, sym string, price int64) error {
	if sym == "" || price < 1 || price > 1_000_000 {
		return instr.ErrParam
	}
	if err := s.checkDay(day); err != nil {
		return err
	}
	s.advance(day)
	s.prices[sym] = price
	return nil
}

func (s *sim) instruct(day int, id, seller, buyer, sym string, qty, amount int64, sd int) error {
	if id == "" || seller == "" || buyer == "" || sym == "" ||
		qty < 1 || qty > 1_000_000_000 || qty%s.U != 0 ||
		amount < 1 || amount > 1_000_000_000_000_000 ||
		sd <= day || seller == buyer {
		return instr.ErrParam
	}
	if err := s.checkDay(day); err != nil {
		return err
	}
	if _, ok := s.byID[id]; ok {
		return instr.ErrDuplicate
	}
	if _, ok := s.prices[sym]; !ok {
		return instr.ErrNoPrice
	}
	s.advance(day)
	ins := &simInstr{
		id: id, seller: seller, buyer: buyer, sym: sym,
		qty: qty, amount: amount, sd: sd, seq: len(s.order),
		status: instr.Open,
	}
	s.byID[id] = ins
	s.order = append(s.order, ins)
	return nil
}

func (s *sim) cancel(day int, id string) error {
	if id == "" {
		return instr.ErrParam
	}
	if err := s.checkDay(day); err != nil {
		return err
	}
	ins, ok := s.byID[id]
	if !ok {
		return instr.ErrNotFound
	}
	if ins.status != instr.Open || day >= ins.sd {
		return instr.ErrState
	}
	s.advance(day)
	ins.status = instr.Cancelled
	return nil
}

// simFloorPay 累计交付 d 时累计应付 floor(amount*d/q)（小参数，int64 足够）。
func simFloorPay(amount, d, q int64) int64 {
	return amount * d / q
}

func simCeilPenalty(r, price, rate int64) int64 {
	return (r*price*rate + 9999) / 10000
}

func (s *sim) runSettle(day int) error {
	if err := s.checkDay(day); err != nil {
		return err
	}
	if s.settledDays[day] {
		return instr.ErrState
	}
	s.advance(day)
	s.settledDays[day] = true
	var due []*simInstr
	for _, ins := range s.order {
		if ins.status == instr.Open && ins.sd <= day {
			due = append(due, ins)
		}
	}
	sort.Slice(due, func(i, j int) bool {
		if due[i].sd != due[j].sd {
			return due[i].sd < due[j].sd
		}
		return due[i].seq < due[j].seq
	})
	for _, ins := range due {
		ins.sellerFault, ins.buyerFault = false, false
		r := ins.qty - ins.d
		a := s.holding(ins.seller, ins.sym) / s.U * s.U
		if a > r {
			a = r
		}
		// 朴素求 b：从大到小逐个 U 的倍数试，第一个付得起的即最大。
		bq := int64(0)
		for k := r / s.U * s.U; k > 0; k -= s.U {
			if simFloorPay(ins.amount, ins.d+k, ins.qty)-ins.paid <= s.cash[ins.buyer] {
				bq = k
				break
			}
		}
		k := a
		if bq < k {
			k = bq
		}
		if k > 0 {
			pay := simFloorPay(ins.amount, ins.d+k, ins.qty) - ins.paid
			s.addHolding(ins.seller, ins.sym, -k)
			s.addHolding(ins.buyer, ins.sym, k)
			s.cash[ins.buyer] -= pay
			s.cash[ins.seller] += pay
			ins.d += k
			ins.paid += pay
		}
		if k == r {
			ins.status = instr.Settled
			continue
		}
		ins.sellerFault = a < r
		ins.buyerFault = bq < r
		rAfter := ins.qty - ins.d
		price := s.prices[ins.sym]
		if ins.sellerFault {
			s.payable[ins.seller] += simCeilPenalty(rAfter, price, s.rs)
		}
		if ins.buyerFault {
			s.payable[ins.buyer] += simCeilPenalty(rAfter, price, s.rb)
		}
	}
	for _, ins := range due {
		if ins.status != instr.Open || day-ins.sd < s.A {
			continue
		}
		r := ins.qty - ins.d
		if ins.sellerFault {
			comp := r*s.prices[ins.sym] - (ins.amount - ins.paid)
			if comp < 0 {
				comp = 0
			}
			s.payable[ins.seller] += comp
			s.receivable[ins.buyer] += comp
			ins.status = instr.BoughtIn
		} else {
			ins.status = instr.Cancelled
		}
	}
	return nil
}

// --- 随机操作序列生成与对照 ---

type opKind int

const (
	opCredit opKind = iota
	opCreditCash
	opSetPrice
	opInstruct
	opCancel
	opRunSettle
)

type op struct {
	kind                         opKind
	day                          int
	id, acct, sym, seller, buyer string
	qty, amount, price           int64
	sd                           int
}

func (o op) String() string {
	switch o.kind {
	case opCredit:
		return fmt.Sprintf("Credit(day=%d acct=%s sym=%s qty=%d)", o.day, o.acct, o.sym, o.qty)
	case opCreditCash:
		return fmt.Sprintf("CreditCash(day=%d acct=%s amt=%d)", o.day, o.acct, o.qty)
	case opSetPrice:
		return fmt.Sprintf("SetPrice(day=%d sym=%s price=%d)", o.day, o.sym, o.price)
	case opInstruct:
		return fmt.Sprintf("Instruct(day=%d id=%s %s->%s sym=%s qty=%d amount=%d sd=%d)",
			o.day, o.id, o.seller, o.buyer, o.sym, o.qty, o.amount, o.sd)
	case opCancel:
		return fmt.Sprintf("Cancel(day=%d id=%s)", o.day, o.id)
	case opRunSettle:
		return fmt.Sprintf("RunSettle(day=%d)", o.day)
	}
	return "?"
}

var simAccounts = []string{"S", "B", "C", "D"}
var simSyms = []string{"X", "Y"}

// genOps 生成一条随机操作序列；day 单调为主，偶发回退/越界以覆盖拒绝路径。
func genOps(r *rand.Rand, U int64, n int) []op {
	ops := make([]op, 0, n)
	day := 0
	idSeq := 0
	for i := 0; i < n; i++ {
		switch x := r.Intn(100); {
		case x < 55:
			// 推进或保持日期。
			day += r.Intn(3)
		case x < 60:
			day-- // 偶发回退
		}
		kind := opCredit
		switch x := r.Intn(100); {
		case x < 20:
			kind = opCredit
		case x < 40:
			kind = opCreditCash
		case x < 50:
			kind = opSetPrice
		case x < 70:
			kind = opInstruct
		case x < 80:
			kind = opCancel
		default:
			kind = opRunSettle
		}
		o := op{kind: kind, day: day}
		o.acct = simAccounts[r.Intn(len(simAccounts))]
		o.sym = simSyms[r.Intn(len(simSyms))]
		switch kind {
		case opCredit, opCreditCash:
			o.qty = 1 + int64(r.Intn(60_000))
			if r.Intn(20) == 0 {
				o.qty = 0 // 偶发非法
			}
		case opSetPrice:
			o.price = 1 + int64(r.Intn(1000))
		case opInstruct:
			if r.Intn(10) > 0 || idSeq == 0 {
				o.id = fmt.Sprintf("id%d", idSeq)
				idSeq++
			} else {
				o.id = fmt.Sprintf("id%d", r.Intn(idSeq)) // 偶发重复
			}
			o.seller = simAccounts[r.Intn(len(simAccounts))]
			o.buyer = simAccounts[r.Intn(len(simAccounts))]
			if r.Intn(20) == 0 {
				o.buyer = o.seller // 偶发非法
			}
			o.qty = U * int64(1+r.Intn(30))
			if r.Intn(20) == 0 {
				o.qty++ // 偶发非 U 的倍数（U>1 时非法）
			}
			o.amount = 1 + int64(r.Intn(50_000))
			o.sd = day + 1 + r.Intn(3)
			if r.Intn(30) == 0 {
				o.sd = day // 偶发非法
			}
		case opCancel:
			if idSeq > 0 && r.Intn(10) < 7 {
				o.id = fmt.Sprintf("id%d", r.Intn(idSeq))
			} else {
				o.id = "ghost"
			}
		}
		ops = append(ops, o)
	}
	return ops
}

func applyOp(b *instr.Book, s *sim, o op) (realErr, simErr error) {
	switch o.kind {
	case opCredit:
		return b.Credit(o.day, o.acct, o.sym, o.qty), s.credit(o.day, o.acct, o.sym, o.qty)
	case opCreditCash:
		return b.CreditCash(o.day, o.acct, o.qty), s.creditCash(o.day, o.acct, o.qty)
	case opSetPrice:
		return b.SetPrice(o.day, o.sym, o.price), s.setPrice(o.day, o.sym, o.price)
	case opInstruct:
		return b.Instruct(o.day, o.id, o.seller, o.buyer, o.sym, o.qty, o.amount, o.sd),
			s.instruct(o.day, o.id, o.seller, o.buyer, o.sym, o.qty, o.amount, o.sd)
	case opCancel:
		return b.Cancel(o.day, o.id), s.cancel(o.day, o.id)
	case opRunSettle:
		return settle.Run(b, o.day), s.runSettle(o.day)
	}
	return nil, nil
}

// compareState 全量对照被测实现与朴素模拟的状态，返回首个差异的判定依据。
func compareState(b *instr.Book, s *sim) string {
	st := b.Snapshot()
	names := map[string]bool{}
	for name := range st.Accounts {
		names[name] = true
	}
	for name := range s.cash {
		names[name] = true
	}
	for name := range s.payable {
		names[name] = true
	}
	for name := range s.holdings {
		names[name] = true
	}
	for name := range names {
		av := acct(st, name)
		if av.Cash != s.cash[name] {
			return fmt.Sprintf("账户 %s 现金: got %d want %d", name, av.Cash, s.cash[name])
		}
		if av.Payable != s.payable[name] {
			return fmt.Sprintf("账户 %s 应付账: got %d want %d", name, av.Payable, s.payable[name])
		}
		if av.Receivable != s.receivable[name] {
			return fmt.Sprintf("账户 %s 应收账: got %d want %d", name, av.Receivable, s.receivable[name])
		}
		syms := map[string]bool{}
		for sym := range av.Holdings {
			syms[sym] = true
		}
		for sym := range s.holdings[name] {
			syms[sym] = true
		}
		for sym := range syms {
			if av.Holdings[sym] != s.holdings[name][sym] {
				return fmt.Sprintf("账户 %s 持券 %s: got %d want %d",
					name, sym, av.Holdings[sym], s.holdings[name][sym])
			}
		}
	}
	if len(st.Instructions) != len(s.order) {
		return fmt.Sprintf("指令数: got %d want %d", len(st.Instructions), len(s.order))
	}
	for i, ins := range st.Instructions {
		si := s.order[i]
		if ins.ID != si.id || ins.Delivered != si.d || ins.Paid != si.paid ||
			ins.Status != si.status || ins.Seq != si.seq ||
			ins.SellerFault != si.sellerFault || ins.BuyerFault != si.buyerFault {
			return fmt.Sprintf("指令 %s: got {d=%d paid=%d %v seq=%d fault=%v/%v} want {d=%d paid=%d %v seq=%d fault=%v/%v}",
				si.id, ins.Delivered, ins.Paid, ins.Status, ins.Seq, ins.SellerFault, ins.BuyerFault,
				si.d, si.paid, si.status, si.seq, si.sellerFault, si.buyerFault)
		}
	}
	return ""
}

// checkInvariants 校验被测实现的全局不变量，返回首个违例的判定依据。
func checkInvariants(b *instr.Book, U int64, credited map[string]int64, creditedCash int64) string {
	st := b.Snapshot()
	holdSum := map[string]int64{}
	var cashSum int64
	for name, av := range st.Accounts {
		if av.Cash < 0 {
			return fmt.Sprintf("账户 %s 现金为负: %d", name, av.Cash)
		}
		if av.Payable < 0 || av.Receivable < 0 {
			return fmt.Sprintf("账户 %s 应付/应收为负", name)
		}
		cashSum += av.Cash
		for sym, q := range av.Holdings {
			if q < 0 {
				return fmt.Sprintf("账户 %s 持券 %s 为负: %d", name, sym, q)
			}
			holdSum[sym] += q
		}
	}
	for sym, sum := range holdSum {
		if sum != credited[sym] {
			return fmt.Sprintf("标的 %s 持券之和 %d 不等于累计 Credit %d", sym, sum, credited[sym])
		}
	}
	if cashSum != creditedCash {
		return fmt.Sprintf("现金之和 %d 不等于累计 CreditCash %d", cashSum, creditedCash)
	}
	for _, ins := range st.Instructions {
		if ins.Delivered%U != 0 {
			return fmt.Sprintf("指令 %s 已交付 %d 非 U 的倍数", ins.ID, ins.Delivered)
		}
		if ins.Paid > ins.Amount || ins.Delivered > ins.Qty {
			return fmt.Sprintf("指令 %s 超付/超交: paid=%d amount=%d d=%d q=%d",
				ins.ID, ins.Paid, ins.Amount, ins.Delivered, ins.Qty)
		}
		if ins.Status == instr.Settled && (ins.Delivered != ins.Qty || ins.Paid != ins.Amount) {
			return fmt.Sprintf("指令 %s 已交收但 d=%d q=%d paid=%d amount=%d",
				ins.ID, ins.Delivered, ins.Qty, ins.Paid, ins.Amount)
		}
	}
	return ""
}

// TestRandomDifferential 1500 组随机操作序列与朴素模拟逐步对照，
// 日志打印输入、输出与判定依据。
func TestRandomDifferential(t *testing.T) {
	const sequences = 1500
	var statusCount [4]int
	var accepted, rejected int
	for seed := int64(0); seed < sequences; seed++ {
		r := rand.New(rand.NewSource(seed))
		U := []int64{1, 10, 100}[r.Intn(3)]
		rs := []int64{0, 10, 500}[r.Intn(3)]
		rb := []int64{0, 5, 250}[r.Intn(3)]
		A := 1 + r.Intn(3)
		b := newBook(t, U, rs, rb, A)
		s := newSim(U, rs, rb, A)
		// 开局设定两个标的的现价，使 Instruct 不因无现价被拒。
		for _, sym := range simSyms {
			if _, simErr := applyOp(b, s, op{kind: opSetPrice, day: 0, sym: sym, price: 1 + int64(r.Intn(1000))}); simErr != nil {
				t.Fatalf("seed=%d 初始 SetPrice(%s) 被拒: %v", seed, sym, simErr)
			}
		}
		ops := genOps(r, U, 40)
		credited := map[string]int64{}
		var creditedCash int64
		verbose := seed < 3 // 前 3 组全量打印日志
		if verbose {
			t.Logf("seq=%d 参数: U=%d rs=%d rb=%d A=%d", seed, U, rs, rb, A)
		}
		for i, o := range ops {
			realErr, simErr := applyOp(b, s, o)
			if realErr == nil {
				accepted++
			} else {
				rejected++
			}
			if !errors.Is(realErr, simErr) {
				t.Fatalf("seq=%d op=%d 输入=%s\n输出: got %v want %v\n判定依据: 拒绝类别须一致",
					seed, i, o, realErr, simErr)
			}
			if realErr == nil {
				switch o.kind {
				case opCredit:
					credited[o.sym] += o.qty
				case opCreditCash:
					creditedCash += o.qty
				}
			}
			if verbose {
				t.Logf("seq=%d op=%d 输入=%s 输出=%v", seed, i, o, realErr)
			}
			if diff := compareState(b, s); diff != "" {
				t.Fatalf("seq=%d op=%d 输入=%s\n判定依据: %s", seed, i, o, diff)
			}
			if inv := checkInvariants(b, U, credited, creditedCash); inv != "" {
				t.Fatalf("seq=%d op=%d 输入=%s\n不变量违例: %s", seed, i, o, inv)
			}
		}
		if verbose {
			t.Logf("seq=%d 完成 %d 步，状态一致", seed, len(ops))
		}
		for _, ins := range b.Snapshot().Instructions {
			statusCount[ins.Status]++
		}
	}
	t.Logf("汇总: 接受 %d 步, 拒绝 %d 步; 指令终态 open=%d settled=%d bought-in=%d cancelled=%d",
		accepted, rejected, statusCount[0], statusCount[1], statusCount[2], statusCount[3])
}

// TestReplayDeterminism 相同操作序列重放得到相同结果。
func TestReplayDeterminism(t *testing.T) {
	for seed := int64(100); seed < 110; seed++ {
		r := rand.New(rand.NewSource(seed))
		U := []int64{1, 10, 100}[r.Intn(3)]
		rs, rb := int64(10), int64(5)
		A := 1 + r.Intn(3)
		ops := genOps(r, U, 60)
		b1 := newBook(t, U, rs, rb, A)
		b2 := newBook(t, U, rs, rb, A)
		s1 := newSim(U, rs, rb, A)
		s2 := newSim(U, rs, rb, A)
		for _, o := range ops {
			applyOp(b1, s1, o)
			applyOp(b2, s2, o)
		}
		st1, st2 := b1.Snapshot(), b2.Snapshot()
		if fmt.Sprintf("%+v", st1) != fmt.Sprintf("%+v", st2) {
			t.Fatalf("seed=%d 重放结果不一致", seed)
		}
	}
}

// TestConcurrentOps 并发调用等价于某个串行顺序：无数据竞争，
// 且全局不变量（非负、守恒、交付单位、付清约束）始终成立。
func TestConcurrentOps(t *testing.T) {
	b := newBook(t, 10, 10, 5, 2)
	must(t, b.SetPrice(0, "X", 100))
	must(t, b.SetPrice(0, "Y", 50))
	var day atomic.Int64
	var creditSum atomic.Int64
	var cashSum atomic.Int64
	var wg sync.WaitGroup
	workers := []*struct {
		prefix string
		seed   int64
	}{
		{"w0", 1}, {"w1", 2}, {"w2", 3}, {"w3", 4},
	}
	for _, w := range workers {
		wg.Add(1)
		go func(prefix string, seed int64) {
			defer wg.Done()
			r := rand.New(rand.NewSource(seed))
			idSeq := 0
			for i := 0; i < 500; i++ {
				if r.Intn(10) == 0 {
					day.Add(1)
				}
				d := int(day.Load())
				acct := simAccounts[r.Intn(len(simAccounts))]
				sym := simSyms[r.Intn(len(simSyms))]
				switch r.Intn(6) {
				case 0:
					q := int64(1 + r.Intn(1000))
					if b.Credit(d, acct, sym, q) == nil {
						creditSum.Add(q)
					}
				case 1:
					q := int64(1 + r.Intn(5000))
					if b.CreditCash(d, acct, q) == nil {
						cashSum.Add(q)
					}
				case 2:
					_ = b.SetPrice(d, sym, int64(1+r.Intn(1000)))
				case 3:
					id := fmt.Sprintf("%s-%d", prefix, idSeq)
					idSeq++
					seller := simAccounts[r.Intn(len(simAccounts))]
					buyer := simAccounts[r.Intn(len(simAccounts))]
					qty := int64(10 * (1 + r.Intn(20)))
					_ = b.Instruct(d, id, seller, buyer, sym, qty,
						int64(1+r.Intn(10_000)), d+1+r.Intn(3))
				case 4:
					_ = b.Cancel(d, fmt.Sprintf("%s-%d", prefix, r.Intn(idSeq+1)))
				case 5:
					_ = settle.Run(b, d)
				}
			}
		}(w.prefix, w.seed)
	}
	wg.Wait()
	st := b.Snapshot()
	var holdSum, cashTotal int64
	for name, av := range st.Accounts {
		if av.Cash < 0 {
			t.Fatalf("账户 %s 现金为负: %d", name, av.Cash)
		}
		cashTotal += av.Cash
		for sym, q := range av.Holdings {
			if q < 0 {
				t.Fatalf("账户 %s 持券 %s 为负: %d", name, sym, q)
			}
			holdSum += q
		}
	}
	if holdSum != creditSum.Load() {
		t.Fatalf("持券总和 %d != 已接受 Credit 总和 %d", holdSum, creditSum.Load())
	}
	if cashTotal != cashSum.Load() {
		t.Fatalf("现金总和 %d != 已接受 CreditCash 总和 %d", cashTotal, cashSum.Load())
	}
	for _, ins := range st.Instructions {
		if ins.Delivered%10 != 0 || ins.Paid > ins.Amount || ins.Delivered > ins.Qty {
			t.Fatalf("指令 %s 不变量违例: %+v", ins.ID, ins)
		}
		if ins.Status == instr.Settled && (ins.Delivered != ins.Qty || ins.Paid != ins.Amount) {
			t.Fatalf("指令 %s 已交收但未付清: %+v", ins.ID, ins)
		}
	}
}
