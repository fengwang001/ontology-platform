package trade

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"slices"
	"sort"
	"sync"
	"testing"

	"ontology/lot"
	"ontology/mtm"
)

func mustBal(t *testing.T, e *Engine, acct string, want int64) {
	t.Helper()
	got, ok := e.Balance([]byte(acct))
	if !ok || got != want {
		t.Fatalf("余额(%s)=%d(ok=%v) 判据: 累计入金+盈亏-手续费=%d", acct, got, ok, want)
	}
}

func mustErr(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("err=%v 判据: errors.Is 应命中 %v", err, want)
	}
}

// TestSpecExample 逐步核对题面示例（含次日续例与恒等式）。
func TestSpecExample(t *testing.T) {
	e := New()
	sym, acct := []byte("IF"), []byte("A")
	if err := e.AddSymbol(1, sym, 10, 1000, 1, 1, 3); err != nil {
		t.Fatal(err)
	}
	if err := e.Deposit(1, acct, 100000); err != nil {
		t.Fatal(err)
	}
	if err := e.Open(2, acct, sym, lot.Long, 3000, 5); err != nil {
		t.Fatal(err)
	}
	mustBal(t, e, "A", 99985) // 手续费 ceil(150000/10000)=15
	if err := e.Open(3, acct, sym, lot.Long, 3010, 3); err != nil {
		t.Fatal(err)
	}
	mustBal(t, e, "A", 99975) // 手续费 ceil(9.03)=10

	mustErr(t, e.Close(4, acct, sym, lot.Long, 3020, 9, CloseToday), lot.ErrTodayShort)
	mustErr(t, e.Close(4, acct, sym, lot.Long, 3020, 1, CloseYesterday), lot.ErrYesterdayShort)
	mustBal(t, e, "A", 99975) // 被拒绝的操作不改任何状态

	if err := e.Close(4, acct, sym, lot.Long, 3020, 2, CloseAuto); err != nil {
		t.Fatal(err)
	}
	// 平第一批 2 手: 盈亏 (3020-3000)*2*10=400, 手续费 ceil(18.12)=19
	mustBal(t, e, "A", 100356)
	pos := e.book.Get(lot.Key{Acct: "A", Sym: "IF", Dir: lot.Long})
	if pos.Qy != 0 || len(pos.Today) != 2 || pos.Today[0] != (lot.Batch{Price: 3000, Qty: 3}) {
		t.Fatalf("今仓=%v 判据: 队首部分平仓后余 [{3000,3},{3010,3}]", pos.Today)
	}

	calls, err := e.Settle(5, sym, 3015)
	if err != nil {
		t.Fatal(err)
	}
	// 盯市: 第一批余 3 手 15*3*10=450, 第二批 5*3*10=150, 共 600
	mustBal(t, e, "A", 100956)
	if len(calls) != 0 {
		t.Fatalf("追保名单=%v 判据: 余额远大于保证金应为空", calls)
	}
	if pos.Qy != 6 || pos.Sp0 != 3015 || len(pos.Today) != 0 {
		t.Fatalf("Qy=%d Sp0=%d 判据: 今仓并入昨仓且 sp0=3015", pos.Qy, pos.Sp0)
	}
	if m := e.totalMargin("A").Int64(); m != 18090 {
		t.Fatalf("保证金=%d 判据: ceil(6*3015*10*1000/10000)=18090", m)
	}

	// 次日续例
	if err := e.Open(6, acct, sym, lot.Long, 3030, 2); err != nil {
		t.Fatal(err)
	}
	mustBal(t, e, "A", 100949) // 手续费 ceil(6.06)=7
	if err := e.Close(7, acct, sym, lot.Long, 3040, 7, CloseAuto); err != nil {
		t.Fatal(err)
	}
	// 平昨 6 手: 盈亏 25*6*10=1500, 手续费 ceil(18.24)=19
	// 平今 1 手: 盈亏 10*1*10=100, 手续费 ceil(9.12)=10
	mustBal(t, e, "A", 102520)
	// 恒等式: 第一批余 3 手盯市 450 + 平仓 25*3*10=750 共 1200 = (3040-3000)*3*10
	if got, want := 450+750, (3040-3000)*3*10; got != want {
		t.Fatalf("恒等式 %d != %d", got, want)
	}
	t.Logf("输入: 题面示例序列; 输出: 余额 102520; 判据: 题面手算值逐步一致")
}

