package adjust

import (
	"errors"
	"testing"
)

type op struct {
	kind string
	args []int64
	strs [][]byte
	side Side
	want error
}

func runOps(t *testing.T, ops []op) *Engine {
	t.Helper()
	e := New()
	for i, o := range ops {
		var got error
		switch o.kind {
		case "adv":
			got = e.Advance(o.args[0])
		case "dep":
			got = e.Deposit(o.strs[0], o.args[0])
		case "trade":
			got = e.Trade(o.strs[0], o.strs[1], o.args[0])
		case "freeze":
			got = e.Freeze(o.strs[0], o.strs[1], o.args[0])
		case "unfreeze":
			got = e.Unfreeze(o.strs[0], o.strs[1], o.args[0])
		case "close":
			got = e.SetClose(o.strs[0], o.args[0])
		case "order":
			got = e.PlaceOrder(o.strs[0], o.strs[1], o.strs[2], o.side, o.args[0], o.args[1])
		case "cancelord":
			got = e.CancelOrder(o.strs[0])
		case "ann":
			got = e.Announce(o.strs[0], o.strs[1], o.args[0], o.args[1], o.args[2], o.args[3])
		case "cancelact":
			got = e.CancelAction(o.strs[0])
		default:
			t.Fatalf("unknown op %s", o.kind)
		}
		if !errors.Is(got, o.want) {
			t.Fatalf("op %d (%s): want %v, got %v", i, o.kind, o.want, got)
		}
	}
	return e
}

