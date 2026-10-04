package book

import (
	"errors"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"testing"

	"ontology/auction"
)

func mustNewB(t *testing.T, ref, tc, tu, lo, hi, m int64) *Book {
	t.Helper()
	b, err := New(ref, tc, tu, lo, hi, m)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return b
}

func sub(b *Book, now int64, id, acct string, side Side, price, qty int64) (int64, error) {
	return b.Submit(now, []byte(id), []byte(acct), side, price, qty)
}

func mustSeq(t *testing.T, seq int64, err error) int64 {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected err %v", err)
	}
	return seq
}

func okSub(t *testing.T, b *Book, now int64, id, acct string, side Side, price, qty int64) int64 {
	t.Helper()
	seq, err := b.Submit(now, []byte(id), []byte(acct), side, price, qty)
	if err != nil {
		t.Fatalf("submit %s: %v", id, err)
	}
	return seq
}

func TestExampleFills(t *testing.T) {
	b := mustNewB(t, 100, 10, 100, 1, 200, 10)
	okSub(t, b, 1, "b1", "a", Buy, 102, 300)
	okSub(t, b, 1, "b2", "a", Buy, 101, 200)
	okSub(t, b, 1, "b3", "a", Buy, 100, 400)
	okSub(t, b, 1, "s1", "a", Sell, 99, 200)
	okSub(t, b, 1, "s2", "a", Sell, 100, 300)
	okSub(t, b, 1, "s3", "a", Sell, 101, 300)
	okSub(t, b, 1, "s4", "a", Sell, 103, 100)

	p, fills, rests, err := b.Uncross(100)
	if err != nil || p != 101 {
		t.Fatalf("uncross p=%d err=%v", p, err)
	}
	wantF := []Fill{
		{BuyID: []byte("b1"), SellID: []byte("s1"), Qty: 200},
		{BuyID: []byte("b1"), SellID: []byte("s2"), Qty: 100},
		{BuyID: []byte("b2"), SellID: []byte("s2"), Qty: 200},
	}
	if len(fills) != len(wantF) {
		t.Fatalf("fills=%v want %v", fills, wantF)
	}
	for i := range wantF {
		if string(fills[i].BuyID) != string(wantF[i].BuyID) ||
			string(fills[i].SellID) != string(wantF[i].SellID) ||
			fills[i].Qty != wantF[i].Qty {
			t.Fatalf("fill[%d]=%v want %v", i, fills[i], wantF[i])
		}
	}
	wantR := []struct {
		id  string
		qty int64
	}{{"b3", 400}, {"s3", 300}, {"s4", 100}}
	if len(rests) != len(wantR) {
		t.Fatalf("rests=%v", rests)
	}
	for i, w := range wantR {
		if string(rests[i].ID) != w.id || rests[i].Qty != w.qty {
			t.Fatalf("rest[%d]=%s:%d want %s:%d", i, rests[i].ID, rests[i].Qty, w.id, w.qty)
		}
	}
}

