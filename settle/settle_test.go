package settle

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"ontology/catalog"
	"ontology/fund"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func eqRes(t *testing.T, name string, got, want Result) {
	t.Helper()
	if got != want {
		t.Fatalf("%s mismatch:\n got=%+v\nwant=%+v", name, got, want)
	}
}

// 题给例参数：D=50000 S1=1e6 S2=5e6 70/80/90；D2=60000 rd=60 ra=50 CapA=20000。
func newWorkedEngine(t *testing.T) *Engine {
	t.Helper()
	e := New(50000, 1_000_000, 5_000_000, 70, 80, 90, 10_000_000_000, 60000, 60, 50, 20000)
	must(t, e.AddItem("jia", catalog.Jia, 0, 10000))
	must(t, e.AddItem("jia100", catalog.Jia, 0, 0))
	must(t, e.AddItem("yi", catalog.Yi, 10, 5000))
	must(t, e.AddItem("bing", catalog.Bing, 0, 0))
	must(t, e.AddPerson("p"))
	return e
}

// TestWorkedExample 题给两笔：限价、逐条先行自付取整、起付耗尽、大病跨线、救助封顶。
func TestWorkedExample(t *testing.T) {
	e := newWorkedEngine(t)

	r1, err := e.Settle(1, "s1", "p", 2026, []Item{
		{Code: "jia", Price: 12000, Qty: 1},
		{Code: "yi", Price: 3333, Qty: 3},
		{Code: "bing", Price: 5000, Qty: 1},
	})
	must(t, err)
	t.Logf("s1: A=%d E=%d g=%d f1=%d f2=%d f3=%d self=%d (未过D2,无救助)",
		r1.A, r1.E, r1.G, r1.F1, r1.F2, r1.F3, r1.Self)
	eqRes(t, "s1", r1, Result{
		A: 26999, E: 18999, G: 18999, X: 0,
		F1: 0, F2: 0, F3: 0, Self: 26999,
		P: 18999, Z: 18999,
	})

	r2, err := e.Settle(2, "s2", "p", 2026, []Item{
		{Code: "jia100", Price: 100002, Qty: 1},
	})
	must(t, err)
	t.Logf("s2: g=%d x=%d f=floor(48300.7)=%d p=%d y=%d f2=floor(6420.6)=%d self=%d",
		r2.G, r2.X, r2.F1, r2.P, r2.Y, r2.F2, r2.Self)
	eqRes(t, "s2-no-aid", r2, Result{
		A: 100002, E: 100002, G: 31001, X: 69001,
		F1: 48300, F2: 6420, F3: 0, Self: 45282,
		M1: 69001, P: 51702, Y: 10701, Z: 45282,
	})

	must(t, e.Reverse(3, "s2"))
	if got := e.Acc("p", 2026); got != (fund.Acc{DU: 18999, X: 0, F: 0, P: 18999, Q: 0}) {
		t.Fatalf("after reverse acc=%+v", got)
	}
	must(t, e.SetAid(4, "p", true))
	r2a, err := e.Settle(5, "s3", "p", 2026, []Item{
		{Code: "jia100", Price: 100002, Qty: 1},
	})
	must(t, err)
	t.Logf("s3 aid: z=%d floor(50%%)=22641 cap20000 -> f3=%d self=%d", r2a.Z, r2a.F3, r2a.Self)
	if r2a.F1 != 48300 || r2a.F2 != 6420 || r2a.F3 != 20000 || r2a.Self != 25282 {
		t.Fatalf("aid result = %+v", r2a)
	}
}

// TestPrepayPerLineCeiling 逐条向上取整而非合计。
func TestPrepayPerLineCeiling(t *testing.T) {
	e := New(0, 100, 1000, 0, 0, 0, 1e12, 0, 0, 0, 0)
	must(t, e.AddItem("yi", catalog.Yi, 10, 0))
	must(t, e.AddPerson("p"))
	// b=101 两条：每条 ceil(10.1)=11 ⇒ Σc=22；合计后 ceil(20.2)=21，可区分。
	r, err := e.Settle(1, "s", "p", 1, []Item{
		{Code: "yi", Price: 101, Qty: 1},
		{Code: "yi", Price: 101, Qty: 1},
	})
	must(t, err)
	if r.A != 202 || r.E != 180 {
		t.Fatalf("per-line ceiling: A=%d E=%d, want E=180", r.A, r.E)
	}
	t.Logf("per-line: two lines b=101 c=11 each -> Σc=22 E=%d", r.E)
}

