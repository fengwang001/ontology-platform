package adjust

import (
	"errors"
	"reflect"
	"testing"

	"ontology/corpact"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestComputePex(t *testing.T) {
	cases := []struct {
		name    string
		p, c, b int64
		want    int64
	}{
		{"规范例", 1000, 30, 3, 767},   // 9970/13 = 766.92... -> 767
		{"半分进位", 13, 0, 10, 7},      // 130/20 = 6.5 -> 7
		{"不足半分舍去", 100, 7, 0, 99},   // 993/10 = 99.3 -> 99
		{"恰为半分进位2", 100, 5, 0, 100}, // 995/10 = 99.5 -> 100
		{"低于1钳位", 10, 99, 0, 1},     // 1/10 = 0.1 -> 0 -> 1
		{"分子为负钳位", 1, 1000000, 0, 1},
		{"大值", 1000000000, 0, 1, 909090909}, // 1e10/11 = 909090909.09...
	}
	for _, tc := range cases {
		if got := computePex(tc.p, tc.c, tc.b); got != tc.want {
			t.Errorf("%s: computePex(%d,%d,%d) = %d, want %d",
				tc.name, tc.p, tc.c, tc.b, got, tc.want)
		}
	}
}

// 规范主例：P=1000、c=30、b=3，账户 A 快照 q=1005、f=333。
func TestSpecMainExample(t *testing.T) {
	e := New()
	must(t, e.Deposit("A", 100))
	must(t, e.Trade("A", "S", 1005))
	must(t, e.Freeze("A", "S", 333))
	must(t, e.SetClose("S", 1000))
	must(t, e.Announce("act", "S", 30, 3, 1, 2))
	must(t, e.PlaceOrder("o1", "A", "S", Buy, 900, 10))
	must(t, e.PlaceOrder("o2", "A", "S", Sell, 900, 10))
	must(t, e.Advance(2))

	res, err := e.Result("act")
	must(t, err)
	want := &corpact.Result{
		Pex: 767,
		Gains: []corpact.AccountGain{{
			Acct:         "A",
			Shares:       301, // floor(1005*3/10)
			FrozenShares: 99,  // floor(333*3/10)
			Cash:         3015,
			FrozenCash:   999,
			FractionCash: 383, // floor(5*767/10)
		}},
		Orders: []corpact.OrderAdj{
			{Oid: "o1", OldPrice: 900, NewPrice: 690}, // 买单 floor
			{Oid: "o2", OldPrice: 900, NewPrice: 691}, // 卖单 ceil
		},
	}
	if !reflect.DeepEqual(res, want) {
		t.Fatalf("result = %+v\nwant %+v", res, want)
	}

	// 可用部分得 n-nf=202 股，而非对可用持仓单独取整 floor(672*3/10)=201
	q, f, ok := e.Position("A", "S")
	if !ok || q != 1005+301 || f != 333+99 {
		t.Fatalf("pos = %d,%d,%v", q, f, ok)
	}
	// 可用现金得 m-mf=2016，另加碎股折现 383；冻结现金 999
	avail, frozen, ok := e.Cash("A")
	if !ok || avail != 100+2016+383 || frozen != 999 {
		t.Fatalf("cash = %d,%d,%v", avail, frozen, ok)
	}
	o1, _ := e.GetOrder("o1")
	o2, _ := e.GetOrder("o2")
	if o1.Price != 690 || o2.Price != 691 {
		t.Fatalf("orders = %d,%d", o1.Price, o2.Price)
	}
}

// 冻结部分与总量分别取整的差值：可用得 n-nf=202，而非 floor((q-f)*b/10)=201。
func TestFrozenRoundingDifference(t *testing.T) {
	e := New()
	must(t, e.Trade("A", "S", 1005))
	must(t, e.Freeze("A", "S", 333))
	must(t, e.SetClose("S", 1000))
	must(t, e.Announce("act", "S", 0, 3, 1, 2))
	must(t, e.Advance(2))
	q, f, _ := e.Position("A", "S")
	if q != 1306 || f != 432 {
		t.Fatalf("pos = %d,%d, want 1306,432", q, f)
	}
	if avail := q - f; avail != 672+202 {
		t.Fatalf("avail = %d, want %d", avail, 874)
	}
}

// 半分进位端到端：P=13、c=0、b=10，Pex=7；执行后收盘价记为 Pex。
func TestHalfCentEndToEnd(t *testing.T) {
	e := New()
	must(t, e.Trade("A", "S", 10))
	must(t, e.SetClose("S", 13))
	must(t, e.Announce("act", "S", 0, 10, 1, 2))
	must(t, e.Advance(2))
	res, err := e.Result("act")
	must(t, err)
	if res.Pex != 7 {
		t.Fatalf("pex = %d, want 7", res.Pex)
	}
	// 执行后收盘价为 7：再登记 (10*7)/20=3.5 -> 4
	must(t, e.Announce("act2", "S", 0, 10, 3, 4))
	must(t, e.Advance(4))
	res2, err := e.Result("act2")
	must(t, err)
	if res2.Pex != 4 {
		t.Fatalf("pex2 = %d, want 4", res2.Pex)
	}
}

// 新价小于 1 的买单被撤销并在清单中标明；卖单 ceil 至少为 1。
func TestBuyOrderCancelledBelowOne(t *testing.T) {
	e := New()
	must(t, e.Trade("A", "S", 10))
	must(t, e.SetClose("S", 10))
	must(t, e.Announce("act", "S", 99, 0, 1, 2)) // Pex=(100-99)/10=0.1 -> 1
	must(t, e.PlaceOrder("b1", "A", "S", Buy, 1, 5))
	must(t, e.PlaceOrder("b2", "A", "S", Buy, 9, 5))
	must(t, e.PlaceOrder("s1", "A", "S", Sell, 1, 5))
	must(t, e.Advance(2))
	res, err := e.Result("act")
	must(t, err)
	if res.Pex != 1 {
		t.Fatalf("pex = %d, want 1", res.Pex)
	}
	want := []corpact.OrderAdj{
		{Oid: "b1", OldPrice: 1, NewPrice: 0, Cancelled: true},
		{Oid: "b2", OldPrice: 9, NewPrice: 0, Cancelled: true},
		{Oid: "s1", OldPrice: 1, NewPrice: 1},
	}
	if !reflect.DeepEqual(res.Orders, want) {
		t.Fatalf("orders = %+v\nwant %+v", res.Orders, want)
	}
	if _, ok := e.GetOrder("b1"); ok {
		t.Fatal("b1 should be cancelled")
	}
	if _, ok := e.GetOrder("b2"); ok {
		t.Fatal("b2 should be cancelled")
	}
	o, ok := e.GetOrder("s1")
	if !ok || o.Price != 1 {
		t.Fatalf("s1 = %+v,%v", o, ok)
	}
}

// rec=5、ex=7：A 第 6 日卖光全部可用 672 股，仍按快照 1005 股获得；
// 第 6 日买入的 B 一无所得。
func TestSellAllAfterRecordDate(t *testing.T) {
	e := New()
	must(t, e.Trade("A", "S", 1005))
	must(t, e.Freeze("A", "S", 333))
	must(t, e.SetClose("S", 1000))
	must(t, e.Announce("act", "S", 30, 3, 5, 7))
	must(t, e.Advance(6)) // 快照在此刻拍摄（day 首次 > rec）
	must(t, e.Trade("A", "S", -672))
	must(t, e.Trade("B", "S", 100))
	must(t, e.Advance(7))

	res, err := e.Result("act")
	must(t, err)
	want := []corpact.AccountGain{{
		Acct:         "A",
		Shares:       301,
		FrozenShares: 99,
		Cash:         3015,
		FrozenCash:   999,
		FractionCash: 383,
	}}
	if !reflect.DeepEqual(res.Gains, want) {
		t.Fatalf("gains = %+v\nwant %+v", res.Gains, want)
	}
	q, f, _ := e.Position("A", "S")
	if q != 333+301 || f != 333+99 {
		t.Fatalf("A pos = %d,%d", q, f)
	}
	if qB, _, _ := e.Position("B", "S"); qB != 100 {
		t.Fatalf("B pos = %d, want 100", qB)
	}
}

// 当前日为 5 时直接 Advance(7)：快照取第 5 日末状态后立即执行。
func TestAdvanceCrossesBothDays(t *testing.T) {
	e := New()
	must(t, e.Trade("A", "S", 1005))
	must(t, e.Freeze("A", "S", 333))
	must(t, e.SetClose("S", 1000))
	must(t, e.Announce("act", "S", 30, 3, 5, 7))
	must(t, e.Advance(5))
	must(t, e.Trade("A", "S", -5)) // 第 5 日末 q=1000
	must(t, e.Advance(7))

	res, err := e.Result("act")
	must(t, err)
	want := []corpact.AccountGain{{
		Acct:         "A",
		Shares:       300, // floor(1000*3/10)
		FrozenShares: 99,
		Cash:         3000,
		FrozenCash:   999,
		FractionCash: 0, // 1000*3 mod 10 = 0
	}}
	if !reflect.DeepEqual(res.Gains, want) {
		t.Fatalf("gains = %+v\nwant %+v", res.Gains, want)
	}
}

// 快照后不可撤销；快照前撤销后同标的可再登记。
func TestCancelAfterSnapshot(t *testing.T) {
	e := New()
	must(t, e.Trade("A", "S", 10))
	must(t, e.SetClose("S", 100))
	must(t, e.Announce("act", "S", 1, 1, 1, 3))
	must(t, e.CancelAction("act")) // 快照前允许
	must(t, e.Announce("act2", "S", 1, 1, 1, 3))
	must(t, e.Advance(2)) // 拍快照，未执行
	if err := e.CancelAction("act2"); !errors.Is(err, ErrState) {
		t.Fatalf("want ErrState, got %v", err)
	}
	must(t, e.Advance(3))
	if _, err := e.Result("act2"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Result("act"); !errors.Is(err, ErrNotExist) {
		t.Fatalf("want ErrNotExist, got %v", err)
	}
}

// 冲突与无参考价的先后：状态不符 > 冲突 > 无参考价，且均在编号重复之后。
func TestConflictAndNoRefPriceOrder(t *testing.T) {
	e := New()
	must(t, e.SetClose("S", 100))
	must(t, e.Announce("a1", "S", 1, 1, 1, 2))
	if err := e.Announce("a2", "S", 1, 1, 1, 2); !errors.Is(err, ErrConflict) {
		t.Fatalf("want ErrConflict, got %v", err)
	}
	if err := e.Announce("a3", "T", 1, 1, 1, 2); !errors.Is(err, ErrNoRefPrice) {
		t.Fatalf("want ErrNoRefPrice, got %v", err)
	}
	must(t, e.Advance(1))
	// rec<=当前日 与冲突同时成立时，报状态不符
	if err := e.Announce("a4", "S", 1, 1, 1, 2); !errors.Is(err, ErrState) {
		t.Fatalf("want ErrState, got %v", err)
	}
	// 编号重复优先于状态不符
	if err := e.Announce("a1", "S", 1, 1, 1, 2); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("want ErrDuplicate, got %v", err)
	}
	// 参数非法优先于编号重复
	if err := e.Announce("a1", "S", 0, 0, 1, 2); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("want ErrInvalidParam, got %v", err)
	}
}

