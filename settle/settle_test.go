package settle

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"ontology/fund"
)

func exampleEngine(t *testing.T) *Engine {
	t.Helper()
	e, err := New(50000, 1_000_000, 5_000_000, 70, 80, 90,
		10_000_000_000, 60000, 60, 50, 20000)
	if err != nil {
		t.Fatal(err)
	}
	mustOK(t, e.AddItem("jia", "甲", 0, 10000))
	mustOK(t, e.AddItem("yi", "乙", 10, 5000))
	mustOK(t, e.AddItem("bing", "丙", 0, 0))
	mustOK(t, e.AddItem("jiaA", "甲", 0, 0))
	mustOK(t, e.AddPerson("p"))
	return e
}

func TestTouchedTiers(t *testing.T) {
	for _, n := range []int{10, 10000} {
		t.Run(fmt.Sprintf("n=%d", n), func(t *testing.T) {
			e, err := New(0, 9_999_999_999_999, 10_000_000_000_000, 0, 0, 0, 10_000_000_000_000, 10_000_000_000_000, 0, 0, 0)
			mustOK(t, err)
			mustOK(t, e.AddItem("a", "甲", 0, 0))
			mustOK(t, e.AddPerson("p"))
			for i := 0; i < n; i++ {
				_, serr := e.Settle(int64(i+1), fmt.Sprintf("s%06d", i), "p", 2024,
					[]Item{{"a", 1, 1}})
				mustOK(t, serr)
				if got := e.touchedForTest(); got != 0 {
					t.Fatalf("第 %d 笔 Settle 读取历史记录数 = %d, 要求 0", i, got)
				}
			}
			// 只允许冲正最后一笔：读取数 1，与 n 无关。
			lastID := fmt.Sprintf("s%06d", n-1)
			_, rerr := e.Reverse(int64(n+1), lastID)
			mustOK(t, rerr)
			if got := e.touchedForTest(); got != 1 {
				t.Fatalf("n=%d 时 Reverse 读取历史记录数 = %d, 要求不超过 1", n, got)
			}
			t.Logf("n=%d: Settle touched=0, Reverse touched=%d", n, e.touchedForTest())
		})
	}
}

func TestConcurrent(t *testing.T) {
	e, err := New(0, 1000, 10000, 50, 80, 90, 10_000_000_000_000, 10_000_000_000_000, 0, 0, 0)
	mustOK(t, err)
	mustOK(t, e.AddItem("a", "甲", 0, 0))

	const people = 8
	const ops = 200
	for i := 0; i < people; i++ {
		mustOK(t, e.AddPerson(fmt.Sprintf("p%d", i)))
	}

	// 全局单调时钟在并发下无法预知抢锁顺序，因此所有并发操作
	// 携带相同 now（now 相等合法，不构成回退），仍然真实竞争同一把锁。
	var wg sync.WaitGroup
	for w := 0; w < people; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			p := fmt.Sprintf("p%d", w)
			for k := 0; k < ops; k++ {
				id := fmt.Sprintf("%d-%d", w, k)
				r, serr := e.Settle(maxNow, id, p, 2024, []Item{{"a", int64(1 + k%500), 1}})
				if serr != nil {
					t.Errorf("settle: %v", serr)
					return
				}
				if r.A != r.F1+r.F2+r.F3+r.Self {
					t.Errorf("恒等式破坏 %+v", r)
					return
				}
			}
		}(w)
	}
	wg.Wait()

	// 不变量核对。
	for w := 0; w < people; w++ {
		acc, ok := e.Acc(fmt.Sprintf("p%d", w), 2024)
		if !ok {
			t.Fatal("累计器缺失")
		}
		if acc.Du < 0 || acc.F < 0 || acc.P < 0 || acc.Q < 0 || acc.X < 0 {
			t.Fatalf("出现负累计 %+v", acc)
		}
		if acc.F > 10_000_000_000_000 || acc.Q > 0 {
			t.Fatalf("封顶不变量破坏 %+v", acc)
		}
	}
	t.Log("并发结算恒等式与非负不变量通过")
}

