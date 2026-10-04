package plan

import (
	"reflect"
	"testing"
)

type recSink struct{ lost [][2]int64 }

func (s *recSink) LostRange(l, r int64) { s.lost = append(s.lost, [2]int64{l, r}) }

func rngEq(got []Range, want ...Range) bool { return reflect.DeepEqual(got, want) }

// TestPlanExample 题给主例（Lm=4,K=2,Tq=30,R=2）逐步复现。
func TestPlanExample(t *testing.T) {
	g := New(Config{Lm: 4, K: 2, Tq: 30, R: 2})
	sk := &recSink{}
	g.AddRun(4, 10) // 1,2,3 已收；hi 推到 10，缺口 [4,10)

	out := g.Emit(100, 5)
	if !rngEq(out, Range{4, 7}, Range{8, 8}) {
		t.Fatalf("plan1=%v", out)
	}
	if !g.Inflight(4) || !g.Inflight(8) || g.Inflight(9) {
		t.Fatal("4..8 应在途,9 不在途")
	}
	// now=110 到达 5、6：只把 5、6 移出，请求其余序号仍在途。
	g.Fill(5)
	g.Fill(6)

	if n := g.Settle(sk, 120); n != 0 {
		t.Fatalf("120 不应有超时, got %d", n)
	}
	out = g.Emit(120, 10)
	if !rngEq(out, Range{9, 9}) {
		t.Fatalf("plan2=%v", out)
	}

	// 130：4、7、8 的 q=100，恰等超时边界 now==q+Tq 也算超时。
	n := g.Settle(sk, 130)
	if n != 3 {
		t.Fatalf("130 应结算 3 段, got %d", n)
	}
	if len(sk.lost) != 0 {
		t.Fatal("c=1 超时不应判丢")
	}
	out = g.Emit(130, 10)
	// 4 单独一段；[7,9)（7、8）相邻同 c；9 在途（q=120）不参与。
	if !rngEq(out, Range{4, 4}, Range{7, 8}) {
		t.Fatalf("plan3=%v", out)
	}

	// 160 一次 Plan 内结算：4、7、8（q=130）c=2 判丢；9（q=120）c=1 翻回。
	n = g.Settle(sk, 160)
	if n != 3 {
		t.Fatalf("160 应结算 3 段(4 与[7,9)判丢,9 翻回), got %d", n)
	}
	t.Logf("lost 回调=%v", sk.lost)
	if !reflect.DeepEqual(sk.lost, [][2]int64{{4, 5}, {7, 9}}) {
		t.Fatalf("lost=%v", sk.lost)
	}
	out = g.Emit(160, 10)
	if !rngEq(out, Range{9, 9}) {
		t.Fatalf("160 应重发 9, got %v", out)
	}
	if g.Missing() != 1 { // 仅剩 9 在途
		t.Fatalf("missing=%d want 1", g.Missing())
	}

	// Hello 抬 lo=10：9 仍 Missing（在途）也判丢。
	n = g.LoseBelow(sk, 1, 10) // ingest 侧传 [旧lo,新lo)
	if n != 1 {
		t.Fatalf("LoseBelow 段数=%d", n)
	}
	if g.Missing() != 0 {
		t.Fatalf("missing=%d want 0", g.Missing())
	}
	g.AddRun(11, 13)
	out = g.Emit(180, 10)
	if !rngEq(out, Range{11, 12}) {
		t.Fatalf("plan180=%v", out)
	}
}

// TestDifferentC c 不同不得合并。
func TestDifferentC(t *testing.T) {
	g := New(Config{Lm: 4, K: 4, Tq: 30, R: 3})
	sk := &recSink{}
	g.AddRun(20, 24)
	out := g.Emit(0, 4) // 20..23 全部请求 c=1
	if !rngEq(out, Range{20, 23}) {
		t.Fatalf("emit0=%v", out)
	}
	// 22、23 到达，它们的片被移除；20、21 保持在途。
	g.Fill(22)
	g.Fill(23)
	// 30 时刻：20、21 超时 c=1 翻回。
	g.Settle(sk, 30)
	// 新增 c=0 缺口 22..24（模拟 hi 推进场景：22、23 已收，不会发生；
	// 直接构造：再 AddRun 到 24 前先把 22、23 视为缺口需要它们不在树中，
	// 故用 22..24 的 c=0 段检验“c 不同不合并”——通过 Fill 已收不会回树，
	// 改用独立构造：c=1 段 [20,22) 与新增 c=0 段相邻。）
	// 为得到相邻 c 不同段，新增 [22,24) 为新 c=0 缺口。
	g.AddRun(22, 24)
	out = g.Emit(40, 4)
	if !rngEq(out, Range{20, 21}, Range{22, 23}) {
		t.Fatalf("c 不同必须拆成两个请求, got %v", out)
	}
}