// 拒绝按次序只报第一个，且被拒绝的操作不改任何状态。
func TestRefusalOrder(t *testing.T) {
	e := New()
	must(t, e.Advance(5))
	cases := []struct {
		name string
		run  func() error
		want error
	}{
		{"Advance参数非法优先于回退", func() error { return e.Advance(-1) }, ErrInvalidParam},
		{"Advance超上限", func() error { return e.Advance(1000001) }, ErrInvalidParam},
		{"Advance回退", func() error { return e.Advance(4) }, ErrDateRollback},
		{"Deposit参数非法", func() error { return e.Deposit("A", 0) }, ErrInvalidParam},
		{"Trade参数非法优先于不存在", func() error { return e.Trade("ghost", "S", 0) }, ErrInvalidParam},
		{"Trade卖出账户不存在", func() error { return e.Trade("ghost", "S", -1) }, ErrNotExist},
		{"Freeze账户不存在", func() error { return e.Freeze("ghost", "S", 1) }, ErrNotExist},
		{"Unfreeze账户不存在", func() error { return e.Unfreeze("ghost", "S", 1) }, ErrNotExist},
		{"CancelOrder不存在", func() error { return e.CancelOrder("nope") }, ErrNotExist},
		{"CancelAction不存在", func() error { return e.CancelAction("nope") }, ErrNotExist},
		{"Result不存在", func() error { _, err := e.Result("nope"); return err }, ErrNotExist},
		{"Result空id参数非法", func() error { _, err := e.Result(""); return err }, ErrInvalidParam},
	}
	for _, tc := range cases {
		if err := tc.run(); !errors.Is(err, tc.want) {
			t.Errorf("%s: want %v, got %v", tc.name, tc.want, err)
		}
	}
	// 数量不足
	must(t, e.Trade("A", "S", 10))
	if err := e.Trade("A", "S", -11); !errors.Is(err, ErrInsufficient) {
		t.Fatalf("want ErrInsufficient, got %v", err)
	}
	if err := e.Freeze("A", "S", 11); !errors.Is(err, ErrInsufficient) {
		t.Fatalf("want ErrInsufficient, got %v", err)
	}
	if err := e.Unfreeze("A", "S", 1); !errors.Is(err, ErrInsufficient) {
		t.Fatalf("want ErrInsufficient, got %v", err)
	}
	// 被拒绝的操作不改任何状态
	q, f, _ := e.Position("A", "S")
	if q != 10 || f != 0 {
		t.Fatalf("pos = %d,%d", q, f)
	}
	if e.Day() != 5 {
		t.Fatalf("day = %d", e.Day())
	}
}

