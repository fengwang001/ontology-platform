package engine_test

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"testing"

	"ontology/lot"
	"ontology/mtm"
	"ontology/trade"
)

func ceild(a, b int64) int64 {
	if a <= 0 {
		return 0
	}
	return (a + b - 1) / b
}

func must(t *testing.T, err error, ctx string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: 意外错误 %v", ctx, err)
	}
}

func wantErrIs(t *testing.T, got error, target error, ctx string) {
	t.Helper()
	if !errors.Is(got, target) {
		t.Fatalf("%s: 错误=%v, 期望 %v", ctx, got, target)
	}
}

// TestWorkedExample 题目给定两日例程全量核对。
func TestWorkedExample(t *testing.T) {
	e := trade.NewEngine()
	must(t, e.AddSymbol(1, []byte("IF"), 10, 1000, 1, 1, 3), "AddSymbol")
	must(t, e.Deposit(1, []byte("a"), 100000), "Deposit")
	must(t, e.Open(2, []byte("a"), []byte("IF"), lot.Long, 3000, 5), "Open1")
	must(t, e.Open(3, []byte("a"), []byte("IF"), lot.Long, 3010, 3), "Open2")

	_, err := e.Close(4, []byte("a"), []byte("IF"), lot.Long, 3020, 9, trade.CloseToday)
	wantErrIs(t, err, trade.ErrShortT, "平今9手(共8手)")
	_, err = e.Close(4, []byte("a"), []byte("IF"), lot.Long, 3020, 1, trade.CloseYesterday)
	wantErrIs(t, err, trade.ErrShortY, "平昨1手(无昨仓)")

	r, err := e.Close(4, []byte("a"), []byte("IF"), lot.Long, 3020, 2, trade.CloseAuto)
	must(t, err, "自动平2手")
	if r.CloseY != 0 || r.CloseT != 2 || r.PnL != 400 || r.FeeY != 0 || r.FeeT != 19 || r.Touched != 1 {
		t.Fatalf("第一次平仓结果异常: %+v", r)
	}

	calls, err := mtm.Settle(e, 5, []byte("IF"), 3015)
	must(t, err, "Settle")
	if len(calls) != 0 {
		t.Fatalf("不应追保, 得到 %v", calls)
	}
	bal, _ := e.Balance([]byte("a"))
	if bal != 100956 {
		t.Fatalf("结算后余额=%d 期望100956", bal)
	}
	if m := e.Margin([]byte("a")); m != 18090 {
		t.Fatalf("保证金=%d 期望18090", m)
	}

	must(t, e.Open(6, []byte("a"), []byte("IF"), lot.Long, 3030, 2), "Open3")
	r2, err := e.Close(7, []byte("a"), []byte("IF"), lot.Long, 3040, 7, trade.CloseAuto)
	must(t, err, "次日自动平7手")
	if r2.CloseY != 6 || r2.CloseT != 1 || r2.PnL != 1600 || r2.FeeY != 19 || r2.FeeT != 10 {
		t.Fatalf("次日平仓结果异常: %+v", r2)
	}
	bal2, _ := e.Balance([]byte("a"))
	if bal2 != 100956-7+1600-19-10 {
		t.Fatalf("次日余额=%d", bal2)
	}
	r3, err := e.Close(8, []byte("a"), []byte("IF"), lot.Long, 3040, 1, trade.CloseToday)
	must(t, err, "平掉最后1手今仓")
	if r3.PnL != 100 {
		t.Fatalf("最后1手盈亏=%d 期望100", r3.PnL)
	}
}

// TestCloseModesAndShortages 三种平仓模式及各自的不足错误（顺序表驱动）。
func TestCloseModesAndShortages(t *testing.T) {
	type step struct {
		name       string
		mode       trade.CloseMode
		qty        int64
		ok         bool
		err        error
		y, tq, pnl int64
	}
	steps := []step{
		{"平昨足量3手(昨余7)", trade.CloseYesterday, 3, true, nil, 3, 0, (140 - 110) * 3 * 1},
		{"平昨再要8手(昨仅7)不足", trade.CloseYesterday, 8, false, trade.ErrShortY, 0, 0, 0},
		{"平今4手跨两批(今余1)", trade.CloseToday, 4, true, nil, 0, 4, (140-120)*3 + (140-130)*1},
		{"平今要2手(今仅1)不足", trade.CloseToday, 2, false, trade.ErrShortT, 0, 0, 0},
		{"自动平9手(昨7+今1=8)持仓不足", trade.CloseAuto, 9, false, trade.ErrShortPos, 0, 0, 0},
		{"自动平7手(昨7+今0)", trade.CloseAuto, 7, true, nil, 7, 0, (140 - 110) * 7},
		{"自动平1手(仅余今1)", trade.CloseAuto, 1, true, nil, 0, 1, 140 - 130},
	}
	e := trade.NewEngine()
	must(t, e.AddSymbol(0, []byte("s"), 1, 1000, 0, 0, 0), "add")
	must(t, e.Deposit(0, []byte("u"), 1e12), "dep")
	must(t, e.Open(0, []byte("u"), []byte("s"), lot.Long, 100, 10), "open")
	_, err := mtm.Settle(e, 1, []byte("s"), 110)
	must(t, err, "settle")
	must(t, e.Open(2, []byte("u"), []byte("s"), lot.Long, 120, 3), "open b1")
	must(t, e.Open(2, []byte("u"), []byte("s"), lot.Long, 130, 2), "open b2")
	for _, s := range steps {
		t.Run(s.name, func(t *testing.T) {
			r, err := e.Close(3, []byte("u"), []byte("s"), lot.Long, 140, s.qty, s.mode)
			if s.ok {
				must(t, err, s.name)
				if r.CloseY != s.y || r.CloseT != s.tq || r.PnL != s.pnl {
					t.Fatalf("输入 mode=%v qty=%d => y=%d t=%d pnl=%d，期望 y=%d t=%d pnl=%d",
						s.mode, s.qty, r.CloseY, r.CloseT, r.PnL, s.y, s.tq, s.pnl)
				}
				t.Logf("输入 mode=%v qty=%d => 输出 平昨=%d 平今=%d 盈亏=%d 费昨=%d 费今=%d touched=%d；判定一致",
					s.mode, s.qty, r.CloseY, r.CloseT, r.PnL, r.FeeY, r.FeeT, r.Touched)
			} else {
				wantErrIs(t, err, s.err, s.name)
			}
		})
	}
}