// TestExactTimeout Tq 恰等即超时；早 1ms 不超时。
func TestExactTimeout(t *testing.T) {
	g := New(Config{Lm: 4, K: 4, Tq: 30, R: 2})
	sk := &recSink{}
	g.AddRun(1, 5)
	g.Emit(100, 4)
	if g.Settle(sk, 129) != 0 {
		t.Fatal("129 不应超时")
	}
	if g.Settle(sk, 130) != 1 {
		t.Fatal("130=q+Tq 恰等应超时")
	}
	// 翻回后再请求，第二次恰等超时判丢。
	g.Emit(130, 4)
	if g.Settle(sk, 159) != 0 {
		t.Fatal("159 不应超时")
	}
	if g.Settle(sk, 160) != 1 {
		t.Fatal("160 第二次超时")
	}
	if !reflect.DeepEqual(sk.lost, [][2]int64{{1, 5}}) {
		t.Fatalf("lost=%v", sk.lost)
	}
}

// TestPartialArrivalSplits 部分到达后剩余序号各自成段且各自超时。
func TestPartialArrivalSplits(t *testing.T) {
	g := New(Config{Lm: 10, K: 4, Tq: 30, R: 2})
	sk := &recSink{}
	g.AddRun(1, 11) // 1..10
	g.Emit(0, 10)
	for _, x := range []int64{2, 3, 4, 6, 7, 9} {
		g.Fill(x)
	}
	// 剩余在途 1、5、8、10 四片。
	for _, x := range []int64{1, 5, 8, 10} {
		if !g.Inflight(x) {
			t.Fatalf("%d 应在途", x)
		}
	}
	n := g.Settle(sk, 30)
	if n != 4 {
		t.Fatalf("应结算 4 片, got %d", n)
	}
	out := g.Emit(30, 10)
	if !rngEq(out, Range{1, 1}, Range{5, 5}, Range{8, 8}, Range{10, 10}) {
		t.Fatalf("四片各成请求, got %v", out)
	}
}

// TestBudgetAndK budget 截短与 K 截止。
func TestBudgetAndK(t *testing.T) {
	g := New(Config{Lm: 3, K: 2, Tq: 30, R: 2})
	g.AddRun(1, 20)
	out := g.Emit(0, 5)
	if !rngEq(out, Range{1, 3}, Range{4, 5}) {
		t.Fatalf("budget 截短: %v", out)
	}
	// K 截止：budget 充足也只发 2 个。
	g2 := New(Config{Lm: 3, K: 2, Tq: 30, R: 2})
	g2.AddRun(1, 20)
	out = g2.Emit(0, 100)
	if !rngEq(out, Range{1, 3}, Range{4, 6}) {
		t.Fatalf("K 截止: %v", out)
	}
}

// TestEmitScanned 考察段数界：≤ 返回请求数 + 超时段数 + 1。
func TestEmitScanned(t *testing.T) {
	g := New(Config{Lm: 2, K: 100, Tq: 30, R: 3})
	for i := int64(0); i < 100; i++ {
		g.AddRun(i*3, i*3+1) // 每段单序号、彼此分离
	}
	// 先让其中 30 段在途后超时，制造“本次超时段数”。
	g.Emit(0, 30)
	settled := g.Settle(&recSink{}, 30)
	out := g.Emit(30, 10)
	if len(out) != 10 {
		t.Fatalf("out=%d", len(out))
	}
	sc := g.EmitScanned()
	t.Logf("settled=%d requests=%d scanned=%d", settled, len(out), sc)
	if sc > len(out)+settled+1 {
		t.Fatalf("scanned %d 超过界", sc)
	}
}