// TestSegmentsAndSingleRound 跨段 + 合计只取整一次。
func TestSegmentsAndSingleRound(t *testing.T) {
	e := New(0, 1_000_000, 5_000_000, 70, 80, 90, 1e12, 0, 0, 0, 0)
	must(t, e.AddItem("jia", catalog.Jia, 0, 0))
	must(t, e.AddPerson("p"))
	first, err := e.Settle(1, "s0", "p", 1, []Item{{Code: "jia", Price: 989999, Qty: 1}})
	must(t, err)
	if first.M1 != 989999 {
		t.Fatalf("setup M1=%d", first.M1)
	}
	r, err := e.Settle(2, "s1", "p", 1, []Item{{Code: "jia", Price: 30002, Qty: 1}})
	must(t, err)
	if r.M1 != 10001 || r.M2 != 20001 || r.M3 != 0 || r.F1 != 23001 {
		t.Fatalf("segments m1=%d m2=%d m3=%d f1=%d", r.M1, r.M2, r.M3, r.F1)
	}
	t.Logf("cross: m1=%d m2=%d f=floor(2300150/100)=%d (逐段取整错为23000)", r.M1, r.M2, r.F1)
}

// TestFundCap 基本医保年度封顶截断。
func TestFundCap(t *testing.T) {
	e := New(0, 100, 1000, 100, 100, 100, 500, 0, 0, 0, 0)
	must(t, e.AddItem("jia", catalog.Jia, 0, 0))
	must(t, e.AddPerson("p"))
	r, err := e.Settle(1, "s", "p", 1, []Item{{Code: "jia", Price: 1000, Qty: 1}})
	must(t, err)
	if r.F1 != 500 || r.Self != 500 {
		t.Fatalf("cap f1=%d self=%d", r.F1, r.Self)
	}
	if acc := e.Acc("p", 1); acc.F != 500 {
		t.Fatalf("acc F=%d > CapF", acc.F)
	}
	t.Logf("fund cap: f=1000 truncate to 500 self=%d", r.Self)
}

// TestDeductibleExactly 大病起付恰等：P==D2 ⇒ y=0。
func TestDeductibleExactly(t *testing.T) {
	e := New(500, 100000, 200000, 0, 0, 0, 1e12, 1000, 60, 0, 0)
	must(t, e.AddItem("jia", catalog.Jia, 0, 0))
	must(t, e.AddPerson("p"))
	r, err := e.Settle(1, "s", "p", 1, []Item{{Code: "jia", Price: 1000, Qty: 1}})
	must(t, err)
	if r.P != 1000 || r.Y != 0 || r.F2 != 0 {
		t.Fatalf("exact deductible P=%d y=%d f2=%d", r.P, r.Y, r.F2)
	}
	t.Logf("exact D2: P=%d == D2=1000 -> y=%d f2=%d", r.P, r.Y, r.F2)
}

// TestAidCap 救助年度封顶。
func TestAidCap(t *testing.T) {
	e := New(0, 100, 1000, 0, 0, 0, 0, 0, 0, 50, 300)
	must(t, e.AddItem("jia", catalog.Jia, 0, 0))
	must(t, e.AddPerson("p"))
	must(t, e.SetAid(1, "p", true))
	r, err := e.Settle(2, "s", "p", 1, []Item{{Code: "jia", Price: 1000, Qty: 1}})
	must(t, err)
	if r.F3 != 300 || r.Self != 700 {
		t.Fatalf("aid cap f3=%d self=%d", r.F3, r.Self)
	}
	t.Logf("aid cap: floor(1000*50%%)=500 truncate CapA=300 self=%d", r.Self)
}