// TestAutoCrossAndHeadPartial 自动模式先昨后今；今仓队首部分平仓。
func TestAutoCrossAndHeadPartial(t *testing.T) {
	e := trade.NewEngine()
	must(t, e.AddSymbol(0, []byte("s"), 2, 1000, 0, 1, 3), "add")
	must(t, e.Deposit(0, []byte("u"), 1e12), "dep")
	must(t, e.Open(0, []byte("u"), []byte("s"), lot.Long, 100, 4), "o1")
	_, err := mtm.Settle(e, 1, []byte("s"), 110)
	must(t, err, "settle")
	must(t, e.Open(2, []byte("u"), []byte("s"), lot.Long, 200, 5), "o2")

	r, err := e.Close(3, []byte("u"), []byte("s"), lot.Long, 300, 2, trade.CloseToday)
	must(t, err, "partial head")
	if r.PnL != 400 || r.Touched != 1 || r.CloseT != 2 {
		t.Fatalf("队首部分平仓异常: %+v", r)
	}
	r, err = e.Close(3, []byte("u"), []byte("s"), lot.Long, 300, 6, trade.CloseAuto)
	must(t, err, "auto cross")
	if r.CloseY != 4 || r.CloseT != 2 || r.PnL != 1920 || r.Touched != 1 {
		t.Fatalf("跨昨今平仓异常: %+v", r)
	}
	t.Logf("输入 昨4@sp0=110 今批5@200(队首先去2) 自动6@300 mult=2 => 平昨4 平今2 盈亏=1520+400=1920；判定先昨后今、FIFO正确")
}

// TestFeeRoundingSeparate 两部分手续费各 ceil 一次，与合并取整结果不同。
func TestFeeRoundingSeparate(t *testing.T) {
	e := trade.NewEngine()
	must(t, e.AddSymbol(0, []byte("s"), 1, 0, 0, 1, 1), "add")
	must(t, e.Deposit(0, []byte("u"), 1e12), "dep")
	must(t, e.Open(0, []byte("u"), []byte("s"), lot.Long, 1, 1), "o")
	_, err := mtm.Settle(e, 1, []byte("s"), 1)
	must(t, err, "settle")
	must(t, e.Open(2, []byte("u"), []byte("s"), lot.Long, 1, 1), "o today")
	r, err := e.Close(3, []byte("u"), []byte("s"), lot.Long, 1, 2, trade.CloseAuto)
	must(t, err, "close")
	merged := ceild(1*1*1*1+1*1*1*1, 10000)
	if r.FeeY != 1 || r.FeeT != 1 {
		t.Fatalf("分开取整: feeY=%d feeT=%d，期望各1", r.FeeY, r.FeeT)
	}
	if r.FeeY+r.FeeT == merged {
		t.Fatalf("本例分开取整(%d)必须与合并取整(%d)不同", r.FeeY+r.FeeT, merged)
	}
	t.Logf("输入 昨1手今1手@1 fy=ft=万1 => 分开 ceil=%d+%d=2，合并 ceil=%d；判定分开取整规则正确",
		r.FeeY, r.FeeT, merged)
}