func TestPlaceOrderValidation(t *testing.T) {
	e := New()
	cases := []struct {
		name           string
		oid, acct, sym string
		side           Side
		price, qty     int64
		want           error
	}{
		{"空oid", "", "A", "S", Buy, 1, 1, ErrInvalidParam},
		{"空账户", "o", "", "S", Buy, 1, 1, ErrInvalidParam},
		{"空标的", "o", "A", "", Buy, 1, 1, ErrInvalidParam},
		{"方向非法", "o", "A", "S", Side(9), 1, 1, ErrInvalidParam},
		{"价格为0", "o", "A", "S", Buy, 0, 1, ErrInvalidParam},
		{"价格超限", "o", "A", "S", Buy, MaxPrice + 1, 1, ErrInvalidParam},
		{"数量为0", "o", "A", "S", Buy, 1, 0, ErrInvalidParam},
		{"数量超限", "o", "A", "S", Buy, 1, MaxQty + 1, ErrInvalidParam},
	}
	for _, tc := range cases {
		err := e.PlaceOrder(tc.oid, tc.acct, tc.sym, tc.side, tc.price, tc.qty)
		if !errors.Is(err, tc.want) {
			t.Errorf("%s: want %v, got %v", tc.name, tc.want, err)
		}
	}
	must(t, e.PlaceOrder("o", "A", "S", Buy, 1, 1))
	if err := e.PlaceOrder("o", "A", "S", Sell, 2, 2); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("want ErrDuplicate, got %v", err)
	}
	// 参数非法优先于编号重复
	if err := e.PlaceOrder("o", "A", "S", Buy, 0, 1); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("want ErrInvalidParam, got %v", err)
	}
	must(t, e.CancelOrder("o"))
	if err := e.CancelOrder("o"); !errors.Is(err, ErrNotExist) {
		t.Fatalf("want ErrNotExist, got %v", err)
	}
}

// 只调整该标的的在簿委托；未执行时 Result 报状态不符。
func TestResultStatesAndOtherSymbol(t *testing.T) {
	e := New()
	must(t, e.Trade("A", "S", 10))
	must(t, e.SetClose("S", 1000))
	must(t, e.SetClose("T", 1000))
	must(t, e.Announce("act", "S", 30, 3, 1, 2))
	if _, err := e.Result("act"); !errors.Is(err, ErrState) {
		t.Fatalf("want ErrState, got %v", err)
	}
	must(t, e.PlaceOrder("os", "A", "S", Buy, 900, 1))
	must(t, e.PlaceOrder("ot", "A", "T", Buy, 900, 1))
	must(t, e.Advance(2))
	o, ok := e.GetOrder("ot")
	if !ok || o.Price != 900 {
		t.Fatalf("other-symbol order changed: %+v", o)
	}
	if _, err := e.Result("act"); err != nil {
		t.Fatal(err)
	}
}