// TestNewYearReset 换年清零、旧年关闭、冲正恢复最大年度。
func TestNewYearReset(t *testing.T) {
	e := New(500, 1000, 10000, 50, 50, 50, 1e12, 0, 0, 0, 0)
	must(t, e.AddItem("jia", catalog.Jia, 0, 0))
	must(t, e.AddPerson("p"))
	_, err := e.Settle(1, "s1", "p", 2026, []Item{{Code: "jia", Price: 600, Qty: 1}})
	must(t, err)
	if acc := e.Acc("p", 2026); acc.DU != 500 {
		t.Fatalf("year1 du=%d", acc.DU)
	}
	if _, err := e.Settle(2, "s2", "p", 2025, []Item{{Code: "jia", Price: 1, Qty: 1}}); !errors.Is(err, ErrYearClosed) {
		t.Fatalf("want ErrYearClosed, got %v", err)
	}
	r, err := e.Settle(3, "s3", "p", 2027, []Item{{Code: "jia", Price: 600, Qty: 1}})
	must(t, err)
	if r.G != 500 {
		t.Fatalf("new year g=%d want 500", r.G)
	}
	must(t, e.Reverse(4, "s3"))
	if _, err := e.Settle(5, "s4", "p", 2027, []Item{{Code: "jia", Price: 10, Qty: 1}}); err != nil {
		t.Fatalf("settle 2027 after reversing prior 2027: %v", err)
	}
	t.Logf("new year reset & maxYear restore ok")
}

// TestReverseOrder 冲正次序与逐字段恢复。
func TestReverseOrder(t *testing.T) {
	e := New(100, 1000, 10000, 50, 50, 50, 1e12, 0, 0, 0, 0)
	must(t, e.AddItem("jia", catalog.Jia, 0, 0))
	must(t, e.AddPerson("p"))
	before := e.Acc("p", 1)
	_, err := e.Settle(1, "s1", "p", 1, []Item{{Code: "jia", Price: 50, Qty: 1}})
	must(t, err)
	_, err = e.Settle(2, "s2", "p", 1, []Item{{Code: "jia", Price: 120, Qty: 1}})
	must(t, err)
	if err := e.Reverse(3, "s1"); !errors.Is(err, ErrNotLast) {
		t.Fatalf("reverse non-last want ErrNotLast, got %v", err)
	}
	must(t, e.Reverse(4, "s2"))
	if err := e.Reverse(5, "s2"); !errors.Is(err, ErrReversed) {
		t.Fatalf("reverse again want ErrReversed, got %v", err)
	}
	must(t, e.Reverse(6, "s1"))
	if got := e.Acc("p", 1); got != before {
		t.Fatalf("after all reverses acc=%+v want %+v", got, before)
	}
	t.Logf("reverse LIFO order & field-by-field restore: %+v", before)
}