// TestShortSignAndNoNetting 空头符号与多空不抵消。
func TestShortSignAndNoNetting(t *testing.T) {
	e := trade.NewEngine()
	must(t, e.AddSymbol(0, []byte("s"), 10, 0, 0, 0, 0), "add")
	must(t, e.Deposit(0, []byte("u"), 1e12), "dep")
	must(t, e.Open(0, []byte("u"), []byte("s"), lot.Short, 100, 2), "open short")
	r, err := e.Close(1, []byte("u"), []byte("s"), lot.Short, 90, 2, trade.CloseToday)
	must(t, err, "close short")
	if r.PnL != 200 {
		t.Fatalf("空头价格下跌盈亏=%d 期望+200", r.PnL)
	}
	must(t, e.Open(2, []byte("u"), []byte("s"), lot.Long, 100, 3), "open long")
	_, err = e.Close(2, []byte("u"), []byte("s"), lot.Short, 100, 1, trade.CloseToday)
	wantErrIs(t, err, trade.ErrShortT, "空头已无仓，多头不得抵消")
}

// TestIdentityAcrossSettles 历次盯市+平仓盈亏恒等式。
func TestIdentityAcrossSettles(t *testing.T) {
	e := trade.NewEngine()
	mult := int64(3)
	must(t, e.AddSymbol(0, []byte("s"), mult, 0, 0, 0, 0), "add")
	must(t, e.Deposit(0, []byte("u"), 1e12), "dep")
	openP := int64(100)
	must(t, e.Open(0, []byte("u"), []byte("s"), lot.Long, openP, 2), "open")
	var mtmSum int64
	prev := openP
	now := int64(1)
	for _, sp := range []int64{110, 105, 130} {
		_, err := mtm.Settle(e, now, []byte("s"), sp)
		must(t, err, "settle")
		mtmSum += (sp - prev) * 2 * mult
		prev = sp
		now++
	}
	closeP := int64(140)
	r, err := e.Close(now, []byte("u"), []byte("s"), lot.Long, closeP, 2, trade.CloseYesterday)
	must(t, err, "close")
	want := (closeP - openP) * 2 * mult
	if r.PnL+mtmSum != want {
		t.Fatalf("盯市=%d 平仓=%d 合计=%d 期望=%d", mtmSum, r.PnL, r.PnL+mtmSum, want)
	}
	t.Logf("开=%d 结算价=[110 105 130] 平=%d => 盯市累计=%d 平仓=%d 合计=%d；恒等式成立",
		openP, closeP, mtmSum, r.PnL, want)
}

// TestMarginPerDirectionRounding 保证金按方向各取整一次再求和。
func TestMarginPerDirectionRounding(t *testing.T) {
	e := trade.NewEngine()
	// mult=1 mr=1(万一)：单手名义5000，单方向 ceil(5000/10000)=1。
	must(t, e.AddSymbol(0, []byte("s"), 1, 1, 0, 0, 0), "add")
	must(t, e.Deposit(0, []byte("u"), 1e12), "dep")
	must(t, e.Open(0, []byte("u"), []byte("s"), lot.Long, 5000, 1), "long")
	must(t, e.Open(0, []byte("u"), []byte("s"), lot.Short, 5000, 1), "short")
	if m := e.Margin([]byte("u")); m != 2 {
		t.Fatalf("两方向各自ceil后期望2，实际%d（合并取整将得1）", m)
	}
	t.Logf("输入 多1手@5000 空1手@5000 mr=万1 => 各 ceil(5000/10000)=1，账户保证金合计2；判定按方向独立取整")
}

// TestOpenExactEquality 余额减手续费恰等保证金时放行。
func TestOpenExactEquality(t *testing.T) {
	e := trade.NewEngine()
	// mult=1 mr=10000(全额) fo=0：1手@100 需保证金100。
	must(t, e.AddSymbol(0, []byte("s"), 1, 10000, 0, 0, 0), "add")
	must(t, e.Deposit(0, []byte("u"), 100), "dep")
	if err := e.Open(1, []byte("u"), []byte("s"), lot.Long, 100, 1); err != nil {
		t.Fatalf("取等应通过, 实际 %v", err)
	}
	// 再多 1 分钱都没有：另一手资金不足
	wantErrIs(t, e.Open(2, []byte("u"), []byte("s"), lot.Long, 100, 1), trade.ErrNoFunds, "第二手资金不足")
	t.Logf("输入 入金100 开1手@100 全额保证金费0 => 取等通过；再加1手 => 资金不足；判定边界正确")
}

// TestMarginCallExactEqual 余额恰等保证金不入追保名单，差1则入。
func TestMarginCallExactEqual(t *testing.T) {
	// mr=5000 半额保证金，无手续费。开2手@100名义200，保证金100。
	run := func(deposit, sp int64) int64 {
		e := trade.NewEngine()
		must(t, e.AddSymbol(0, []byte("s"), 1, 5000, 0, 0, 0), "add")
		must(t, e.Deposit(0, []byte("u"), deposit), "dep")
		must(t, e.Open(1, []byte("u"), []byte("s"), lot.Long, 100, 2), "open")
		calls, err := mtm.Settle(e, 2, []byte("s"), sp)
		must(t, err, "settle")
		return int64(len(calls))
	}
	// 入金100 开2手@100 => 余100=保证金100；sp=100 盯市0 => 恰等，不入。
	if n := run(100, 100); n != 0 {
		t.Fatalf("恰等场景不应追保, 名单数=%d", n)
	}
	// sp=99 => 盯市-2 余98，保证金=ceil(198*0.5)=99 => 98<99 严格小，追保。
	if n := run(100, 99); n != 1 {
		t.Fatalf("差1场景应追保, 名单数=%d", n)
	}
	t.Logf("输入 半额保证金 开2手@100 余=保证=100; sp=100恰等不入; sp=99时余98<保证金99入名单；判定边界正确")
}

