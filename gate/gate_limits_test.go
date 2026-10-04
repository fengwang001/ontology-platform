package gate

import "testing"

func seedG(t *testing.T, g *Gateway) {
	t.Helper()
	runSteps(t, g, []step{
		reg(1, "A1", "G"), reg(2, "A2", "G"), reg(3, "A3", "G2"),
		setlim(4, "S", 100, 150, 1000),
	})
}

func TestPriorityAcctGroupDay(t *testing.T) {
	// 同一笔同时违反三项限额，确认账户 > 组 > 日内。
	g := New()
	seedG(t, g)
	g.SetLimit(5, b("S"), 100, 150, 100)
	runSteps(t, g, []step{
		ord(6, "p1", "A1", "S", Long, Open, 100, nil), // A1: e=100 贡献100 o=100
		ord(7, "p2", "A2", "S", Long, Open, 50, nil),  // g=150
		ord(8, "p3", "A1", "S", Long, Open, 1, ErrAcctLimit),
	})
	// 放松账户限额并给 A1 套保：同笔只违反组与日内，应报组限额。
	g.SetLimit(9, b("S"), 1000, 150, 100)
	runSteps(t, g, []step{
		hedge(10, "A1", "S", Long, 50, nil),           // A1 贡献 100->50，g=100
		ord(11, "p4", "A2", "S", Long, Open, 50, nil), // A2 e=100，g=150
		ord(12, "p5", "A1", "S", Long, Open, 1, ErrGroupLimit),
	})
	// 只违反日内：组腾出空间后再开 1，报日内（A1 o=100 已满）。
	runSteps(t, g, []step{
		cancel(13, "p4", nil), // A2 撤回 50 在途：g=50
		ord(14, "p6", "A1", "S", Long, Open, 1, ErrDayLimit),
	})
}

func TestHedgePerAccountNoTransfer(t *testing.T) {
	g := New()
	seedG(t, g)
	g.SetLimit(5, b("S"), 200, 150, 1000) // 放大账户限额，只让组限额约束
	runSteps(t, g, []step{
		hedge(6, "A1", "S", Long, 80, nil),
		ord(7, "h1", "A1", "S", Long, Open, 60, nil),  // e=60<H，贡献 0，富余 20
		ord(8, "h2", "A2", "S", Long, Open, 150, nil), // A2 无 H：g=150 取等
		// 合并抵扣口径：总 e−总 H=211−80=131<=150 会错误放行；逐账户口径 g=151 拒绝。
		ord(9, "h3", "A2", "S", Long, Open, 1, ErrGroupLimit),
		// 富余仍只属于 A1：A1 自身开到 80，贡献仍 0，组不变，放行。
		ord(10, "h4", "A1", "S", Long, Open, 20, nil),
	})
	if ge := g.GroupExposure(b("G"), b("S")); ge != 150 {
		t.Fatalf("g=%d want 150", ge)
	}
	r := g.AcctRec(b("A1"), b("S"))
	if r.Contrib[0] != 0 || r.Pos[0]+r.Open[0] != 80 {
		t.Fatalf("A1 contrib=%d e=%d want 0,80", r.Contrib[0], r.Pos[0]+r.Open[0])
	}
}