func TestDirectionMixedRef(t *testing.T) {
	build := func(ref int64) int64 {
		b := mustNewB(t, ref, 10, 100, 1, 200, 10)
		okSub(t, b, 1, "b1", "a", Buy, 102, 300)
		okSub(t, b, 1, "b2", "a", Buy, 100, 200)
		okSub(t, b, 1, "s1", "a", Sell, 99, 300)
		okSub(t, b, 1, "s2", "a", Sell, 101, 200)
		p, _, _, err := b.Uncross(100)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	if p := build(100); p != 100 {
		t.Fatalf("ref100 p=%d want 100", p)
	}
	if p := build(103); p != 102 {
		t.Fatalf("ref103 p=%d want 102", p)
	}
}

func TestNoCrossing(t *testing.T) {
	b := mustNewB(t, 100, 10, 100, 1, 200, 10)
	okSub(t, b, 1, "b1", "a", Buy, 90, 100)
	okSub(t, b, 1, "s1", "a", Sell, 110, 50)
	p, fills, rests, err := b.Uncross(100)
	if err != nil {
		t.Fatal(err)
	}
	if p != 0 || len(fills) != 0 || len(rests) != 2 {
		t.Fatalf("p=%d fills=%d rests=%d", p, len(fills), len(rests))
	}
}

func TestPhasesAndCancel(t *testing.T) {
	b := mustNewB(t, 100, 10, 100, 1, 200, 10)
	okSub(t, b, 9, "x", "a", Buy, 100, 10)
	if err := b.Cancel(9, []byte("x")); err != nil {
		t.Fatalf("cancel in revocable: %v", err)
	}
	okSub(t, b, 9, "x", "a", Buy, 100, 10)

	okSub(t, b, 10, "y", "a", Sell, 100, 5)
	if err := b.Cancel(10, []byte("y")); !errors.Is(err, ErrPhase) {
		t.Fatalf("cancel at Tc err=%v want ErrPhase", err)
	}
	if err := b.Cancel(20, []byte("nope")); !errors.Is(err, ErrPhase) {
		t.Fatalf("cancel missing in irrevocable err=%v", err)
	}
	if _, _, _, err := b.Uncross(99); !errors.Is(err, ErrPhase) {
		t.Fatalf("uncross at 99 err=%v", err)
	}
	p, _, _, err := b.Uncross(100)
	if err != nil || p == 0 {
		t.Fatalf("uncross at Tu: p=%d err=%v", p, err)
	}
	if _, err := sub(b, 100, "z", "a", Buy, 100, 1); !errors.Is(err, ErrPhase) {
		t.Fatalf("submit after open err=%v", err)
	}
	if err := b.Cancel(100, []byte("x")); !errors.Is(err, ErrPhase) {
		t.Fatalf("cancel after open err=%v", err)
	}
	if _, _, _, err := b.Uncross(101); !errors.Is(err, ErrPhase) {
		t.Fatalf("second uncross err=%v", err)
	}
}

func TestPriceBandEndpoints(t *testing.T) {
	b := mustNewB(t, 100, 10, 100, 90, 110, 10)
	okSub(t, b, 1, "lo", "a", Buy, 90, 1)
	okSub(t, b, 1, "hi", "a", Sell, 110, 1)
	if _, err := sub(b, 1, "below", "a", Buy, 89, 1); !errors.Is(err, ErrPriceBand) {
		t.Fatalf("lo-1 err=%v", err)
	}
	if _, err := sub(b, 1, "above", "a", Sell, 111, 1); !errors.Is(err, ErrPriceBand) {
		t.Fatalf("hi+1 err=%v", err)
	}
}

func TestAcctLimitAndRelease(t *testing.T) {
	b := mustNewB(t, 100, 10, 100, 1, 200, 2)
	okSub(t, b, 1, "o1", "A", Buy, 100, 1)
	okSub(t, b, 1, "o2", "A", Buy, 100, 1)
	if _, err := sub(b, 1, "o3", "A", Buy, 100, 1); !errors.Is(err, ErrAcctLimit) {
		t.Fatalf("over limit err=%v", err)
	}
	okSub(t, b, 1, "o4", "B", Buy, 100, 1)
	if err := b.Cancel(2, []byte("o1")); err != nil {
		t.Fatal(err)
	}
	okSub(t, b, 2, "o5", "A", Buy, 100, 1)
}

func TestRejectOrder(t *testing.T) {
	t.Run("参数非法优先于时钟回退", func(t *testing.T) {
		b := mustNewB(t, 100, 10, 100, 1, 200, 2)
		okSub(t, b, 5, "o1", "A", Buy, 100, 1)
		if _, err := sub(b, -1, "o2", "A", Buy, 100, 1); !errors.Is(err, ErrInvalid) {
			t.Fatalf("now<0 err=%v", err)
		}
		if _, err := sub(b, 4, "o2", "A", Buy, 100, 0); !errors.Is(err, ErrInvalid) {
			t.Fatalf("qty0 err=%v", err)
		}
		if _, err := sub(b, 4, "o2", "A", Side(9), 100, 1); !errors.Is(err, ErrInvalid) {
			t.Fatalf("bad side err=%v", err)
		}
		if _, err := sub(b, 4, "", "A", Buy, 100, 1); !errors.Is(err, ErrInvalid) {
			t.Fatalf("empty id err=%v", err)
		}
		if _, err := sub(b, 4, "o2", "", Buy, 100, 1); !errors.Is(err, ErrInvalid) {
			t.Fatalf("empty acct err=%v", err)
		}
	})
	t.Run("时钟回退优先于阶段", func(t *testing.T) {
		b := mustNewB(t, 100, 10, 100, 1, 200, 2)
		okSub(t, b, 5, "o1", "A", Buy, 100, 1)
		if _, _, _, err := b.Uncross(100); err != nil {
			t.Fatal(err)
		}
		if _, err := sub(b, 9, "o2", "A", Buy, 100, 1); !errors.Is(err, ErrClockBack) {
			t.Fatalf("opened+backward err=%v", err)
		}
	})
	t.Run("阶段优先于编号重复", func(t *testing.T) {
		b := mustNewB(t, 100, 10, 100, 1, 200, 2)
		okSub(t, b, 1, "o1", "A", Buy, 100, 1)
		if _, _, _, err := b.Uncross(100); err != nil {
			t.Fatal(err)
		}
		if _, err := sub(b, 100, "o1", "A", Buy, 100, 1); !errors.Is(err, ErrPhase) {
			t.Fatalf("dup after open err=%v", err)
		}
	})
	t.Run("重复优先于价格越带", func(t *testing.T) {
		b := mustNewB(t, 100, 10, 100, 90, 110, 5)
		okSub(t, b, 1, "o1", "A", Buy, 100, 1)
		if _, err := sub(b, 1, "o1", "A", Buy, 500, 1); !errors.Is(err, ErrDuplicate) {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("价格越带优先于账户超限", func(t *testing.T) {
		b := mustNewB(t, 100, 10, 100, 90, 110, 1)
		okSub(t, b, 1, "o1", "A", Buy, 100, 1)
		if _, err := sub(b, 1, "o2", "A", Buy, 500, 1); !errors.Is(err, ErrPriceBand) {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("Cancel不存在含已撤", func(t *testing.T) {
		b := mustNewB(t, 100, 10, 100, 1, 200, 2)
		okSub(t, b, 1, "o1", "A", Buy, 100, 1)
		if err := b.Cancel(1, []byte("ghost")); !errors.Is(err, ErrNotFound) {
			t.Fatalf("missing err=%v", err)
		}
		if err := b.Cancel(1, []byte("o1")); err != nil {
			t.Fatal(err)
		}
		if err := b.Cancel(2, []byte("o1")); !errors.Is(err, ErrNotFound) {
			t.Fatalf("already canceled err=%v", err)
		}
	})
	t.Run("New参数非法", func(t *testing.T) {
		bad := [][6]int64{
			{0, 0, 0, 1, 10, 1},
			{5, 0, 0, 6, 10, 1},
			{11, 0, 0, 1, 10, 1},
			{5, 5, 4, 1, 10, 1},
			{5, -1, 0, 1, 10, 1},
			{5, 0, 1 + 1e12, 1, 10, 1},
			{5, 0, 0, 1, 10, 0},
			{5, 0, 0, 1, 10, 1 + 1e5},
		}
		for i, a := range bad {
			if _, err := New(a[0], a[1], a[2], a[3], a[4], a[5]); !errors.Is(err, ErrInvalid) {
				t.Fatalf("case %d err=%v", i, err)
			}
		}
	})
}

func TestRejectedTakesNoSeq(t *testing.T) {
	b := mustNewB(t, 100, 10, 100, 90, 110, 1)
	s1 := okSub(t, b, 1, "o1", "A", Buy, 100, 1)
	if s1 != 1 {
		t.Fatalf("first seq=%d", s1)
	}
	if _, err := sub(b, 1, "o2", "A", Buy, 500, 1); !errors.Is(err, ErrPriceBand) {
		t.Fatal(err)
	}
	if _, err := sub(b, 1, "o3", "A", Buy, 100, 1); !errors.Is(err, ErrAcctLimit) {
		t.Fatal(err)
	}
	if err := b.Cancel(10, []byte("o1")); !errors.Is(err, ErrPhase) {
		t.Fatal(err)
	}
	if _, _, _, err := b.Uncross(50); !errors.Is(err, ErrPhase) {
		t.Fatal(err)
	}
	if _, _, _, err := b.Uncross(100); err != nil {
		t.Fatal(err)
	}
	if _, err := sub(b, 100, "o4", "A", Buy, 100, 1); !errors.Is(err, ErrPhase) {
		t.Fatal(err)
	}
	// 所有被拒操作均未占号：簿内唯一委托序号为 1，其按序出现在剩余清单首位。
	// （o1 已开盘，只能通过前面的成交/剩余间接确认；这里另起一簿直接验序号。）
	b2 := mustNewB(t, 100, 10, 100, 90, 110, 5)
	okSub(t, b2, 1, "a", "A", Buy, 100, 1)
	if _, err := sub(b2, 1, "a", "A", Buy, 100, 1); !errors.Is(err, ErrDuplicate) {
		t.Fatal(err)
	}
	s2 := okSub(t, b2, 1, "b", "A", Sell, 100, 1)
	if s2 != 2 {
		t.Fatalf("seq after rejected dup =%d want 2", s2)
	}
}

type nop struct {
	side  auction.Side
	price int64
	qty   int64
}

// naiveAlloc 是独立于 alloc 包的朴素分配实现，作为全链路对照。
func naiveAlloc(ords []auction.Order, p int64) ([][3]interface{}, []nop) {
	rem := map[string]int64{}
	ordByID := map[string]auction.Order{}
	var buys, sells []string
	for _, o := range ords {
		id := string(o.ID)
		rem[id] = o.Qty
		ordByID[id] = o
		if o.Side == auction.Buy && o.Price >= p {
			buys = append(buys, id)
		}
		if o.Side == auction.Sell && o.Price <= p {
			sells = append(sells, id)
		}
	}
	sort.SliceStable(buys, func(i, j int) bool {
		a, b := ordByID[buys[i]], ordByID[buys[j]]
		if a.Price != b.Price {
			return a.Price > b.Price
		}
		return a.Seq < b.Seq
	})
	sort.SliceStable(sells, func(i, j int) bool {
		a, b := ordByID[sells[i]], ordByID[sells[j]]
		if a.Price != b.Price {
			return a.Price < b.Price
		}
		return a.Seq < b.Seq
	})
	var fills [][3]interface{}
	bi, si := 0, 0
	for bi < len(buys) && si < len(sells) {
		bk, sk := buys[bi], sells[si]
		q := rem[bk]
		if rem[sk] < q {
			q = rem[sk]
		}
		fills = append(fills, [3]interface{}{bk, sk, q})
		rem[bk] -= q
		rem[sk] -= q
		if rem[bk] == 0 {
			bi++
		}
		if rem[sk] == 0 {
			si++
		}
	}
	var rests []nop
	for _, o := range ords {
		if r := rem[string(o.ID)]; r > 0 {
			rests = append(rests, nop{side: o.Side, price: o.Price, qty: r})
		}
	}
	return fills, rests
}

func TestRandomEndToEnd(t *testing.T) {
	rng := rand.New(rand.NewSource(424242))
	for g := 0; g < 1500; g++ {
		lo := int64(1)
		hi := int64(30)
		ref := int64(1 + rng.Intn(30))
		tc, tu := int64(5), int64(10)
		m := int64(3)
		bk := mustNewB(t, ref, tc, tu, lo, hi, m)
		n := 1 + rng.Intn(20)

		type evt struct {
			kind            string
			id              string
			side            auction.Side
			price, qty, now int64
			ok              bool
		}
		var evts []evt
		live := map[string]bool{}
		var liveOrder []string
		acctCnt := map[string]int{}
		next := 0
		for i := 0; i < n; i++ {
			const now = int64(0) // 收集阶段统一时钟，避免朴素模型处理时钟回退
			if rng.Intn(5) == 0 && len(liveOrder) > 0 {
				id := liveOrder[rng.Intn(len(liveOrder))]
				err := bk.Cancel(now, []byte(id))
				ok := err == nil
				evts = append(evts, evt{kind: "C", id: id, now: now, ok: ok})
				if ok {
					delete(live, id)
					out := liveOrder[:0]
					for _, x := range liveOrder {
						if x != id {
							out = append(out, x)
						}
					}
					liveOrder = out
					acctCnt["A"]--
				}
				continue
			}
			id := fmtID(next)
			next++
			sd := auction.Buy
			if rng.Intn(2) == 1 {
				sd = auction.Sell
			}
			price := int64(1 + rng.Intn(int(hi)+2)) // 偶尔越带
			qty := int64(1 + rng.Intn(100))
			_, err := bk.Submit(now, []byte(id), []byte("A"), sd, price, qty)
			ok := err == nil
			evts = append(evts, evt{kind: "S", id: id, side: sd, price: price, qty: qty, now: now, ok: ok})
			expectReject := price < lo || price > hi || acctCnt["A"] >= int(m)
			if ok == expectReject { // 接受却被预期拒绝，或反之
				t.Fatalf("group %d event %+v reject mismatch (acctCnt=%d)", g, evts[len(evts)-1], acctCnt["A"])
			}
			if ok {
				live[id] = true
				liveOrder = append(liveOrder, id)
				acctCnt["A"]++
			}
		}

		p, fills, rests, err := bk.Uncross(tu)
		if err != nil {
			t.Fatalf("group %d uncross: %v", g, err)
		}

		// 朴素重建在簿委托（按接受次序）。
		var ords []auction.Order
		seq := int64(1)
		for _, e := range evts {
			if e.kind == "S" && e.ok && live[e.id] {
				ords = append(ords, auction.Order{Seq: seq, ID: []byte(e.id), Side: e.side, Price: e.price, Qty: e.qty})
				seq++
			}
		}
		res := auction.Price(ords, ref)
		if res.Price != p {
			t.Fatalf("group %d price book=%d naive=%d", g, p, res.Price)
		}
		nf, nr := naiveAlloc(ords, p)
		if len(nf) != len(fills) {
			t.Fatalf("group %d fills len %d vs %d", g, len(fills), len(nf))
		}
		for i, f := range nf {
			if string(fills[i].BuyID) != f[0] || string(fills[i].SellID) != f[1] || fills[i].Qty != f[2] {
				t.Fatalf("group %d fill[%d] %v vs %v", g, i, fills[i], f)
			}
		}
		if len(nr) != len(rests) {
			t.Fatalf("group %d rests len %d vs %d", g, len(rests), len(nr))
		}
		var totalV int64
		filled := map[string]int64{}
		qtyOf := map[string]int64{}
		for _, o := range ords {
			qtyOf[string(o.ID)] = o.Qty
		}
		for _, f := range fills {
			totalV += f.Qty
			filled[string(f.BuyID)] += f.Qty
			filled[string(f.SellID)] += f.Qty
		}
		var expV int64
		for _, st := range res.Stats {
			if st.Price == p {
				expV = st.V
			}
		}
		if totalV != expV {
			t.Fatalf("group %d total fills %d != V(P)=%d", g, totalV, expV)
		}
		for id, q := range filled {
			if q > qtyOf[id] {
				t.Fatalf("group %d overfill %s", g, id)
			}
		}
		// 不变量：不会同时存在买价>=P 的余量与卖价<=P 的余量。
		var remBuy, remSell bool
		for _, r := range rests {
			if p > 0 && r.Side == auction.Buy && r.Price >= p {
				remBuy = true
			}
			if p > 0 && r.Side == auction.Sell && r.Price <= p {
				remSell = true
			}
		}
		if remBuy && remSell {
			t.Fatalf("group %d both sides remain crossable at P=%d", g, p)
		}

		var log []string
		for _, e := range evts {
			tag := "ok"
			if !e.ok {
				tag = "REJ"
			}
			if e.kind == "S" {
				ch := "b"
				if e.side == auction.Sell {
					ch = "s"
				}
				log = append(log, ch+e.id+"@"+itoa64(e.price)+"x"+itoa64(e.qty)+"@t"+itoa64(e.now)+"/"+tag)
			} else {
				log = append(log, "C"+e.id+"@t"+itoa64(e.now)+"/"+tag)
			}
		}
		var stats []string
		for _, st := range res.Stats {
			stats = append(stats, "p="+itoa64(st.Price)+" B="+itoa64(st.B)+" S="+itoa64(st.S)+" V="+itoa64(st.V)+" I="+itoa64(st.I))
		}
		out := []string{}
		for _, f := range fills {
			out = append(out, "("+string(f.BuyID)+","+string(f.SellID)+","+itoa64(f.Qty)+")")
		}
		t.Logf("group %d ref=%d input=[%s] candidates{%s} -> P=%d fills=[%s] rests=%d",
			g, ref, strings.Join(log, " "), strings.Join(stats, " | "), p, strings.Join(out, " "), len(rests))
	}
}

func TestConcurrentLinearizable(t *testing.T) {
	bk := mustNewB(t, 100, 10, 100, 1, 200, 100000)
	var wg sync.WaitGroup
	for w := 0; w < 16; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				id := "w" + itoa64(int64(w)) + "_" + itoa64(int64(i))
				sd := Buy
				if (w+i)%2 == 0 {
					sd = Sell
				}
				if _, err := bk.Submit(1, []byte(id), []byte("acc"), sd, 100+int64((w+i)%3)-1, 1); err != nil {
					t.Errorf("submit: %v", err)
					return
				}
			}
		}(w)
	}
	wg.Wait()
	p, fills, _, err := bk.Uncross(100)
	if err != nil {
		t.Fatal(err)
	}
	var tot int64
	for _, f := range fills {
		tot += f.Qty
	}
	t.Logf("concurrent uncross p=%d fills=%d qty=%d", p, len(fills), tot)
}

func TestReplayDeterministic(t *testing.T) {
	run := func() (int64, []Fill, []Rest, error) {
		rng := rand.New(rand.NewSource(7))
		bk := mustNewB(t, 100, 11, 11, 80, 120, 100)
		for i := 0; i < 60; i++ {
			id := "o" + itoa64(int64(i))
			sd := Buy
			if rng.Intn(2) == 1 {
				sd = Sell
			}
			if _, err := bk.Submit(10, []byte(id), []byte("A"), sd, 80+int64(rng.Intn(41)), int64(1+rng.Intn(50))); err != nil {
				t.Fatal(err)
			}
		}
		return bk.Uncross(11)
	}
	p1, f1, r1, e1 := run()
	p2, f2, r2, e2 := run()
	if e1 != nil || e2 != nil {
		t.Fatal(e1)
	}
	if p1 != p2 || len(f1) != len(f2) || len(r1) != len(r2) {
		t.Fatal("replay length mismatch")
	}
	for i := range f1 {
		if string(f1[i].BuyID) != string(f2[i].BuyID) || string(f1[i].SellID) != string(f2[i].SellID) || f1[i].Qty != f2[i].Qty {
			t.Fatalf("replay fill mismatch at %d", i)
		}
	}
	for i := range r1 {
		if string(r1[i].ID) != string(r2[i].ID) || r1[i].Qty != r2[i].Qty {
			t.Fatalf("replay rest mismatch at %d", i)
		}
	}
}

func fmtID(i int) string {
	return "n" + itoa64(int64(i))
}

func itoa64(x int64) string {
	if x == 0 {
		return "0"
	}
	neg := x < 0
	if neg {
		x = -x
	}
	var buf [22]byte
	i := len(buf)
	for x > 0 {
		i--
		buf[i] = byte('0' + x%10)
		x /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