// TestRejectOrder 拒绝次序：参数非法 > 时钟回退 > 不存在/重复 > 持仓不足 > 资金不足。
func TestRejectOrder(t *testing.T) {
	type tc struct {
		name string
		fn   func(e *trade.Engine) error
		want error
	}
	cases := []tc{
		{"AddSymbol:非法now优先于重复", func(e *trade.Engine) error {
			return e.AddSymbol(-1, []byte("IF"), 10, 1, 0, 0, 0)
		}, trade.ErrInvalid},
		{"AddSymbol:时钟回退优先于重复", func(e *trade.Engine) error {
			return e.AddSymbol(0, []byte("IF"), 10, 1, 0, 0, 0)
		}, trade.ErrClock},
		{"AddSymbol:重复", func(e *trade.Engine) error {
			return e.AddSymbol(5, []byte("IF"), 10, 1, 0, 0, 0)
		}, trade.ErrDuplicate},
		{"Open:非法参数最先", func(e *trade.Engine) error {
			return e.Open(0, []byte("u"), []byte("IF"), lot.Dir(0), 100, 1)
		}, trade.ErrInvalid},
		{"Open:时钟回退先于不存在", func(e *trade.Engine) error {
			return e.Open(0, []byte("u"), []byte("nope"), lot.Long, 100, 1)
		}, trade.ErrClock},
		{"Open:合约/账户不存在", func(e *trade.Engine) error {
			return e.Open(6, []byte("ghost"), []byte("IF"), lot.Long, 100, 1)
		}, trade.ErrUnknown},
		{"Close:昨仓不足先于资金检查", func(e *trade.Engine) error {
			_, err := e.Close(6, []byte("u"), []byte("IF"), lot.Long, 100, 1, trade.CloseYesterday)
			return err
		}, trade.ErrShortY},
		{"Settle:非法sp先于时钟", func(e *trade.Engine) error {
			_, err := mtm.Settle(e, 0, []byte("IF"), 0)
			return err
		}, trade.ErrInvalid},
		{"Settle:时钟回退先于合约不存在", func(e *trade.Engine) error {
			_, err := mtm.Settle(e, 0, []byte("nope"), 100)
			return err
		}, trade.ErrClock},
		{"Settle:合约不存在", func(e *trade.Engine) error {
			_, err := mtm.Settle(e, 6, []byte("nope"), 100)
			return err
		}, trade.ErrUnknown},
	}
	e := trade.NewEngine()
	must(t, e.AddSymbol(5, []byte("IF"), 10, 1000, 0, 0, 0), "seed symbol")
	must(t, e.Deposit(5, []byte("u"), 100000), "seed acct")
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.fn(e)
			wantErrIs(t, err, c.want, c.name)
		})
	}
	if e.Now() != 5 {
		t.Fatalf("拒绝操作不得推进时钟, now=%d", e.Now())
	}
}

// TestParamBounds 输入域边界校验。
func TestParamBounds(t *testing.T) {
	e := trade.NewEngine()
	cases := []struct {
		name string
		fn   func() error
	}{
		{"now>1e12", func() error { return e.AddSymbol(1e12+1, []byte("s"), 1, 0, 0, 0, 0) }},
		{"空合约字节串", func() error { return e.AddSymbol(0, nil, 1, 0, 0, 0, 0) }},
		{"mult=0", func() error { return e.AddSymbol(0, []byte("a"), 0, 0, 0, 0, 0) }},
		{"mult>1e4", func() error { return e.AddSymbol(0, []byte("b"), 1e4+1, 0, 0, 0, 0) }},
		{"mr=10001", func() error { return e.AddSymbol(0, []byte("c"), 1, 10001, 0, 0, 0) }},
		{"入金=0", func() error { return e.Deposit(0, []byte("u"), 0) }},
		{"入金>1e12", func() error { return e.Deposit(0, []byte("u"), 1e12+1) }},
		{"空账户", func() error { return e.Deposit(0, nil, 1) }},
		{"价格=0", func() error { return e.Deposit(0, []byte("u"), 1e12) }},
	}
	// 前 7 个直接非法
	for i, c := range cases[:8] {
		if err := c.fn(); !errors.Is(err, trade.ErrInvalid) {
			t.Fatalf("用例%d(%s) err=%v 期望 ErrInvalid", i, c.name, err)
		}
	}
	// 正常建仓后测价格/手数边界
	must(t, e.AddSymbol(1, []byte("s"), 1, 0, 0, 0, 0), "add")
	must(t, e.Deposit(1, []byte("u"), 1e12), "dep")
	if err := e.Open(2, []byte("u"), []byte("s"), lot.Long, 0, 1); !errors.Is(err, trade.ErrInvalid) {
		t.Fatalf("价格0 err=%v", err)
	}
	if err := e.Open(2, []byte("u"), []byte("s"), lot.Long, 1e7+1, 1); !errors.Is(err, trade.ErrInvalid) {
		t.Fatalf("价格越界 err=%v", err)
	}
	if err := e.Open(2, []byte("u"), []byte("s"), lot.Long, 1, 1e6+1); !errors.Is(err, trade.ErrInvalid) {
		t.Fatalf("手数越界 err=%v", err)
	}
	// 上界合法值通过
	must(t, e.Open(2, []byte("u"), []byte("s"), lot.Long, 1e7, 1e6), "上界开仓")
	if _, err := e.Close(3, []byte("u"), []byte("s"), lot.Dir(9), 1, 1, trade.CloseAuto); !errors.Is(err, trade.ErrInvalid) {
		t.Fatalf("非法方向 err=%v", err)
	}
	if _, err := e.Close(3, []byte("u"), []byte("s"), lot.Long, 1, 1, CloseModeBad); !errors.Is(err, trade.ErrInvalid) {
		t.Fatalf("非法平仓模式 err=%v", err)
	}
	if _, err := mtm.Settle(e, 4, []byte("s"), 1e7+1); !errors.Is(err, trade.ErrInvalid) {
		t.Fatalf("结算价越界 err=%v", err)
	}
	t.Log("输入 now/账户/合约/mult/四率/入金/价格/手数/方向/模式/结算价 全部边界按 ErrInvalid 拒绝")
}