func TestNewValidation(t *testing.T) {
	cases := []struct {
		name string
		args []int64
		rs   []int
	}{
		{"D超界", []int64{maxMoney + 1, 1, 2, 0, 0, 0}, []int{0, 0, 0, 0, 0}},
		{"S1等于S2", []int64{0, 5, 5, 0, 0, 0}, []int{0, 0, 0, 0, 0}},
		{"比例101", []int64{0, 1, 2, 0, 0, 0}, []int{101, 0, 0, 0, 0}},
		{"比例负", []int64{0, 1, 2, 0, 0, 0}, []int{0, -1, 0, 0, 0}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := tc.args
			r := tc.rs
			if _, err := New(a[0], a[1], a[2], r[0], r[1], r[2], a[3], a[4], r[3], r[4], a[5]); !errors.Is(err, ErrInvalidParam) {
				t.Fatalf("want ErrInvalidParam, got %v", err)
			}
		})
	}
}

func TestSettleRejectOrder(t *testing.T) {
	e := exampleEngine(t)
	good := []Item{{"jiaA", 1, 1}}
	// 参数非法优先于时钟回退
	if _, err := e.Settle(-1, "x", "p", 2024, good); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("参数>时钟, got %v", err)
	}
	if _, err := e.Settle(0, "", "p", 2024, good); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("空 id 参数非法, got %v", err)
	}
	if _, err := e.Settle(0, "x", "p", 0, good); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("年度越界参数非法, got %v", err)
	}
	// 时钟回退优先于参保人不存在
	if _, err := e.Settle(-1, "x", "ghost", 2024, good); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("非法 now 先报参数, got %v", err)
	}
	// now 合法但低于时钟：参保人不存在也要让位于时钟回退
	_, err0 := e.Settle(10, "s0", "p", 2024, good)
	mustOK(t, err0)
	if _, err := e.Settle(9, "x", "ghost", 2024, good); !errors.Is(err, ErrClockRollback) {
		t.Fatalf("时钟>参保人, got %v", err)
	}
	// 参保人不存在优先于结算号重复
	if _, err := e.Settle(11, "s0", "ghost", 2024, good); !errors.Is(err, ErrPersonNotFound) {
		t.Fatalf("参保人>重复号, got %v", err)
	}
	// 结算号重复优先于目录项不存在
	if _, err := e.Settle(11, "s0", "p", 2024, []Item{{"nope", 1, 1}}); !errors.Is(err, ErrDuplicateSettlement) {
		t.Fatalf("重复号>目录缺项, got %v", err)
	}
	// 目录项不存在优先于年度关闭；最小下标
	_, err := e.Settle(11, "s1x", "p", 2000, []Item{{"jiaA", 1, 1}, {"nope", 1, 1}, {"nope2", 1, 1}})
	if !errors.Is(err, ErrItemNotFound) {
		t.Fatalf("目录缺项, got %v", err)
	}
	if !errors.Is(err, ErrItemNotFound) || err.Error() != "settle: catalog item not found: index 1" {
		t.Fatalf("应报最小下标 1, got %v", err)
	}
	// 年度已关闭
	_, err12 := e.Settle(12, "s1", "p", 2025, good)
	mustOK(t, err12)
	if _, err := e.Settle(13, "s2", "p", 2024, good); !errors.Is(err, ErrYearClosed) {
		t.Fatalf("年度关闭, got %v", err)
	}
	// 被拒操作不改时钟
	if e.Clock() != 12 {
		t.Fatalf("拒绝后时钟应停在 12, got %d", e.Clock())
	}
	t.Logf("拒绝次序全部符合，时钟停在 %d", e.Clock())
}

func TestReverseRejectOrder(t *testing.T) {
	e := exampleEngine(t)
	good := []Item{{"jiaA", 1, 1}}
	// 参数非法 > 时钟回退
	if _, err := e.Reverse(-1, "x"); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("参数>时钟, got %v", err)
	}
	_, err0 := e.Settle(10, "s0", "p", 2024, good)
	mustOK(t, err0)
	if _, err := e.Reverse(9, "x"); !errors.Is(err, ErrClockRollback) {
		t.Fatalf("时钟>不存在, got %v", err)
	}
	if _, err := e.Reverse(11, "missing"); !errors.Is(err, ErrSettlementNotFound) {
		t.Fatalf("不存在, got %v", err)
	}
	if _, err := e.Reverse(11, "s0"); err != nil {
		t.Fatalf("正常冲正失败: %v", err)
	}
	if _, err := e.Reverse(12, "s0"); !errors.Is(err, ErrAlreadyReversed) {
		t.Fatalf("已冲正, got %v", err)
	}
	t.Log("Reverse 拒绝次序符合：参数>时钟>不存在>已冲正>非最后一笔")
}