func TestWorkedExample(t *testing.T) {
	e := runOps(t, []op{
		{"close", []int64{1000}, [][]byte{[]byte("S")}, 0, nil},
		{"trade", []int64{1005}, [][]byte{[]byte("A"), []byte("S")}, 0, nil},
		{"freeze", []int64{333}, [][]byte{[]byte("A"), []byte("S")}, 0, nil},
		{"adv", []int64{5}, nil, 0, nil},
		{"ann", []int64{30, 3, 5, 7}, [][]byte{[]byte("x1"), []byte("S")}, 0, nil},
		{"order", []int64{900, 100}, [][]byte{[]byte("ob"), []byte("A"), []byte("S")}, Buy, nil},
		{"order", []int64{900, 100}, [][]byte{[]byte("os"), []byte("A"), []byte("S")}, Sell, nil},
		{"adv", []int64{7}, nil, 0, nil},
	})
	res, err := e.Result([]byte("x1"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Pex != 767 {
		t.Fatalf("pex = %d, want 767", res.Pex)
	}
	if len(res.Awards) != 1 {
		t.Fatalf("awards = %+v", res.Awards)
	}
	aw := res.Awards[0]
	if string(aw.Acct) != "A" || aw.Shares != 301 || aw.SharesFroz != 99 ||
		aw.Cash != 3015 || aw.CashFroz != 999 || aw.FragCash != 383 {
		t.Fatalf("award = %+v", aw)
	}
	if len(res.Orders) != 2 {
		t.Fatalf("orders = %+v", res.Orders)
	}
	if string(res.Orders[0].OID) != "ob" || res.Orders[0].NewPrice != 690 || res.Orders[0].Canceled {
		t.Fatalf("buy adj = %+v", res.Orders[0])
	}
	if string(res.Orders[1].OID) != "os" || res.Orders[1].NewPrice != 691 || res.Orders[1].Canceled {
		t.Fatalf("sell adj = %+v", res.Orders[1])
	}
	if p, _ := e.book.Close([]byte("S")); p != 767 {
		t.Fatalf("close after ex = %d", p)
	}
	q, f, _ := e.book.Position([]byte("A"), []byte("S"))
	if q != 1306 || f != 432 {
		t.Fatalf("pos = %d,%d", q, f)
	}
	avail, froz, _ := e.book.Cash([]byte("A"))
	if avail != 2399 || froz != 999 {
		t.Fatalf("cash = %d,%d", avail, froz)
	}
}

func TestRefPriceRounding(t *testing.T) {
	cases := []struct {
		p, c, b, want int64
	}{
		{13, 0, 10, 7},
		{12, 0, 10, 6},
		{23, 0, 10, 12},
		{21, 0, 10, 11},
		{1000, 30, 3, 767},
		{10, 99, 100, 1},
	}
	for _, tc := range cases {
		if got := refPrice(tc.p, tc.c, tc.b); got != tc.want {
			t.Errorf("refPrice(%d,%d,%d)=%d want %d", tc.p, tc.c, tc.b, got, tc.want)
		}
	}
}

func TestBuyBelowOneCanceled(t *testing.T) {
	e := runOps(t, []op{
		{"close", []int64{100}, [][]byte{[]byte("S")}, 0, nil},
		{"trade", []int64{10}, [][]byte{[]byte("A"), []byte("S")}, 0, nil},
		{"ann", []int64{990, 0, 0, 1}, [][]byte{[]byte("x1"), []byte("S")}, 0, nil},
		{"order", []int64{1, 5}, [][]byte{[]byte("ob"), []byte("A"), []byte("S")}, Buy, nil},
		{"order", []int64{1, 5}, [][]byte{[]byte("os"), []byte("A"), []byte("S")}, Sell, nil},
		{"adv", []int64{1}, nil, 0, nil},
	})
	res, _ := e.Result([]byte("x1"))
	if res.Pex != 1 {
		t.Fatalf("pex = %d", res.Pex)
	}
	if len(res.Orders) != 2 {
		t.Fatalf("orders = %+v", res.Orders)
	}
	if !res.Orders[0].Canceled || res.Orders[0].NewPrice != 0 {
		t.Fatalf("buy should be canceled: %+v", res.Orders[0])
	}
	if res.Orders[1].Canceled || res.Orders[1].NewPrice != 1 {
		t.Fatalf("sell should clamp to 1: %+v", res.Orders[1])
	}
	if err := e.CancelOrder([]byte("ob")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("canceled buy should be gone, got %v", err)
	}
	if err := e.CancelOrder([]byte("os")); err != nil {
		t.Fatalf("sell should remain, got %v", err)
	}
}

func TestRightsOnlyFromSnapshot(t *testing.T) {
	e := runOps(t, []op{
		{"close", []int64{1000}, [][]byte{[]byte("S")}, 0, nil},
		{"trade", []int64{1005}, [][]byte{[]byte("A"), []byte("S")}, 0, nil},
		{"adv", []int64{5}, nil, 0, nil},
		{"ann", []int64{30, 3, 5, 7}, [][]byte{[]byte("x1"), []byte("S")}, 0, nil},
		{"adv", []int64{6}, nil, 0, nil},
		{"trade", []int64{-672}, [][]byte{[]byte("A"), []byte("S")}, 0, nil},
		{"trade", []int64{672}, [][]byte{[]byte("B"), []byte("S")}, 0, nil},
		{"adv", []int64{7}, nil, 0, nil},
	})
	res, _ := e.Result([]byte("x1"))
	if len(res.Awards) != 1 || string(res.Awards[0].Acct) != "A" ||
		res.Awards[0].Shares != 301 {
		t.Fatalf("awards = %+v", res.Awards)
	}
	qA, _, _ := e.book.Position([]byte("A"), []byte("S"))
	qB, _, _ := e.book.Position([]byte("B"), []byte("S"))
	if qA != 634 || qB != 672 {
		t.Fatalf("qA=%d qB=%d", qA, qB)
	}
}

func TestAdvanceCrossesBoth(t *testing.T) {
	e := runOps(t, []op{
		{"close", []int64{13}, [][]byte{[]byte("S")}, 0, nil},
		{"trade", []int64{20}, [][]byte{[]byte("A"), []byte("S")}, 0, nil},
		{"adv", []int64{5}, nil, 0, nil},
		{"ann", []int64{0, 10, 5, 7}, [][]byte{[]byte("x1"), []byte("S")}, 0, nil},
		{"adv", []int64{7}, nil, 0, nil},
	})
	res, err := e.Result([]byte("x1"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Pex != 7 {
		t.Fatalf("pex = %d", res.Pex)
	}
	if res.Awards[0].Shares != 20 {
		t.Fatalf("shares = %d", res.Awards[0].Shares)
	}
}

func TestCancelActionWindow(t *testing.T) {
	e := runOps(t, []op{
		{"close", []int64{100}, [][]byte{[]byte("S")}, 0, nil},
		{"adv", []int64{2}, nil, 0, nil},
		{"ann", []int64{1, 1, 2, 4}, [][]byte{[]byte("x1"), []byte("S")}, 0, nil},
		{"adv", []int64{3}, nil, 0, nil},
		{"cancelact", nil, [][]byte{[]byte("x1")}, 0, ErrBadState},
	})
	if _, err := e.Result([]byte("x1")); !errors.Is(err, ErrBadState) {
		t.Fatalf("result before ex: %v", err)
	}
	if _, err := e.Result([]byte("nope")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing result: %v", err)
	}
	runOps(t, []op{
		{"close", []int64{100}, [][]byte{[]byte("S")}, 0, nil},
		{"adv", []int64{5}, nil, 0, nil},
		{"ann", []int64{1, 1, 5, 7}, [][]byte{[]byte("x1"), []byte("S")}, 0, nil},
		{"cancelact", nil, [][]byte{[]byte("x1")}, 0, nil},
	})
}

func TestAnnounceChecks(t *testing.T) {
	e0 := runOps(t, []op{
		{"close", []int64{100}, [][]byte{[]byte("S")}, 0, nil},
		{"adv", []int64{2}, nil, 0, nil},
		{"ann", []int64{1, 1, 2, 4}, [][]byte{[]byte("x1"), []byte("S")}, 0, nil},
		{"ann", []int64{1, 1, 2, 4}, [][]byte{[]byte("x2"), []byte("S")}, 0, ErrConflict},
		{"ann", []int64{1, 1, 2, 4}, [][]byte{[]byte("x3"), []byte("T")}, 0, ErrNoRefPrice},
		{"ann", []int64{1, 1, 9, 10}, [][]byte{[]byte("x4"), []byte("T")}, 0, ErrBadState},
		{"ann", []int64{1, 1, 9, 10}, [][]byte{[]byte("x1"), []byte("T")}, 0, ErrDuplicateID},
		// 非法参数（c,b 同为 0）先于一切。
		{"ann", []int64{0, 0, 9, 10}, [][]byte{[]byte("x1"), []byte("T")}, 0, ErrInvalidParam},
	})
	// 被拒行动不落状态：x3 未登记。
	_ = e0
	e := New()
	_ = e.SetClose([]byte("S"), 100)
	_ = e.Advance(2)
	_ = e.Announce([]byte("x1"), []byte("S"), 1, 1, 2, 4)
	if err := e.Announce([]byte("x3"), []byte("T"), 1, 1, 2, 4); !errors.Is(err, ErrNoRefPrice) {
		t.Fatal(err)
	}
	if _, err := e.Result([]byte("x3")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("rejected announce must not register, got %v", err)
	}
}

func TestRejectPrecedence(t *testing.T) {
	e := New()
	// Advance：参数非法先于回退。
	if err := e.Advance(-1); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("adv -1: %v", err)
	}
	_ = e.Advance(5)
	if err := e.Advance(4); !errors.Is(err, ErrDateRollback) {
		t.Fatalf("rollback: %v", err)
	}
	// Trade：非法参数（空账户）先于账户不存在；卖出未知账户报不存在先于数量不足。
	if err := e.Trade(nil, []byte("S"), 1); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("trade empty acct: %v", err)
	}
	if err := e.Trade([]byte("X"), []byte("S"), -1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("sell missing acct: %v", err)
	}
	// Freeze 未知账户。
	if err := e.Freeze([]byte("X"), []byte("S"), 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("freeze missing acct: %v", err)
	}
	// 账户存在但数量不足。
	_ = e.Deposit([]byte("A"), 1)
	if err := e.Trade([]byte("A"), []byte("S"), -1); !errors.Is(err, ErrInsufficient) {
		t.Fatalf("oversell: %v", err)
	}
	if err := e.Freeze([]byte("A"), []byte("S"), 1); !errors.Is(err, ErrInsufficient) {
		t.Fatalf("overfreeze: %v", err)
	}
	// 委托：参数非法先于重复。
	if err := e.PlaceOrder([]byte("o1"), []byte("A"), []byte("S"), Buy, 10, 1); err != nil {
		t.Fatal(err)
	}
	if err := e.PlaceOrder([]byte("o1"), nil, []byte("S"), Buy, 10, 1); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("order invalid before dup: %v", err)
	}
	if err := e.PlaceOrder([]byte("o1"), []byte("A"), []byte("S"), Buy, 10, 1); !errors.Is(err, ErrDuplicateID) {
		t.Fatalf("dup order: %v", err)
	}
	if err := e.CancelOrder([]byte("zzz")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cancel missing: %v", err)
	}
	// 被拒操作不改状态：卖出失败后持仓仍为 0。
	if q, _, _ := e.book.Position([]byte("A"), []byte("S")); q != 0 {
		t.Fatalf("pos changed after reject: %d", q)
	}
}

func TestFrozenRoundingDifference(t *testing.T) {
	// 冻结部分与总量分别取整再相减：q=13,f=12,b=3
	// n=floor(39/10)=3, nf=floor(36/10)=3, 可用得 0。
	e := runOps(t, []op{
		{"close", []int64{100}, [][]byte{[]byte("S")}, 0, nil},
		{"trade", []int64{13}, [][]byte{[]byte("A"), []byte("S")}, 0, nil},
		{"freeze", []int64{12}, [][]byte{[]byte("A"), []byte("S")}, 0, nil},
		{"ann", []int64{0, 3, 0, 1}, [][]byte{[]byte("x1"), []byte("S")}, 0, nil},
		{"adv", []int64{1}, nil, 0, nil},
	})
	res, _ := e.Result([]byte("x1"))
	aw := res.Awards[0]
	if aw.Shares != 3 || aw.SharesFroz != 3 {
		t.Fatalf("award = %+v", aw)
	}
	q, f, _ := e.book.Position([]byte("A"), []byte("S"))
	// q=16,f=15，可用仍为 1（未因取整多发到可用）。
	if q != 16 || f != 15 || q-f != 1 {
		t.Fatalf("pos = %d,%d avail=%d", q, f, q-f)
	}
}