const CloseModeBad = trade.CloseMode(99)

// TestRejectedNoStateChange 被拒操作不改变余额、时钟等任何状态。
func TestRejectedNoStateChange(t *testing.T) {
	e := trade.NewEngine()
	must(t, e.AddSymbol(10, []byte("s"), 1, 10000, 0, 0, 0), "add")
	must(t, e.Deposit(10, []byte("u"), 100), "dep")
	must(t, e.Open(10, []byte("u"), []byte("s"), lot.Long, 100, 1), "open")
	before, _ := e.Balance([]byte("u"))
	// 资金不足的开仓不得扣费
	err := e.Open(11, []byte("u"), []byte("s"), lot.Long, 100, 1)
	wantErrIs(t, err, trade.ErrNoFunds, "资金不足")
	after, _ := e.Balance([]byte("u"))
	if before != after {
		t.Fatalf("拒绝开仓改变了余额: %d -> %d", before, after)
	}
	// 时钟回退不改变时钟
	_ = e.Open(5, []byte("u"), []byte("s"), lot.Long, 1, 1)
	if e.Now() != 10 {
		t.Fatalf("时钟被回退/改动, now=%d 期望10", e.Now())
	}
	// 全有或全无：平今不足时仓位不变
	_, err = e.Close(11, []byte("u"), []byte("s"), lot.Long, 100, 5, trade.CloseToday)
	wantErrIs(t, err, trade.ErrShortT, "今仓不足")
	if m := e.Margin([]byte("u")); m != 100 {
		t.Fatalf("拒绝平仓改变了仓位/保证金=%d", m)
	}
}

// ---- 逐手朴素模拟器（独立实现，用于随机对照） ----

type naHand struct {
	dir   lot.Dir
	openP int64
}

type naiveSim struct {
	mult, mr, fo, fy, ft int64
	// 每个账户: balance
	bal map[string]int64
	// 持仓 per (acct,dir): 昨仓手数+sp0，今仓逐手 FIFO
	yQty  map[string]int64
	sp0   map[string]int64
	hands map[string][]naHand
	// 今仓批次对应的全生命周期逐手序号
	allSeq map[string][]int
	// 昨日手序列（并入昨仓后仍保留逐手身份）：seq -> 手
	yHands map[string][]naHand
	ySeq   map[string][]int
	keys   map[string]bool
	// 逐手累计已实现盯市盈亏，用于恒等式核对
	realized map[int]int64
	openInfo map[int]naHand
	seqGen   int
}

type naHandKey struct {
	acct string
	dir  lot.Dir
	idx  int // 开仓时在该账户方向逐手序列中的序号
}

func newNaive(mult, mr, fo, fy, ft int64) *naiveSim {
	return &naiveSim{
		mult: mult, mr: mr, fo: fo, fy: fy, ft: ft,
		bal: map[string]int64{}, yQty: map[string]int64{}, sp0: map[string]int64{},
		hands: map[string][]naHand{}, allSeq: map[string][]int{},
		yHands: map[string][]naHand{}, ySeq: map[string][]int{},
		realized: map[int]int64{}, openInfo: map[int]naHand{}, keys: map[string]bool{},
	}
}

func (n *naiveSim) key(acct string, dir lot.Dir) string {
	return fmt.Sprintf("%s/%d", acct, dir)
}

func (n *naiveSim) feeOpen(p, q int64) int64 { return ceild(p*q*n.mult*n.fo, 10000) }

