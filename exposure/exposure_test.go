package exposure

import (
	"testing"

	"ontology/limit"
)

func TestIncrementalGroupExposureAndTouched(t *testing.T) {
	b := NewBook()
	// A1 H=50：e=140 -> 贡献 90；A2 e=60 -> 贡献 60；g=150。
	b.SetHedge([]byte("A1"), []byte("G"), []byte("S"), limit.Long, 50)
	b.AcceptOpen([]byte("A1"), []byte("G"), []byte("S"), limit.Long, 140)
	if b.Touched() != 1 {
		t.Fatalf("AcceptOpen touched=%d want 1", b.Touched())
	}
	b.FillOpen([]byte("A1"), []byte("G"), []byte("S"), limit.Long, 120)
	b.AcceptOpen([]byte("A2"), []byte("G"), []byte("S"), limit.Long, 60)
	if got := b.GroupExp([]byte("G"), []byte("S")); got != 150 {
		t.Fatalf("g=%d want 150", got)
	}
	r1 := b.Rec([]byte("A1"), []byte("S"))
	if r1.Pos[0] != 120 || r1.Open[0] != 20 || r1.Contrib[0] != 90 {
		t.Fatalf("A1 pos=%d open=%d contrib=%d want 120,20,90", r1.Pos[0], r1.Open[0], r1.Contrib[0])
	}
	// 在途平仓不减敞口，但占用可平量。
	b.AcceptClose([]byte("A1"), []byte("G"), []byte("S"), limit.Long, 100)
	if b.AcctExp([]byte("A1"), []byte("S"), limit.Long) != 140 {
		t.Fatalf("pending close must not reduce exposure")
	}
	if b.Closeable([]byte("A1"), []byte("S"), limit.Long) != 20 {
		t.Fatalf("closeable=%d want 20", b.Closeable([]byte("A1"), []byte("S"), limit.Long))
	}
	// 平仓成交：持仓减、敞口降、贡献增量到 0；A2 仍贡献 60。
	b.FillClose([]byte("A1"), []byte("G"), []byte("S"), limit.Long, 100)
	if b.Touched() != 1 {
		t.Fatalf("FillClose touched=%d want 1", b.Touched())
	}
	if got := b.GroupExp([]byte("G"), []byte("S")); got != 60 {
		t.Fatalf("after close fill g=%d want 60", got)
	}
	if b.AcctExp([]byte("A1"), []byte("S"), limit.Long) != 40 {
		t.Fatalf("A1 e=%d want 40 (pos20+pending open20)", b.AcctExp([]byte("A1"), []byte("S"), limit.Long))
	}
	// 调低套保到 0：贡献变 e=20，g=80。
	b.SetHedge([]byte("A1"), []byte("G"), []byte("S"), limit.Long, 0)
	if b.Touched() != 1 {
		t.Fatalf("SetHedge touched=%d want 1", b.Touched())
	}
	if got := b.GroupExp([]byte("G"), []byte("S")); got != 100 {
		t.Fatalf("after hedge 0 g=%d want 100", got)
	}
	// 日切前 o=已成交120+在途20=140；日切只清已成交，之后只剩在途 20。
	if o := b.DayOpen([]byte("A1"), []byte("S")); o != 140 {
		t.Fatalf("day open before reset=%d want 140", o)
	}
	b.ResetDay()
	if o := b.DayOpen([]byte("A1"), []byte("S")); o != 20 {
		t.Fatalf("day open after reset=%d want 20 (pending only)", o)
	}
	r1 = b.Rec([]byte("A1"), []byte("S"))
	if r1.Pos[0] != 20 || r1.Open[0] != 20 {
		t.Fatalf("after reset pos=%d open=%d want 20,20", r1.Pos[0], r1.Open[0])
	}
}

func TestCheckOpenPriorityAndEquality(t *testing.T) {
	b := NewBook()
	b.AcceptOpen([]byte("A1"), []byte("G"), []byte("S"), limit.Long, 100)
	b.AcceptOpen([]byte("A2"), []byte("G"), []byte("S"), limit.Long, 50)
	// g=150。A1 e=100：+1 同时破账户(La=100)与组(150)，先报账户。
	if r := b.CheckOpen([]byte("A1"), []byte("G"), []byte("S"), limit.Long, 1, 100, 150, 1000); r != AcctLimit {
		t.Fatalf("reason=%v want AcctLimit", r)
	}
	// 放大账户限额后同笔破组：报组限额。
	if r := b.CheckOpen([]byte("A1"), []byte("G"), []byte("S"), limit.Long, 1, 1000, 150, 1000); r != GroupLimit {
		t.Fatalf("reason=%v want GroupLimit", r)
	}
	// 组腾出后只剩日内约束。
	b.CancelOpen([]byte("A2"), []byte("G"), []byte("S"), limit.Long, 50)
	if r := b.CheckOpen([]byte("A1"), []byte("G"), []byte("S"), limit.Long, 1, 1000, 150, 100); r != DayLimit {
		t.Fatalf("reason=%v want DayLimit", r)
	}
	// 取等通过。
	if r := b.CheckOpen([]byte("A1"), []byte("G"), []byte("S"), limit.Long, 0, 100, 150, 1000); r != OK {
		t.Fatalf("qty 0 reason=%v want OK", r)
	}
}