func TestSetAidOrder(t *testing.T) {
	e := exampleEngine(t)
	if err := e.SetAid(-1, "p", true); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("参数非法, got %v", err)
	}
	if err := e.SetAid(1, "ghost", true); !errors.Is(err, ErrPersonNotFound) {
		t.Fatalf("参保人不存在, got %v", err)
	}
	mustOK(t, e.SetAid(5, "p", true))
	if err := e.SetAid(4, "p", false); !errors.Is(err, ErrClockRollback) {
		t.Fatalf("时钟回退, got %v", err)
	}
	if !e.Aid("p") {
		t.Fatal("被拒 SetAid 不应改变身份")
	}
}

func TestNewYearReset(t *testing.T) {
	e := exampleEngine(t)
	_, err := e.Settle(1, "s1", "p", 2024, firstSettlementItems())
	mustOK(t, err)
	r, err := e.Settle(2, "s2", "p", 2025, []Item{{"jiaA", 1000, 1}})
	mustOK(t, err)
	if r.Before != zeroAcc() {
		t.Fatalf("新年度应从零开始, got before=%+v", r.Before)
	}
	if r.G != 1000 || r.F1 != 0 {
		t.Fatalf("新年度先扣起付, got %+v", r)
	}
	_, err = e.Settle(3, "s3", "p", 2024, []Item{{"jiaA", 1, 1}})
	if !errors.Is(err, ErrYearClosed) {
		t.Fatalf("回到旧年度应 ErrYearClosed, got %v", err)
	}
	t.Logf("换年清零: before=%+v g=%d；回 2024 拒绝: %v", r.Before, r.G, err)
}

func TestReverseLIFO(t *testing.T) {
	e := exampleEngine(t)
	zero := zeroAcc()
	r1, err := e.Settle(1, "s1", "p", 2024, firstSettlementItems())
	mustOK(t, err)
	r2, err := e.Settle(2, "s2", "p", 2024, []Item{{"jiaA", 100002, 1}})
	mustOK(t, err)
	_ = r2

	if _, err := e.Reverse(3, "s1"); !errors.Is(err, ErrNotLast) {
		t.Fatalf("冲正非栈顶应 ErrNotLast, got %v", err)
	}
	if e.touchedForTest() != 1 {
		t.Fatal("即使被拒，栈顶读取数应为 1")
	}
	if e.Clock() != 2 {
		t.Fatalf("拒绝不应推进时钟, clock=%d", e.Clock())
	}
	accMid, _ := e.Acc("p", 2024)

	rev, err := e.Reverse(4, "s2")
	mustOK(t, err)
	if rev.After != r1.After {
		t.Fatalf("冲正 s2 后应等于 s1 后状态, got %+v want %+v", rev.After, r1.After)
	}
	if _, err := e.Reverse(5, "s2"); !errors.Is(err, ErrAlreadyReversed) {
		t.Fatalf("重复冲正应 ErrAlreadyReversed, got %v", err)
	}
	rev1, err := e.Reverse(6, "s1")
	mustOK(t, err)
	if rev1.After != zero {
		t.Fatalf("冲正 s1 后应全零, got %+v", rev1.After)
	}
	// 结算号不可复用
	if _, err := e.Settle(7, "s1", "p", 2024, firstSettlementItems()); !errors.Is(err, ErrDuplicateSettlement) {
		t.Fatalf("已冲正结算号仍不可复用, got %v", err)
	}
	// 全部冲正后 2024 可重新结算（年度重新打开，累计器为零）
	r3, err := e.Settle(8, "s3", "p", 2024, firstSettlementItems())
	mustOK(t, err)
	if r3.Before != zero {
		t.Fatalf("全部冲正后重算应从零开始, got %+v", r3.Before)
	}
	t.Logf("LIFO: 冲正前 %+v；拒冲非栈顶状态不变；冲正后全零再重开 %+v",
		accMid, r3.After)
}