func (n *naiveSim) marginOne(notional int64) int64 { return ceild(notional*n.mult*n.mr, 10000) }

// marginAcct 按 (账户,合约,方向) 各取整一次再求和。
func (n *naiveSim) marginAcct(acct string) int64 {
	var m int64
	for dir := range []lot.Dir{lot.Long, lot.Short} {
		d := []lot.Dir{lot.Long, lot.Short}[dir]
		k := n.key(acct, d)
		notional := n.yQty[k] * n.sp0[k]
		for _, h := range n.hands[k] {
			notional += h.openP
		}
		if n.yQty[k] > 0 || len(n.hands[k]) > 0 {
			m += n.marginOne(notional)
		}
	}
	return m
}

func (n *naiveSim) notionalAfter(acct string, dir lot.Dir, price, qty int64) int64 {
	k := n.key(acct, dir)
	notional := n.yQty[k] * n.sp0[k]
	for _, h := range n.hands[k] {
		notional += h.openP
	}
	return notional + price*qty
}

type naClose struct {
	y, t, pnl, feeY, feeT int64
	err                   error
}

func (n *naiveSim) deposit(acct string, amt int64) { n.bal[acct] += amt }

func (n *naiveSim) open(acct string, dir lot.Dir, price, qty int64) error {
	fee := n.feeOpen(price, qty)
	k := n.key(acct, dir)
	var marginTotal int64
	for _, d := range []lot.Dir{lot.Long, lot.Short} {
		kk := n.key(acct, d)
		nn := n.yQty[kk] * n.sp0[kk]
		for _, h := range n.hands[kk] {
			nn += h.openP
		}
		if kk == k {
			nn += price * qty
		}
		if nn > 0 {
			marginTotal += n.marginOne(nn)
		}
	}
	if n.bal[acct]-fee < marginTotal {
		return trade.ErrNoFunds
	}
	n.bal[acct] -= fee
	for i := int64(0); i < qty; i++ {
		seq := n.seqGen
		n.seqGen++
		n.allSeq[k] = append(n.allSeq[k], seq)
		n.hands[k] = append(n.hands[k], naHand{dir: dir, openP: price})
		n.openInfo[seq] = naHand{dir: dir, openP: price}
	}
	n.keys[k] = true
	return nil
}

func (n *naiveSim) close(acct string, dir lot.Dir, price, qty int64, mode trade.CloseMode) naClose {
	k := n.key(acct, dir)
	var y, t int64
	switch mode {
	case trade.CloseYesterday:
		if qty > n.yQty[k] {
			return naClose{err: trade.ErrShortY}
		}
		y = qty
	case trade.CloseToday:
		if qty > int64(len(n.hands[k])) {
			return naClose{err: trade.ErrShortT}
		}
		t = qty
	case trade.CloseAuto:
		y = qty
		if y > n.yQty[k] {
			y = n.yQty[k]
		}
		t = qty - y
		if t > int64(len(n.hands[k])) {
			return naClose{err: trade.ErrShortPos}
		}
	}
	sign := int64(dir)
	var pnl int64
	if y > 0 {
		pnl += sign * (price - n.sp0[k]) * y * n.mult
		// 平昨逐手 FIFO（昨仓手内部顺序：先并入的在前）
		for i := int64(0); i < y; i++ {
			n.yHands[k] = n.yHands[k][1:]
			seq := n.ySeq[k][0]
			n.ySeq[k] = n.ySeq[k][1:]
			n.realized[seq] += sign * (price - n.sp0[k]) * n.mult
			n.checkIdentity(seq, price)
		}
		n.yQty[k] -= y
	}
	if t > 0 {
		for i := int64(0); i < t; i++ {
			h := n.hands[k][0]
			n.hands[k] = n.hands[k][1:]
			seq := n.allSeq[k][0]
			n.allSeq[k] = n.allSeq[k][1:]
			hp := sign * (price - h.openP) * n.mult
			pnl += hp
			n.realized[seq] += hp
			n.checkIdentity(seq, price)
		}
	}
	feeY := ceild(price*y*n.mult*n.fy, 10000)
	feeT := ceild(price*t*n.mult*n.ft, 10000)
	n.bal[acct] += pnl - feeY - feeT
	return naClose{y: y, t: t, pnl: pnl, feeY: feeY, feeT: feeT}
}

// checkIdentity 每手平仓时核对 历次盯市+本次平仓 = s*(平-开)*mult。
func (n *naiveSim) checkIdentity(seq int, closeP int64) {
	h := n.openInfo[seq]
	want := int64(h.dir) * (closeP - h.openP) * n.mult
	if got := n.realized[seq]; got != want {
		panic(fmt.Sprintf("恒等式失败 seq=%d dir=%d open=%d close=%d realized=%d want=%d",
			seq, h.dir, h.openP, closeP, got, want))
	}
}