// TestFeeSplitRounding 平昨与平今两部分手续费各取整一次, 与合并取整不同。
func TestFeeSplitRounding(t *testing.T) {
	e := New()
	sym, acct := []byte("S"), []byte("A")
	// mult=1, fy=ft=50: 每部分 100*1*1*50/10000=0.5 -> 各 ceil 得 1+1=2; 合并 ceil(1.0)=1
	if err := e.AddSymbol(1, sym, 1, 0, 0, 50, 50); err != nil {
		t.Fatal(err)
	}
	if err := e.Deposit(1, acct, 1000); err != nil {
		t.Fatal(err)
	}
	if err := e.Open(2, acct, sym, lot.Long, 100, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Settle(3, sym, 100); err != nil { // 1 手转为昨仓, sp0=100
		t.Fatal(err)
	}
	if err := e.Open(4, acct, sym, lot.Long, 100, 1); err != nil {
		t.Fatal(err)
	}
	if err := e.Close(5, acct, sym, lot.Long, 100, 2, CloseAuto); err != nil {
		t.Fatal(err)
	}
	mustBal(t, e, "A", 998) // 盈亏 0, 手续费 2; 若合并取整则为 999
	t.Logf("输入: 平昨1手+平今1手@100; 输出: 手续费 2; 判据: ceil(0.5)+ceil(0.5)=2 != ceil(1.0)=1")
}

// TestShortDirection 空头方向符号 s=-1。
func TestShortDirection(t *testing.T) {
	e := New()
	sym, acct := []byte("S"), []byte("A")
	if err := e.AddSymbol(1, sym, 10, 0, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := e.Deposit(1, acct, 1000); err != nil {
		t.Fatal(err)
	}
	if err := e.Open(2, acct, sym, lot.Short, 100, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Settle(3, sym, 90); err != nil {
		t.Fatal(err)
	}
	mustBal(t, e, "A", 1200) // 空头下跌盈利: -1*(90-100)*2*10=200
	if err := e.Close(4, acct, sym, lot.Short, 80, 2, CloseYesterday); err != nil {
		t.Fatal(err)
	}
	mustBal(t, e, "A", 1400) // -1*(80-90)*2*10=200
	t.Logf("输入: 空头 2 手 100->90->80; 输出: 余额 1400; 判据: s=-1 时跌价盈利")
}

// TestSettleIdentity 多次结算后, 各批平仓盈亏与历次盯市之和 = s*(平仓价-开仓价)*手数*mult。
func TestSettleIdentity(t *testing.T) {
	e := New()
	sym, acct := []byte("S"), []byte("A")
	if err := e.AddSymbol(1, sym, 10, 0, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := e.Deposit(1, acct, 100000); err != nil {
		t.Fatal(err)
	}
	if err := e.Open(2, acct, sym, lot.Long, 100, 3); err != nil {
		t.Fatal(err)
	}
	if err := e.Open(3, acct, sym, lot.Long, 105, 2); err != nil {
		t.Fatal(err)
	}
	var mtmSum int64
	for i, sp := range []int64{110, 95, 102} {
		before, _ := e.Balance(acct)
		if _, err := e.Settle(int64(4+i), sym, sp); err != nil {
			t.Fatal(err)
		}
		after, _ := e.Balance(acct)
		mtmSum += after - before
	}
	before, _ := e.Balance(acct)
	if err := e.Close(7, acct, sym, lot.Long, 103, 5, CloseYesterday); err != nil {
		t.Fatal(err)
	}
	after, _ := e.Balance(acct)
	closePnL := after - before
	got := mtmSum + closePnL
	want := int64((103-100)*3*10 + (103-105)*2*10) // 90-40=50
	if got != want {
		t.Fatalf("恒等式: 盯市和 %d + 平仓 %d = %d 判据: 应为 %d", mtmSum, closePnL, got, want)
	}
	mustBal(t, e, "A", 100050)
	t.Logf("输入: 两批开仓+三次结算+平仓; 输出: 盯市和 %d 平仓 %d; 判据: 望远镜求和=50", mtmSum, closePnL)
}

// TestMarginPerDirectionRounding 保证金按（账户，合约，方向）各取整一次再求和。
func TestMarginPerDirectionRounding(t *testing.T) {
	e := New()
	sym, acct := []byte("S"), []byte("A")
	if err := e.AddSymbol(1, sym, 1, 50, 0, 0, 0); err != nil { // mult=1, mr=50
		t.Fatal(err)
	}
	if err := e.Deposit(1, acct, 100); err != nil {
		t.Fatal(err)
	}
	if err := e.Open(2, acct, sym, lot.Long, 100, 1); err != nil {
		t.Fatal(err)
	}
	if err := e.Open(3, acct, sym, lot.Short, 100, 1); err != nil {
		t.Fatal(err)
	}
	// 每方向 ceil(100*50/10000)=ceil(0.5)=1, 求和 2; 账户级合并取整会是 ceil(1.0)=1
	if got := e.totalMargin("A").Int64(); got != 2 {
		t.Fatalf("保证金=%d 判据: 多空各取整再求和 ceil(0.5)+ceil(0.5)=2", got)
	}
}

// TestMarginCallBoundary 追保名单: 余额严格小于保证金才入名单, 恰等不入, 按账户字节序。
func TestMarginCallBoundary(t *testing.T) {
	e := New()
	sym := []byte("S")
	if err := e.AddSymbol(1, sym, 1, 200, 0, 0, 0); err != nil { // mult=1, mr=200
		t.Fatal(err)
	}
	for _, a := range []string{"b", "A", "a"} {
		if err := e.Deposit(1, []byte(a), 2); err != nil {
			t.Fatal(err)
		}
	}
	for _, a := range []string{"b", "A", "a"} {
		if err := e.Open(2, []byte(a), sym, lot.Long, 100, 1); err != nil {
			t.Fatal(err)
		}
	}
	// 保证金=ceil(100*200/10000)=2, 余额 2 恰等 -> 不入名单
	calls, err := e.Settle(3, sym, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 0 {
		t.Fatalf("追保名单=%v 判据: 余额恰等于保证金不入名单", calls)
	}
	// 结算价 99: 余额 1 < 保证金 ceil(99*200/10000)=2 -> 全部入名单, 按字节序
	calls, err = e.Settle(4, sym, 99)
	if err != nil {
		t.Fatal(err)
	}
	want := [][]byte{[]byte("A"), []byte("a"), []byte("b")}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("追保名单=%q 判据: 严格小于入名单且按字节序 %q", calls, want)
	}
	t.Logf("输入: 三账户各 1 手@100 结算 100/99; 输出: %q; 判据: 恰等不入, 字节序排序", calls)
}

// TestOpenFundsEquality 开仓资金取等通过, 差一不可。
func TestOpenFundsEquality(t *testing.T) {
	newEngine := func(deposit int64) *Engine {
		e := New()
		if err := e.AddSymbol(1, []byte("S"), 1, 100, 10, 0, 0); err != nil {
			t.Fatal(err)
		}
		if err := e.Deposit(1, []byte("A"), deposit); err != nil {
			t.Fatal(err)
		}
		return e
	}
	// 1 手@1000: 手续费 ceil(1000*10/10000)=1, 保证金 ceil(1000*100/10000)=10, 共需 11
	e := newEngine(11)
	if err := e.Open(2, []byte("A"), []byte("S"), lot.Long, 1000, 1); err != nil {
		t.Fatalf("取等应通过: %v", err)
	}
	mustBal(t, e, "A", 10)

	e2 := newEngine(10)
	mustErr(t, e2.Open(2, []byte("A"), []byte("S"), lot.Long, 1000, 1), ErrInsufficientFunds)
	mustBal(t, e2, "A", 10) // 拒绝后余额不变
	if pos := e2.book.Get(lot.Key{Acct: "A", Sym: "S", Dir: lot.Long}); pos != nil {
		t.Fatalf("拒绝后不应有持仓: %+v", pos)
	}
	// 拒绝不推进时钟: 相同 now 的后续操作仍可接受
	if err := e2.Deposit(2, []byte("A"), 1); err != nil {
		t.Fatalf("拒绝的 Open 不应推进时钟: %v", err)
	}
	if err := e2.Open(3, []byte("A"), []byte("S"), lot.Long, 1000, 1); err != nil {
		t.Fatalf("补足 1 后应通过: %v", err)
	}
}

// TestRejectionOrder 拒绝按次序只报第一个: 参数非法 > 时钟回退 > 不存在/重复 > 仓不足 > 资金不足。
func TestRejectionOrder(t *testing.T) {
	e := New()
	if err := e.AddSymbol(5, []byte("S"), 10, 1000, 1, 1, 1); err != nil {
		t.Fatal(err)
	}
	if err := e.Deposit(5, []byte("A"), 100); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		err  error
		want error
	}{
		{"参数非法先于时钟", e.Open(4, []byte("A"), []byte("S"), lot.Long, 100, 0), ErrInvalidParam},
		{"时钟先于账户不存在", e.Open(4, []byte("NOPE"), []byte("S"), lot.Long, 100, 1), ErrClock},
		{"时钟先于合约重复", e.AddSymbol(4, []byte("S"), 1, 0, 0, 0, 0), ErrClock},
		{"账户不存在", e.Open(6, []byte("NOPE"), []byte("S"), lot.Long, 100, 1), ErrAccountNotFound},
		{"合约重复", e.AddSymbol(6, []byte("S"), 1, 0, 0, 0, 0), ErrSymbolDuplicate},
		{"合约不存在先于仓不足", e.Close(6, []byte("A"), []byte("NOSYM"), lot.Long, 100, 1, CloseYesterday), ErrSymbolNotFound},
		{"昨仓不足", e.Close(6, []byte("A"), []byte("S"), lot.Long, 100, 1, CloseYesterday), lot.ErrYesterdayShort},
		{"今仓不足", e.Close(6, []byte("A"), []byte("S"), lot.Long, 100, 1, CloseToday), lot.ErrTodayShort},
		{"持仓不足", e.Close(6, []byte("A"), []byte("S"), lot.Long, 100, 1, CloseAuto), lot.ErrPositionShort},
		{"资金不足", e.Open(6, []byte("A"), []byte("S"), lot.Long, 100, 1), ErrInsufficientFunds},
	}
	for _, c := range cases {
		mustErr(t, c.err, c.want)
		t.Logf("输入: %s; 输出: %v; 判据: 拒绝次序只报第一个", c.name, c.err)
	}
	// 全部被拒, 时钟仍停在 5, 状态未变
	mustBal(t, e, "A", 100)
	if err := e.Deposit(5, []byte("A"), 1); err != nil {
		t.Fatalf("拒绝不应推进时钟: %v", err)
	}
	// 资金检查取等通过: 余额 101, 手续费 1, 保证金 100
	if err := e.Open(6, []byte("A"), []byte("S"), lot.Long, 100, 1); err != nil {
		t.Fatalf("取等应通过: %v", err)
	}
	mustBal(t, e, "A", 100)
}

// ---- 逐手记录的朴素模拟, 用于随机对照 ----

type simHand struct {
	basis int64
	today bool
}

type simKey struct {
	acct, sym string
	dir       lot.Direction
}

type sim struct {
	bal   map[string]int64
	exist map[string]bool
	pos   map[simKey][]simHand
	syms  map[string]mtm.Contract
}

func newSim() *sim {
	return &sim{
		bal:   make(map[string]int64),
		exist: make(map[string]bool),
		pos:   make(map[simKey][]simHand),
		syms:  make(map[string]mtm.Contract),
	}
}

// 与 mtm.CeilRate 独立的小数实现（随机用例数值小, 不溢出）。
func simCeil(amount, rate int64) int64 {
	if amount <= 0 || rate <= 0 {
		return 0
	}
	return (amount*rate + 9999) / 10000
}

func (s *sim) margin(acct string) int64 {
	var total int64
	for k, hands := range s.pos {
		if k.acct != acct {
			continue
		}
		c := s.syms[k.sym]
		var value int64
		for _, h := range hands {
			value += h.basis // 昨仓 basis 已是上一结算价, 今仓为开仓价
		}
		total += simCeil(value*c.Mult, c.Mr)
	}
	return total
}

func (s *sim) open(acct, sym string, dir lot.Direction, price, qty int64) error {
	if !s.exist[acct] {
		return ErrAccountNotFound
	}
	c := s.syms[sym]
	fee := simCeil(price*qty*c.Mult, c.Fo)
	k := simKey{acct, sym, dir}
	hands := s.pos[k]
	for i := int64(0); i < qty; i++ {
		hands = append(hands, simHand{basis: price, today: true})
	}
	s.pos[k] = hands
	if s.bal[acct]-fee < s.margin(acct) {
		s.pos[k] = hands[:len(hands)-int(qty)] // 全有或全无
		return ErrInsufficientFunds
	}
	s.bal[acct] -= fee
	return nil
}

func (s *sim) close(acct, sym string, dir lot.Direction, price, qty int64, mode CloseMode) error {
	if !s.exist[acct] {
		return ErrAccountNotFound
	}
	c := s.syms[sym]
	k := simKey{acct, sym, dir}
	hands := s.pos[k]
	var yCnt, tCnt int64
	for _, h := range hands {
		if h.today {
			tCnt++
		} else {
			yCnt++
		}
	}
	var needY, needT int64
	switch mode {
	case CloseYesterday:
		if yCnt < qty {
			return lot.ErrYesterdayShort
		}
		needY = qty
	case CloseToday:
		if tCnt < qty {
			return lot.ErrTodayShort
		}
		needT = qty
	case CloseAuto:
		if yCnt+tCnt < qty {
			return lot.ErrPositionShort
		}
		needY = qty
		if needY > yCnt {
			needY = yCnt
		}
		needT = qty - needY
	}
	sign := dir.Sign()
	var pnl, yClosed, tClosed int64
	rest := hands[:0]
	for _, h := range hands {
		switch {
		case !h.today && needY > 0:
			pnl += sign * (price - h.basis) * c.Mult
			needY--
			yClosed++
		case h.today && needT > 0:
			pnl += sign * (price - h.basis) * c.Mult
			needT--
			tClosed++
		default:
			rest = append(rest, h)
		}
	}
	s.pos[k] = rest
	fee := simCeil(price*yClosed*c.Mult, c.Fy) + simCeil(price*tClosed*c.Mult, c.Ft)
	s.bal[acct] += pnl - fee
	return nil
}

func (s *sim) settle(sym string, sp int64) []string {
	c := s.syms[sym]
	for k, hands := range s.pos {
		if k.sym != sym {
			continue
		}
		sign := k.dir.Sign()
		var pnl int64
		for i := range hands {
			pnl += sign * (sp - hands[i].basis) * c.Mult
			hands[i] = simHand{basis: sp}
		}
		s.pos[k] = hands
		s.bal[k.acct] += pnl
	}
	var calls []string
	for acct, bal := range s.bal {
		if bal < s.margin(acct) {
			calls = append(calls, acct)
		}
	}
	sort.Strings(calls)
	return calls
}

func sameErr(a, b error) bool {
	if a == nil || b == nil {
		return a == b
	}
	return errors.Is(a, b) && errors.Is(b, a)
}

// runSequence 生成第 seq 条随机操作序列并在引擎上执行;
// check 为真时逐步与逐手朴素模拟对照。返回两个账户的最终余额。
func runSequence(t *testing.T, seq int, check bool) map[string]int64 {
	t.Helper()
	rng := rand.New(rand.NewSource(int64(seq)*7919 + 13))
	e := New()
	sm := newSim()
	accts := []string{"A", "B"}
	symNames := []string{"S1", "S2"}
	var now int64
	for _, name := range symNames {
		c := mtm.Contract{
			Mult: 1 + rng.Int63n(4),
			Mr:   100 + rng.Int63n(2900),
			Fo:   rng.Int63n(50),
			Fy:   rng.Int63n(50),
			Ft:   rng.Int63n(50),
		}
		if err := e.AddSymbol(now, []byte(name), c.Mult, c.Mr, c.Fo, c.Fy, c.Ft); err != nil {
			t.Fatal(err)
		}
		sm.syms[name] = c
	}
	for op := 0; op < 40; op++ {
		now += rng.Int63n(3)
		acct := accts[rng.Intn(2)]
		sym := symNames[rng.Intn(2)]
		dir := lot.Direction(rng.Intn(2))
		price := int64(90 + rng.Intn(21))
		var desc string
		var engErr, simErr error
		switch kind := rng.Intn(100); {
		case kind < 15:
			amt := int64(1 + rng.Intn(100))
			if rng.Intn(10) < 7 {
				amt = 10000 + rng.Int63n(90000)
			}
			desc = fmt.Sprintf("Deposit(%s,%d)", acct, amt)
			engErr = e.Deposit(now, []byte(acct), amt)
			if engErr == nil {
				sm.bal[acct] += amt
				sm.exist[acct] = true
			}
		case kind < 50:
			qty := int64(1 + rng.Intn(5))
			desc = fmt.Sprintf("Open(%s,%s,dir=%d,%d,%d)", acct, sym, dir, price, qty)
			engErr = e.Open(now, []byte(acct), []byte(sym), dir, price, qty)
			simErr = sm.open(acct, sym, dir, price, qty)
		case kind < 75:
			qty := int64(1 + rng.Intn(10))
			mode := CloseMode(rng.Intn(3))
			desc = fmt.Sprintf("Close(%s,%s,dir=%d,%d,%d,mode=%d)", acct, sym, dir, price, qty, mode)
			engErr = e.Close(now, []byte(acct), []byte(sym), dir, price, qty, mode)
			simErr = sm.close(acct, sym, dir, price, qty, mode)
		default:
			sp := int64(85 + rng.Intn(31))
			desc = fmt.Sprintf("Settle(%s,%d)", sym, sp)
			var engCalls [][]byte
			engCalls, engErr = e.Settle(now, []byte(sym), sp)
			simCalls := sm.settle(sym, sp)
			if check && engErr == nil {
				got := make([]string, len(engCalls))
				for i, c := range engCalls {
					got[i] = string(c)
				}
				if !slices.Equal(got, simCalls) {
					t.Fatalf("seq=%d op=%d %s 追保名单=%v 判据: 逐手模拟=%v", seq, op, desc, got, simCalls)
				}
			}
		}
		if !check {
			continue
		}
		if !sameErr(engErr, simErr) {
			t.Fatalf("seq=%d op=%d now=%d 输入=%s 引擎err=%v 判据: 逐手模拟err=%v", seq, op, now, desc, engErr, simErr)
		}
		for _, a := range accts {
			got, _ := e.Balance([]byte(a))
			if got != sm.bal[a] {
				t.Fatalf("seq=%d op=%d 输入=%s 余额(%s)=%d 判据: 逐手模拟=%d", seq, op, desc, a, got, sm.bal[a])
			}
		}
		t.Logf("seq=%d op=%d now=%d 输入=%s 输出: err=%v 余额A=%d 余额B=%d 判据: 逐手朴素模拟一致",
			seq, op, now, desc, engErr, sm.bal["A"], sm.bal["B"])
	}
	final := make(map[string]int64)
	for _, a := range accts {
		final[a], _ = e.Balance([]byte(a))
	}
	return final
}

// TestRandomVsNaive 1500 组随机操作序列与逐手朴素模拟对照; 前 50 组重放验证确定性。
func TestRandomVsNaive(t *testing.T) {
	const seqs = 1500
	for seq := 0; seq < seqs; seq++ {
		first := runSequence(t, seq, true)
		if seq < 50 {
			if replay := runSequence(t, seq, false); !reflect.DeepEqual(first, replay) {
				t.Fatalf("seq=%d 重放 %v != 首次 %v 判据: 相同操作序列结果相同", seq, replay, first)
			}
		}
	}
	t.Logf("输入: %d 组随机序列; 输出: 全部一致; 判据: 逐手朴素模拟 + 重放确定性", seqs)
}

// TestConcurrent 并发调用等价于某个串行顺序（配合 -race）。
func TestConcurrent(t *testing.T) {
	e := New()
	if err := e.AddSymbol(0, []byte("S"), 1, 100, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	const n = 50
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			acct := []byte(fmt.Sprintf("acct%03d", i))
			for j := 0; j < 100; j++ {
				if err := e.Deposit(0, acct, 10); err != nil {
					t.Errorf("Deposit: %v", err)
				}
			}
			if err := e.Open(0, acct, []byte("S"), lot.Long, 100, 1); err != nil {
				t.Errorf("Open: %v", err)
			}
		}(i)
	}
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := e.Settle(0, []byte("S"), 100); err != nil {
				t.Errorf("Settle: %v", err)
			}
		}()
	}
	wg.Wait()
	for i := 0; i < n; i++ {
		acct := fmt.Sprintf("acct%03d", i)
		bal, ok := e.Balance([]byte(acct))
		if !ok || bal != 1000 {
			t.Fatalf("余额(%s)=%d 判据: 100 次入金 10 的串行等价结果 1000", acct, bal)
		}
	}
}