func TestReverseAidIndependent(t *testing.T) {
	// 救助身份的当前取值不影响冲正恢复。
	e := exampleEngine(t)
	mustOK(t, e.SetAid(1, "p", true))
	r, err := e.Settle(2, "s1", "p", 2024, []Item{{"jiaA", 100000, 1}})
	mustOK(t, err)
	if r.F3 <= 0 {
		t.Fatalf("前置应有救助支付, got F3=%d", r.F3)
	}
	mustOK(t, e.SetAid(3, "p", false))
	rev, err := e.Reverse(4, "s1")
	mustOK(t, err)
	if rev.After != zeroAcc() || rev.F3 != r.F3 {
		t.Fatalf("冲正不应受当前身份影响, rev=%+v", rev)
	}
	t.Logf("身份关闭后冲正仍恢复成功: F3=%d after=%+v", rev.F3, rev.After)
}

func TestCrossSegmentRounding(t *testing.T) {
	// X=989999 时 x=30002：m1=10001 m2=20001，
	// f=⌊(700070+1600080)/100⌋=23001（合计后只取整一次）。
	e := exampleEngine(t)
	r1, err := e.Settle(1, "k1", "p", 2024, []Item{{"jiaA", 1_039_999, 1}})
	if err != nil {
		t.Fatal(err)
	}
	if r1.G != 50000 || r1.X != 989999 {
		t.Fatalf("前置: g=%d x=%d", r1.G, r1.X)
	}
	r2, err := e.Settle(2, "k2", "p", 2024, []Item{{"jiaA", 30002, 1}})
	if err != nil {
		t.Fatal(err)
	}
	if r2.M1 != 10001 || r2.M2 != 20001 || r2.M3 != 0 {
		t.Fatalf("分段长度错误 %d %d %d", r2.M1, r2.M2, r2.M3)
	}
	if r2.F1 != 23001 {
		t.Fatalf("合计取整应 23001（逐段取整错误值 23000）, got %d", r2.F1)
	}
	t.Logf("跨段: m1=%d m2=%d 分子=%d f=%d",
		r2.M1, r2.M2, r2.M1*70+r2.M2*80, r2.F1)
}

func TestCapFTruncation(t *testing.T) {
	e, err := New(0, 100000, 500000, 90, 90, 90, 500, 0, 0, 0, 0)
	mustOK(t, err)
	mustOK(t, e.AddItem("a", "甲", 0, 0))
	mustOK(t, e.AddPerson("p"))
	r, err := e.Settle(1, "s", "p", 1, []Item{{"a", 10000, 1}})
	mustOK(t, err)
	if r.F1 != 500 || r.P != 9500 || r.Self != 9500 {
		t.Fatalf("封顶截断错误: %+v", r)
	}
	acc, _ := e.Acc("p", 1)
	if acc.F != 500 {
		t.Fatalf("F 封顶 = %d", acc.F)
	}
	t.Logf("基金封顶: f=9000 CapF-F=500 => F1=%d P=%d 个人=%d", r.F1, r.P, r.Self)
}

func TestD2Exactly(t *testing.T) {
	e, err := New(0, 9_999_999_999_999, 10_000_000_000_000, 0, 0, 0, 10_000_000_000_000, 1000, 50, 0, 0)
	mustOK(t, err)
	mustOK(t, e.AddItem("a", "甲", 0, 0))
	mustOK(t, e.AddPerson("p"))
	r1, err := e.Settle(1, "s1", "p", 1, []Item{{"a", 1000, 1}})
	mustOK(t, err)
	if r1.Y != 0 || r1.F2 != 0 {
		t.Fatalf("恰等 D2 时 y 应为 0, got y=%d F2=%d", r1.Y, r1.F2)
	}
	r2, err := e.Settle(2, "s2", "p", 1, []Item{{"a", 1, 1}})
	mustOK(t, err)
	if r2.Y != 1 || r2.F2 != 0 {
		t.Fatalf("超出 1 分 y=1 F2=⌊0.5⌋=0, got y=%d F2=%d", r2.Y, r2.F2)
	}
	r3, err := e.Settle(3, "s3", "p", 1, []Item{{"a", 2, 1}})
	mustOK(t, err)
	if r3.Y != 2 || r3.F2 != 1 {
		t.Fatalf("y=2 F2=1, got y=%d F2=%d", r3.Y, r3.F2)
	}
	t.Logf("大病恰等: y1=%d y2=%d F2_2=%d y3=%d F2_3=%d", r1.Y, r2.Y, r2.F2, r3.Y, r3.F2)
}