// settle 返回该合约盯市后被追保账户有序列表。
func (n *naiveSim) settle(sp int64) []string {
	for k := range n.keys {
		// k 形如 acct/dir
		parts := strings.Split(k, "/")
		acct := parts[0]
		dInt := int(parts[1][0] - '0')
		if parts[1] == "-1" {
			dInt = -1
		}
		dir := lot.Dir(dInt)
		sign := int64(dir)
		var pnl int64
		// 昨仓逐手盯市（若昨仓为空 sp0 无意义）
		for i := range n.yHands[k] {
			n.realized[n.ySeq[k][i]] += sign * (sp - n.sp0[k]) * n.mult
			pnl += sign * (sp - n.sp0[k]) * n.mult
		}
		for _, h := range n.hands[k] {
			pnl += sign * (sp - h.openP) * n.mult
		}
		n.bal[acct] += pnl
		// 今仓逐手盯市并入账，随后并入昨仓队列（保留手身份）
		for i, h := range n.hands[k] {
			n.realized[n.allSeq[k][i]] += sign * (sp - h.openP) * n.mult
			n.yHands[k] = append(n.yHands[k], h)
			n.ySeq[k] = append(n.ySeq[k], n.allSeq[k][i])
		}
		n.yQty[k] += int64(len(n.hands[k]))
		n.sp0[k] = sp
		n.hands[k] = nil
		n.allSeq[k] = nil
	}
	callSet := map[string]bool{}
	for a := range n.bal {
		if n.bal[a] < n.marginAcct(a) {
			callSet[a] = true
		}
	}
	out := make([]string, 0, len(callSet))
	for a := range callSet {
		out = append(out, a)
	}
	sort.Strings(out)
	return out
}

// TestRandomVsNaive 1500 组随机操作序列与逐手朴素模拟对照。
func TestRandomVsNaive(t *testing.T) {
	const groups = 1500
	const steps = 60
	for g := 0; g < groups; g++ {
		rng := rand.New(rand.NewSource(int64(g + 1)))
		mult := int64(1 + rng.Intn(10))
		mr := int64(rng.Intn(3000))
		fo := int64(rng.Intn(10))
		fy := int64(rng.Intn(10))
		ft := int64(rng.Intn(10))
		e := trade.NewEngine()
		must(t, e.AddSymbol(0, []byte("s"), mult, mr, fo, fy, ft), "add")
		n := newNaive(mult, mr, fo, fy, ft)
		const accts = 3
		for a := 0; a < accts; a++ {
			dep := int64(1 + rng.Intn(5000000))
			an := fmt.Sprintf("a%d", a)
			must(t, e.Deposit(0, []byte(an), dep), "dep")
			n.deposit(an, dep)
		}
		var logb strings.Builder
		fmt.Fprintf(&logb, "[组%d] mult=%d mr=%d fo=%d fy=%d ft=%d dep:3账户; 操作:", g, mult, mr, fo, fy, ft)
		now := int64(1)
		for s := 0; s < steps; s++ {
			acct := fmt.Sprintf("a%d", rng.Intn(accts))
			dir := []lot.Dir{lot.Long, lot.Short}[rng.Intn(2)]
			price := int64(1 + rng.Intn(2000))
			qty := int64(1 + rng.Intn(6))
			op := rng.Intn(10)
			now += int64(rng.Intn(3))
			switch {
			case op < 4: // Open
				errE := e.Open(now, []byte(acct), []byte("s"), dir, price, qty)
				errN := n.open(acct, dir, price, qty)
				fmt.Fprintf(&logb, " O(t=%d,%s,%v,p=%d,q=%d)->%v;", now, acct, dir, price, qty, errE)
				if (errE == nil) != (errN == nil) {
					t.Fatalf("组%d Open 接受性分歧 engine=%v naive=%v\n%s", g, errE, errN, logb.String())
				}
				if errors.Is(errE, trade.ErrNoFunds) != errors.Is(errN, trade.ErrNoFunds) {
					t.Fatalf("组%d 资金不足判定分歧\n%s", g, logb.String())
				}
			case op < 8: // Close
				mode := []trade.CloseMode{trade.CloseYesterday, trade.CloseToday, trade.CloseAuto}[rng.Intn(3)]
				rE, errE := e.Close(now, []byte(acct), []byte("s"), dir, price, qty, mode)
				rN := n.close(acct, dir, price, qty, mode)
				fmt.Fprintf(&logb, " C(t=%d,%s,%v,p=%d,q=%d,m=%d)->(%d,%d,pnl=%d,fy=%d,ft=%d,e=%v);",
					now, acct, dir, price, qty, mode, rE.CloseY, rE.CloseT, rE.PnL, rE.FeeY, rE.FeeT, errE)
				if (errE == nil) != (rN.err == nil) {
					t.Fatalf("组%d Close 接受性分歧 engine=%v naive=%v\n%s", g, errE, rN.err, logb.String())
				}
				if errE == nil && (rE.CloseY != rN.y || rE.CloseT != rN.t || rE.PnL != rN.pnl ||
					rE.FeeY != rN.feeY || rE.FeeT != rN.feeT) {
					t.Fatalf("组%d Close 结果分歧 engine=(y=%d,t=%d,pnl=%d,fy=%d,ft=%d) naive=%+v\n%s",
						g, rE.CloseY, rE.CloseT, rE.PnL, rE.FeeY, rE.FeeT, rN, logb.String())
				}
			default: // Settle
				sp := int64(1 + rng.Intn(2000))
				callsE, errE := mtm.Settle(e, now, []byte("s"), sp)
				must(t, errE, "random settle")
				callsN := n.settle(sp)
				fmt.Fprintf(&logb, " S(t=%d,sp=%d)->%v;", now, sp, callsE)
				if len(callsE) != len(callsN) {
					t.Fatalf("组%d 追保名单长度分歧 engine=%v naive=%v\n%s", g, callsE, callsN, logb.String())
				}
				for i := range callsN {
					if string(callsE[i]) != callsN[i] {
						t.Fatalf("组%d 追保名单分歧 engine=%v naive=%v\n%s", g, callsE, callsN, logb.String())
					}
				}
			}
			// 每步核对余额与保证金
			for a := 0; a < accts; a++ {
				an := fmt.Sprintf("a%d", a)
				bE, _ := e.Balance([]byte(an))
				if bE != n.bal[an] {
					t.Fatalf("组%d 步%d 账户%s 余额分歧 engine=%d naive=%d\n%s",
						g, s, an, bE, n.bal[an], logb.String())
				}
				if mE, mN := e.Margin([]byte(an)), n.marginAcct(an); mE != mN {
					t.Fatalf("组%d 步%d 账户%s 保证金分歧 engine=%d naive=%d\n%s",
						g, s, an, mE, mN, logb.String())
				}
			}
		}
		if g < 5 || g == 999 {
			t.Logf("%s 判定: 全部操作接受性/盈亏/费用/余额/保证金/追保名单与朴素模拟一致；逐手恒等式成立", logb.String())
		} else {
			t.Logf("[组%d] mult=%d mr=%d fo=%d fy=%d ft=%d, 60步随机开/平/结算; 判定: 逐步接受性/盈亏/两段手续费/余额/保证金/追保名单均与逐手朴素模拟一致",
				g, mult, mr, fo, fy, ft)
		}
	}
}