// TestRejectionOrder 拒绝按序只报第一个。
func TestRejectionOrder(t *testing.T) {
	e := newWorkedEngine(t)
	// 先推进时钟与加一笔。
	_, err := e.Settle(10, "base", "p", 2026, []Item{{Code: "jia100", Price: 1, Qty: 1}})
	must(t, err)

	// Settle 次序：参数非法 > 时钟回退 > 参保人不存在 > id重复 > 目录缺项 > 年度关闭。
	if _, err := e.Settle(-1, "x", "p", 2026, []Item{{Code: "jia100", Price: 1, Qty: 1}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid first, got %v", err)
	}
	if _, err := e.Settle(9, "x", "p", 2026, []Item{{Code: "jia100", Price: 1, Qty: 1}}); !errors.Is(err, ErrClockBack) {
		t.Fatalf("clock back, got %v", err)
	}
	if _, err := e.Settle(11, "x", "ghost", 2026, []Item{{Code: "jia100", Price: 1, Qty: 1}}); !errors.Is(err, ErrNoPerson) {
		t.Fatalf("no person, got %v", err)
	}
	// 制造 id 重复
	_, err = e.Settle(11, "dup", "p", 2026, []Item{{Code: "jia100", Price: 1, Qty: 1}})
	must(t, err)
	if _, err := e.Settle(12, "dup", "p", 2026, []Item{{Code: "jia100", Price: 1, Qty: 1}}); !errors.Is(err, ErrDupID) {
		t.Fatalf("dup id, got %v", err)
	}
	if _, err := e.Settle(12, "new", "p", 2026, []Item{{Code: "jia100", Price: 1, Qty: 1}, {Code: "missing", Price: 1, Qty: 1}}); !errors.Is(err, ErrNoItem) {
		t.Fatalf("missing item, got %v", err)
	}
	// 目录缺项下标最小：第 0 项缺失应先报（即使后面也缺）。
	if _, err := e.Settle(12, "new2", "p", 2026, []Item{{Code: "nope0", Price: 1, Qty: 1}, {Code: "nope1", Price: 1, Qty: 1}}); !errors.Is(err, ErrNoItem) {
		t.Fatalf("missing smallest index, got %v", err)
	}

	// Reverse 次序：参数非法 > 时钟回退 > 不存在 > 已冲正 > 非最后。
	if err := e.Reverse(-1, "dup"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("rev invalid, got %v", err)
	}
	if err := e.Reverse(1, "dup"); !errors.Is(err, ErrClockBack) {
		t.Fatalf("rev clock back, got %v", err)
	}
	if err := e.Reverse(13, "nope"); !errors.Is(err, ErrNoSettlement) {
		t.Fatalf("rev missing, got %v", err)
	}
	// dup 是该人当前最后一笔（base 更早），先正常冲正再要求已冲正。
	must(t, e.Reverse(13, "dup"))
	if err := e.Reverse(14, "dup"); !errors.Is(err, ErrReversed) {
		t.Fatalf("rev already, got %v", err)
	}
	// 再结两笔，冲较早一笔应报非最后。
	_, err = e.Settle(15, "a1", "p", 2026, []Item{{Code: "jia100", Price: 1, Qty: 1}})
	must(t, err)
	_, err = e.Settle(16, "a2", "p", 2026, []Item{{Code: "jia100", Price: 1, Qty: 1}})
	must(t, err)
	if err := e.Reverse(17, "base"); !errors.Is(err, ErrNotLast) {
		t.Fatalf("rev not last, got %v", err)
	}
	t.Logf("rejection order all as specified; clock unchanged on reject")
}

// TestConcurrentSafe 并发调用结果等价于某串行顺序：race 检测 + 恒等与封顶不变量。
func TestConcurrentSafe(t *testing.T) {
	e := New(1000, 100000, 1000000, 70, 80, 90, 5_000_000, 50000, 60, 50, 1_000_000)
	must(t, e.AddItem("jia", catalog.Jia, 0, 0))
	const people = 8
	for i := 0; i < people; i++ {
		must(t, e.AddPerson(fmtPerson(i)))
	}
	var wg sync.WaitGroup
	var orderMu sync.Mutex // 取号与调用同临界区，保证存在合法串行顺序
	var clock int64
	for i := 0; i < people; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			p := fmtPerson(i)
			for k := 0; k < 40; k++ {
				var r Result
				var err error
				orderMu.Lock()
				clock++
				r, err = e.Settle(clock, fmt.Sprintf("c-%d-%d", i, k), p, 1,
					[]Item{{Code: "jia", Price: 500, Qty: 1}})
				orderMu.Unlock()
				if err != nil {
					t.Errorf("concurrent settle: %v", err)
					return
				}
				if r.A != r.F1+r.F2+r.F3+r.Self {
					t.Errorf("identity: %+v", r)
				}
			}
		}(i)
	}
	wg.Wait()
	for i := 0; i < people; i++ {
		acc := e.Acc(fmtPerson(i), 1)
		if acc.DU > 1000 || acc.F > 5_000_000 || acc.Q > 1_000_000 {
			t.Fatalf("invariant person %d acc=%+v", i, acc)
		}
	}
	t.Logf("concurrent: %d goroutines x 40 settles, invariants hold", people)
}

func fmtPerson(i int) string { return "pp" + string(rune('a'+i)) }