func TestAidCap(t *testing.T) {
	e, err := New(0, 9_999_999_999_999, 10_000_000_000_000, 0, 0, 0, 10_000_000_000_000, 10_000_000_000_000, 0, 50, 100)
	mustOK(t, err)
	mustOK(t, e.AddItem("a", "甲", 0, 0))
	mustOK(t, e.AddPerson("p"))
	mustOK(t, e.SetAid(1, "p", true))
	r, err := e.Settle(2, "s", "p", 1, []Item{{"a", 1000, 1}})
	mustOK(t, err)
	if r.F3 != 100 || r.Self != 900 {
		t.Fatalf("救助封顶应 F3=100, got %+v", r)
	}
	acc, _ := e.Acc("p", 1)
	if acc.Q != 100 {
		t.Fatalf("Q=%d", acc.Q)
	}
	t.Logf("救助封顶: z=1000 应 500 截断到 100 => F3=%d 个人=%d", r.F3, r.Self)
}

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func zeroAcc() fund.Acc { return fund.Acc{} }

func firstSettlementItems() []Item {
	return []Item{
		{"jia", 12000, 1},
		{"yi", 3333, 3},
		{"bing", 5000, 1},
	}
}

func assertPay(t *testing.T, got *Result, want Result) {
	t.Helper()
	if got.A != want.A || got.E != want.E || got.G != want.G || got.X != want.X ||
		got.M1 != want.M1 || got.M2 != want.M2 || got.M3 != want.M3 ||
		got.F1 != want.F1 || got.P != want.P || got.Y != want.Y ||
		got.F2 != want.F2 || got.Z != want.Z || got.F3 != want.F3 || got.Self != want.Self {
		t.Fatalf("支付不匹配\ngot  %+v\nwant %+v", got, want)
	}
}

func TestSpecExampleFirstSettlement(t *testing.T) {
	e := exampleEngine(t)
	res, err := e.Settle(1, "s1", "p", 2024, firstSettlementItems())
	if err != nil {
		t.Fatal(err)
	}
	want := Result{A: 26999, E: 18999, G: 18999, X: 0,
		F1: 0, P: 18999, Y: 0, F2: 0, Z: 18999, F3: 0, Self: 26999}
	assertPay(t, res, want)
	t.Logf("第一笔: A=%d E=%d g=%d F1=%d p=%d F2=%d 个人=%d",
		res.A, res.E, res.G, res.F1, res.P, res.F2, res.Self)
}

func TestSpecExampleSecondSettlement(t *testing.T) {
	for _, aid := range []bool{false, true} {
		t.Run(fmt.Sprintf("aid=%v", aid), func(t *testing.T) {
			e := exampleEngine(t)
			if _, err := e.Settle(1, "s1", "p", 2024, firstSettlementItems()); err != nil {
				t.Fatal(err)
			}
			if aid {
				mustOK(t, e.SetAid(2, "p", true))
			}
			res, err := e.Settle(3, "s2", "p", 2024, []Item{{"jiaA", 100002, 1}})
			if err != nil {
				t.Fatal(err)
			}
			want := Result{A: 100002, E: 100002, G: 31001, X: 69001,
				M1: 69001, F1: 48300, P: 51702, Y: 10701, F2: 6420, Z: 45282}
			if aid {
				want.F3 = 20000
				want.Self = 25282
			} else {
				want.F3 = 0
				want.Self = 45282
			}
			assertPay(t, res, want)
			if res.A != res.F1+res.F2+res.F3+res.Self {
				t.Fatal("A != F1+F2+F3+个人")
			}
			acc, _ := e.Acc("p", 2024)
			if acc.Du != 50000 || acc.P != 70701 || acc.F != 48300 {
				t.Fatalf("累计器异常 %+v", acc)
			}
			if acc.F > 10_000_000_000 || acc.Q > 20000 {
				t.Fatal("封顶不变量被破坏")
			}
			t.Logf("第二笔 aid=%v: F1=%d F2=%d F3=%d 个人=%d 累计=%+v",
				aid, res.F1, res.F2, res.F3, res.Self, acc)
		})
	}
}