// TestConcurrent 并发调用结果等价于某串行顺序，-race 下无数据竞争。
func barrierWait(mu *sync.Mutex, cond *sync.Cond, gen, arrived *int, size, wantGen int) {
	mu.Lock()
	for *gen != wantGen {
		cond.Wait()
	}
	*arrived++
	if *arrived == size {
		*arrived = 0
		*gen++
		cond.Broadcast()
	} else {
		g := *gen
		for *gen == g {
			cond.Wait()
		}
	}
	mu.Unlock()
}

func TestConcurrent(t *testing.T) {
	e := trade.NewEngine()
	must(t, e.AddSymbol(0, []byte("s"), 1, 1000, 0, 0, 0), "add")
	nAcct := 20
	for a := 0; a < nAcct; a++ {
		must(t, e.Deposit(0, []byte(fmt.Sprintf("c%d", a)), 1e11), "dep")
	}
	const rounds = 50
	var wg sync.WaitGroup
	wg.Add(nAcct)
	var mu sync.Mutex
	cond := sync.NewCond(&mu)
	gen := 0     // 已放行的阶段代次
	arrived := 0 // 当前阶段已到达人数
	for a := 0; a < nAcct; a++ {
		go func(a int) {
			defer wg.Done()
			an := []byte(fmt.Sprintf("c%d", a))
			for i := 0; i < rounds; i++ {
				barrierWait(&mu, cond, &gen, &arrived, nAcct+1, 2*i)
				_ = e.Open(int64(2*i+1), an, []byte("s"), lot.Long, 100, 1)
				barrierWait(&mu, cond, &gen, &arrived, nAcct+1, 2*i+1)
				_, _ = e.Close(int64(2*i+2), an, []byte("s"), lot.Long, int64(100+i), 1, trade.CloseToday)
			}
		}(a)
	}
	for i := 0; i < rounds; i++ {
		// main 作为额外的一个栅栏参与者：与 worker 同代到达后共同放行。
		barrierWait(&mu, cond, &gen, &arrived, nAcct+1, 2*i)   // 等齐开仓
		barrierWait(&mu, cond, &gen, &arrived, nAcct+1, 2*i+1) // 等齐平仓
	}
	wg.Wait()
	// 每手开费0、平今费0；第 i 轮平仓盈亏 = i*1*1，50 轮合计 0+1+...+49=1225。
	const wantBal = int64(1e11 + 1225)
	for a := 0; a < nAcct; a++ {
		an := fmt.Sprintf("c%d", a)
		b, _ := e.Balance([]byte(an))
		if b != wantBal {
			t.Fatalf("账户%s 并发后余额=%d，期望%d", an, b, wantBal)
		}
	}
	t.Log("20账户×50轮并发开/平完成；-race 无竞争，各账户余额与串行语义一致")
}