func TestSpecExample(t *testing.T) {
	// 题目主示例（La=100,Lg=150,D=1000，A1 多头 H=50）。
	g := New()
	seedG(t, g)
	runSteps(t, g, []step{
		hedge(5, "A1", "S", Long, 50, nil),
		ord(6, "base", "A1", "S", Long, Open, 140, nil), // e=140<=150，贡献90
		fill(7, "base", 120, nil),                       // 持仓120，在途20
		ord(8, "a2", "A2", "S", Long, Open, 60, nil),    // e=60，g=150
		ord(9, "a2p", "A2", "S", Long, Open, 1, ErrGroupLimit),
		ord(10, "a1x", "A1", "S", Long, Open, 11, ErrAcctLimit),
		ord(11, "a1eq", "A1", "S", Long, Open, 10, ErrGroupLimit),
		ord(12, "c130", "A1", "S", Long, Close, 130, ErrCloseQty),
		ord(12, "c100", "A1", "S", Long, Close, 100, nil),
	})
	if e := g.AcctExposure(b("A1"), b("S"), Long); e != 140 {
		t.Fatalf("after accept close e=%d want 140", e)
	}
	if ge := g.GroupExposure(b("G"), b("S")); ge != 150 {
		t.Fatalf("after accept close g=%d want 150", ge)
	}
	must(g.Fill(13, b("c100"), 100))
	r := g.AcctRec(b("A1"), b("S"))
	if r.Pos[0] != 20 || r.Open[0] != 20 || r.Contrib[0] != 0 {
		t.Fatalf("A1 pos=%d open=%d contrib=%d want 20,20,0", r.Pos[0], r.Open[0], r.Contrib[0])
	}
	if ge := g.GroupExposure(b("G"), b("S")); ge != 60 {
		t.Fatalf("after fill close g=%d want 60", ge)
	}
	runSteps(t, g, []step{
		ord(14, "c21", "A1", "S", Long, Close, 21, ErrCloseQty),
		hedge(15, "A1", "S", Long, 0, nil), // 调低套保：贡献变 40，g=100
	})
	if ge := g.GroupExposure(b("G"), b("S")); ge != 100 {
		t.Fatalf("after hedge 0 g=%d want 100", ge)
	}
	// 调低 La=30：被动超限，开仓拒绝、平仓与成交照常。
	g.SetLimit(16, b("S"), 30, 150, 1000)
	runSteps(t, g, []step{
		ord(17, "a2o", "A2", "S", Long, Open, 1, ErrAcctLimit),
		// A1 当前持仓 20（>30? 否），用 A1 多头 e=40 验证被动超限后平仓照常：
		ord(18, "a1o", "A1", "S", Long, Open, 1, ErrAcctLimit),
		ord(19, "a1c", "A1", "S", Long, Close, 20, nil),
		fill(20, "a1c", 20, nil),
	})
	r2 := g.AcctRec(b("A1"), b("S"))
	if r2.Pos[0] != 0 || r2.Open[0] != 20 {
		t.Fatalf("A1 pos=%d open=%d want 0,20", r2.Pos[0], r2.Open[0])
	}
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func TestCancelFreesDayOpen(t *testing.T) {
	g := New()
	seedG(t, g)
	g.SetLimit(5, b("S"), 1e9, 1e9, 1000)
	runSteps(t, g, []step{
		ord(6, "o1", "A1", "S", Long, Open, 600, nil),
		fill(7, "o1", 600, nil),
		ord(8, "o2", "A1", "S", Short, Open, 300, nil),
		ord(9, "o3", "A1", "S", Long, Open, 100, nil), // o=1000
		ord(10, "o4", "A1", "S", Long, Open, 1, ErrDayLimit),
		cancel(11, "o2", nil),                          // o=700
		ord(12, "o5", "A1", "S", Long, Open, 300, nil), // o=1000 取等
		ord(13, "o6", "A1", "S", Long, Open, 1, ErrDayLimit),
	})
	if o := g.DayOpen(b("A1"), b("S")); o != 1000 {
		t.Fatalf("day open=%d want 1000", o)
	}
	// ResetDay 只清已成交 600：o=400（100+300 在途）。
	g.ResetDay(20)
	if o := g.DayOpen(b("A1"), b("S")); o != 400 {
		t.Fatalf("after reset day open=%d want 400", o)
	}
	// 日切后还能再开 600；成交不释放日内（在途转已成交）。
	must(g.Order(21, b("o7"), b("A1"), b("S"), Short, Open, 600))
	must(g.Fill(22, b("o7"), 600))
	if o := g.DayOpen(b("A1"), b("S")); o != 1000 {
		t.Fatalf("after fill day open=%d want 1000", o)
	}
}

func TestPartialFillAggregates(t *testing.T) {
	g := New()
	seedG(t, g)
	runSteps(t, g, []step{
		ord(5, "op", "A1", "S", Long, Open, 100, nil),
		fill(6, "op", 40, nil), // 持仓40 在途60，e=100
		cancel(7, "op", nil),   // 释放60：持仓40 在途0，e=40
		ord(8, "cl", "A1", "S", Long, Close, 40, nil),
		fill(9, "cl", 15, nil), // 持仓25，在途平仓25
	})
	r := g.AcctRec(b("A1"), b("S"))
	if r.Pos[0] != 25 || r.Open[0] != 0 || r.Close[0] != 25 {
		t.Fatalf("pos=%d open=%d pendclose=%d want 25,0,25", r.Pos[0], r.Open[0], r.Close[0])
	}
	if g.AcctExposure(b("A1"), b("S"), Long) != 25 {
		t.Fatalf("e=%d want 25", g.AcctExposure(b("A1"), b("S"), Long))
	}
	if c := closeable(g, b("A1"), b("S"), Long); c != 0 {
		t.Fatalf("closeable=%d want 0", c)
	}
	// 在途平仓 25 被撤后，可平量恢复。
	must(g.Cancel(10, b("cl")))
	if c := closeable(g, b("A1"), b("S"), Long); c != 25 {
		t.Fatalf("closeable after cancel=%d want 25", c)
	}
}

func closeable(g *Gateway, acct, sym []byte, side Side) int64 {
	return g.book.Closeable(acct, sym, side)
}

func TestTouchedBound(t *testing.T) {
	for _, n := range []int{2, 5000} {
		g := New()
		g.SetLimit(1, b("S"), 1000, 1_000_000_000, 1_000_000_000)
		for i := 0; i < n; i++ {
			a := "A" + itoa(i)
			must(g.Register(int64(2+i), []byte(a), b("G")))
		}
		for i := 0; i < n; i++ {
			a := "A" + itoa(i)
			must(g.Order(int64(10000+int64(i)), []byte("o"+a), []byte(a), b("S"), Long, Open, 10))
		}
		// 对 A0 的逐笔操作各自只触碰 1 条账户记录，与组员数量无关。
		must(g.Order(30000, b("new"), b("A0"), b("S"), Long, Open, 1))
		if t0 := g.Touched(); t0 != 1 {
			t.Fatalf("n=%d Order touched=%d want 1", n, t0)
		}
		must(g.Fill(30001, b("new"), 1))
		if t0 := g.Touched(); t0 != 1 {
			t.Fatalf("n=%d Fill touched=%d want 1", n, t0)
		}
		must(g.SetHedge(30002, b("A0"), b("S"), Long, 5))
		if t0 := g.Touched(); t0 != 1 {
			t.Fatalf("n=%d SetHedge touched=%d want 1", n, t0)
		}
		must(g.Cancel(30003, b("oA0")))
		if t0 := g.Touched(); t0 != 1 {
			t.Fatalf("n=%d Cancel touched=%d want 1", n, t0)
		}
	}
	// SetLimit 不触碰账户记录：touched 维持上一笔 Fill 的 1 或更低（这里直接验证不递增）。
	g := New()
	must(g.Register(1, b("A"), b("G")))
	g.SetLimit(2, b("S"), 10, 10, 10)
	must(g.Order(3, b("o"), b("A"), b("S"), Long, Open, 1))
	g.SetLimit(4, b("S"), 9, 9, 9)
	if t0 := g.Touched(); t0 != 1 {
		t.Fatalf("after SetLimit touched=%d (SetLimit must not touch account recs)", t0)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [12]byte
	p := len(buf)
	for i > 0 {
		p--
		buf[p] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[p:])
}

func runSteps(t *testing.T, g *Gateway, steps []step) {
	t.Helper()
	for i, st := range steps {
		err := st.f(g)
		if (st.want == nil && err != nil) || (st.want != nil && (err == nil || err.Error() != st.want.Error())) {
			t.Fatalf("step %d (%s): want %v got %v", i, st.desc, st.want, err)
		}
	}
}

func TestThreeLimitsEqualityAndPlusOne(t *testing.T) {
	// 账户限额取等/+1：A3 独占 G2，组限额不干扰。
	g := New()
	seedG(t, g)
	runSteps(t, g, []step{
		ord(5, "aeq", "A3", "S", Long, Open, 100, nil),
		ord(6, "aover", "A3", "S", Long, Open, 1, ErrAcctLimit),
	})
	if e := g.AcctExposure(b("A3"), b("S"), Long); e != 100 {
		t.Fatalf("A3 long e=%d want 100", e)
	}
	// 组限额取等：100(A1)+50(A2)=150；+1 被拒。
	g2 := New()
	seedG(t, g2)
	runSteps(t, g2, []step{
		ord(5, "g1", "A1", "S", Long, Open, 100, nil),
		ord(6, "g2", "A2", "S", Long, Open, 50, nil),
		ord(7, "g3", "A2", "S", Short, Open, 1, ErrGroupLimit),
	})
	if ge := g2.GroupExposure(b("G"), b("S")); ge != 150 {
		t.Fatalf("group exp=%d want 150", ge)
	}
	// 日内取等：600 已成交 + 300 在途 + 100 = 1000。
	g3 := New()
	seedG(t, g3)
	g3.SetLimit(5, b("S"), 1e9, 1e9, 1000)
	runSteps(t, g3, []step{
		ord(6, "d1", "A1", "S", Long, Open, 600, nil),
		fill(7, "d1", 600, nil),
		ord(8, "d2", "A1", "S", Short, Open, 300, nil),
		ord(9, "d3", "A1", "S", Long, Open, 100, nil),
	})
	if o := g3.DayOpen(b("A1"), b("S")); o != 1000 {
		t.Fatalf("day open=%d want 1000", o)
	}
	// 日内 +1。
	g4 := New()
	seedG(t, g4)
	g4.SetLimit(5, b("S"), 1e9, 1e9, 1000)
	runSteps(t, g4, []step{
		ord(6, "d1", "A1", "S", Long, Open, 600, nil),
		fill(7, "d1", 600, nil),
		ord(8, "d2", "A1", "S", Short, Open, 300, nil),
		ord(9, "d3", "A1", "S", Long, Open, 101, ErrDayLimit),
	})
}
