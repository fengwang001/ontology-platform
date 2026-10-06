package transfer

import (
	"errors"
	"testing"
)

func newTestSystem(t *testing.T) *System {
	t.Helper()
	s, err := NewSystem(Config{OverReceiptTolerancePermille: 100, CloseWaitSeconds: 10},
		map[string]map[string]int64{
			"W1": {"A": 100, "B": 50},
			"W2": {"A": 0},
		})
	if err != nil {
		t.Fatalf("NewSystem: %v", err)
	}
	return s
}

func mustVerify(t *testing.T, s *System) {
	t.Helper()
	ok, entries := s.VerifyConservation()
	if !ok {
		t.Fatalf("conservation violated: %+v", entries)
	}
}

func stockOf(t *testing.T, s *System, wh, product string) StockSnapshot {
	t.Helper()
	snap, ok := s.Stock(wh, product)
	if !ok {
		t.Fatalf("warehouse %s not found", wh)
	}
	return snap
}

// 超收容忍额取整：发出 7、千分比 100 → 容忍 floor(7*100/1000)=0，多收 1 即超收。
func TestToleranceRounding(t *testing.T) {
	s, err := NewSystem(Config{OverReceiptTolerancePermille: 100, CloseWaitSeconds: 0},
		map[string]map[string]int64{"W1": {"A": 100}, "W2": {}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CreateOrder(0, "T1", "W1", "W2", []Line{{Product: "A", Qty: 7}}); err != nil {
		t.Fatal(err)
	}
	if err := s.ShipOrder(1, "T1"); err != nil {
		t.Fatal(err)
	}
	if err := s.Receive(2, "T1", 0, 7); err != nil {
		t.Fatalf("receive exact shipped: %v", err)
	}
	var ore *OverReceiveError
	if err := s.Receive(3, "T1", 0, 1); !errors.As(err, &ore) {
		t.Fatalf("want OverReceiveError, got %v", err)
	}
	if ore.Limit != 7 {
		t.Fatalf("limit = %d, want 7 (tolerance floored to 0)", ore.Limit)
	}
	mustVerify(t, s)
}

// 恰等于容忍额可收，超一即拒：发出 10、千分比 100 → 容忍 1。
func TestToleranceExactBoundary(t *testing.T) {
	s, err := NewSystem(Config{OverReceiptTolerancePermille: 100, CloseWaitSeconds: 0},
		map[string]map[string]int64{"W1": {"A": 100}, "W2": {}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CreateOrder(0, "T1", "W1", "W2", []Line{{Product: "A", Qty: 10}}); err != nil {
		t.Fatal(err)
	}
	if err := s.ShipOrder(1, "T1"); err != nil {
		t.Fatal(err)
	}
	if err := s.Receive(2, "T1", 0, 11); err != nil {
		t.Fatalf("receive shipped+tolerance: %v", err)
	}
	if err := s.Receive(3, "T1", 0, 1); !errors.Is(err, ErrOverReceive) {
		t.Fatalf("want ErrOverReceive, got %v", err)
	}
	lines, _, _ := s.OrderLines("T1")
	if lines[0].Surplus != 1 || lines[0].Received != 11 {
		t.Fatalf("lines = %+v, want received 11 surplus 1", lines[0])
	}
	mustVerify(t, s)
}

// 等待时长：差一秒报未到关闭时刻，恰好满即可关闭。
func TestCloseWaitBoundary(t *testing.T) {
	s := newTestSystem(t)
	if err := s.CreateOrder(0, "T1", "W1", "W2", []Line{{Product: "A", Qty: 10}}); err != nil {
		t.Fatal(err)
	}
	if err := s.ShipOrder(5, "T1"); err != nil {
		t.Fatal(err)
	}
	if err := s.Receive(6, "T1", 0, 4); err != nil {
		t.Fatal(err)
	}
	// 发出于 5，等待 10 秒 → 15 可关，14 不可。
	var ncy *NotClosableYetError
	if err := s.CloseOrder(14, "T1"); !errors.As(err, &ncy) {
		t.Fatalf("want NotClosableYetError, got %v", err)
	}
	if ncy.Earliest != 15 {
		t.Fatalf("earliest = %d, want 15", ncy.Earliest)
	}
	if err := s.CloseOrder(15, "T1"); err != nil {
		t.Fatalf("close at exact wait boundary: %v", err)
	}
	lines, status, _ := s.OrderLines("T1")
	if status != StatusClosed || lines[0].Shortage != 6 {
		t.Fatalf("status = %v lines = %+v, want closed shortage 6", status, lines[0])
	}
	mustVerify(t, s)
}

// 全部收齐（含超收）可提前关闭，无需等待。
func TestCloseEarlyWhenFullyReceived(t *testing.T) {
	s := newTestSystem(t)
	if err := s.CreateOrder(0, "T1", "W1", "W2", []Line{{Product: "A", Qty: 10}}); err != nil {
		t.Fatal(err)
	}
	if err := s.ShipOrder(5, "T1"); err != nil {
		t.Fatal(err)
	}
	if err := s.Receive(6, "T1", 0, 11); err != nil {
		t.Fatal(err)
	}
	if err := s.CloseOrder(7, "T1"); err != nil {
		t.Fatalf("early close when fully received: %v", err)
	}
	lines, _, _ := s.OrderLines("T1")
	if lines[0].Shortage != 0 || lines[0].Surplus != 1 {
		t.Fatalf("lines = %+v, want shortage 0 surplus 1", lines[0])
	}
	mustVerify(t, s)
}

// 短缺与超收同单并存。
func TestShortageAndSurplusCoexist(t *testing.T) {
	s := newTestSystem(t)
	if err := s.CreateOrder(0, "T1", "W1", "W2", []Line{
		{Product: "A", Qty: 10},
		{Product: "B", Qty: 10},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.ShipOrder(1, "T1"); err != nil {
		t.Fatal(err)
	}
	if err := s.Receive(2, "T1", 0, 6); err != nil { // A 短缺 4
		t.Fatal(err)
	}
	if err := s.Receive(3, "T1", 1, 11); err != nil { // B 超收 1
		t.Fatal(err)
	}
	if err := s.CloseOrder(11, "T1"); err != nil { // 1+10=11 恰好满等待
		t.Fatal(err)
	}
	lines, _, _ := s.OrderLines("T1")
	if lines[0].Shortage != 4 || lines[0].Surplus != 0 {
		t.Fatalf("line A = %+v, want shortage 4 surplus 0", lines[0])
	}
	if lines[1].Shortage != 0 || lines[1].Surplus != 1 {
		t.Fatalf("line B = %+v, want shortage 0 surplus 1", lines[1])
	}
	mustVerify(t, s)
}

// 找回：恰等于短缺可，超一报找回过量；无短缺行报无短缺。
func TestRecover(t *testing.T) {
	s := newTestSystem(t)
	if err := s.CreateOrder(0, "T1", "W1", "W2", []Line{
		{Product: "A", Qty: 10},
		{Product: "B", Qty: 10},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.ShipOrder(1, "T1"); err != nil {
		t.Fatal(err)
	}
	if err := s.Receive(2, "T1", 0, 6); err != nil {
		t.Fatal(err)
	}
	if err := s.Receive(3, "T1", 1, 10); err != nil {
		t.Fatal(err)
	}
	if err := s.CloseOrder(11, "T1"); err != nil {
		t.Fatal(err)
	}
	// 未关闭单不可找回已由上面保证；此处 B 无短缺。
	if err := s.Recover(12, "T1", 1, 1); !errors.Is(err, ErrNoShortage) {
		t.Fatalf("want ErrNoShortage, got %v", err)
	}
	// 超一报找回过量。
	var ree *RecoverExcessError
	if err := s.Recover(12, "T1", 0, 5); !errors.As(err, &ree) {
		t.Fatalf("want RecoverExcessError, got %v", err)
	}
	if ree.Max != 4 {
		t.Fatalf("max = %d, want 4", ree.Max)
	}
	// 恰等于短缺。
	before := stockOf(t, s, "W2", "A").Available
	if err := s.Recover(13, "T1", 0, 4); err != nil {
		t.Fatalf("recover exact shortage: %v", err)
	}
	lines, _, _ := s.OrderLines("T1")
	if lines[0].Shortage != 0 {
		t.Fatalf("shortage = %d, want 0", lines[0].Shortage)
	}
	if got := stockOf(t, s, "W2", "A").Available; got != before+4 {
		t.Fatalf("dst available = %d, want %d", got, before+4)
	}
	if err := s.Recover(14, "T1", 0, 1); !errors.Is(err, ErrNoShortage) {
		t.Fatalf("want ErrNoShortage after full recovery, got %v", err)
	}
	mustVerify(t, s)
}

// 取消未发出的单释放冻结；已发出/已关闭/已取消以可区分错误拒绝。
func TestCancelReleasesFrozenAndStateErrors(t *testing.T) {
	s := newTestSystem(t)
	if err := s.CreateOrder(0, "T1", "W1", "W2", []Line{{Product: "A", Qty: 30}}); err != nil {
		t.Fatal(err)
	}
	if got := stockOf(t, s, "W1", "A"); got.Available != 70 || got.Frozen != 30 {
		t.Fatalf("after create: %+v, want available 70 frozen 30", got)
	}
	if err := s.CancelOrder(1, "T1"); err != nil {
		t.Fatal(err)
	}
	if got := stockOf(t, s, "W1", "A"); got.Available != 100 || got.Frozen != 0 {
		t.Fatalf("after cancel: %+v, want available 100 frozen 0", got)
	}
	if err := s.CancelOrder(2, "T1"); !errors.Is(err, ErrOrderCancelled) {
		t.Fatalf("cancel cancelled: want ErrOrderCancelled, got %v", err)
	}

	// 已发出的单取消错误可与已取消区分。
	if err := s.CreateOrder(3, "T2", "W1", "W2", []Line{{Product: "A", Qty: 10}}); err != nil {
		t.Fatal(err)
	}
	if err := s.ShipOrder(4, "T2"); err != nil {
		t.Fatal(err)
	}
	if err := s.CancelOrder(5, "T2"); !errors.Is(err, ErrOrderAlreadyShipped) {
		t.Fatalf("cancel shipped: want ErrOrderAlreadyShipped, got %v", err)
	}
	if err := s.Receive(6, "T2", 0, 10); err != nil {
		t.Fatal(err)
	}
	if err := s.CloseOrder(7, "T2"); err != nil {
		t.Fatal(err)
	}
	if err := s.CancelOrder(8, "T2"); !errors.Is(err, ErrOrderClosed) {
		t.Fatalf("cancel closed: want ErrOrderClosed, got %v", err)
	}
	mustVerify(t, s)
}

// 批量创建时任一行不足则整单拒绝，报下标最小的不足行，且无残留冻结。
func TestCreateAllOrNothing(t *testing.T) {
	s := newTestSystem(t)
	err := s.CreateOrder(0, "T1", "W1", "W2", []Line{
		{Product: "A", Qty: 60}, // 够
		{Product: "B", Qty: 60}, // 不足（50）
		{Product: "A", Qty: 1},  // 重复商品，参数非法优先
	})
	if !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("duplicate product: want ErrInvalidParam, got %v", err)
	}

	err = s.CreateOrder(0, "T1", "W1", "W2", []Line{
		{Product: "A", Qty: 60},
		{Product: "B", Qty: 60},
	})
	var ise *InsufficientStockError
	if !errors.As(err, &ise) {
		t.Fatalf("want InsufficientStockError, got %v", err)
	}
	if ise.LineIndex != 1 {
		t.Fatalf("line index = %d, want 1", ise.LineIndex)
	}
	// 无残留：可用与冻结均未变化，单不存在。
	if got := stockOf(t, s, "W1", "A"); got.Available != 100 || got.Frozen != 0 {
		t.Fatalf("residue on A: %+v", got)
	}
	if got := stockOf(t, s, "W1", "B"); got.Available != 50 || got.Frozen != 0 {
		t.Fatalf("residue on B: %+v", got)
	}
	if _, _, err := s.OrderLines("T1"); !errors.Is(err, ErrOrderNotFound) {
		t.Fatalf("order should not exist, got %v", err)
	}

	// 多行同时不足时报下标最小者。
	err = s.CreateOrder(0, "T2", "W1", "W2", []Line{
		{Product: "A", Qty: 200},
		{Product: "B", Qty: 200},
	})
	if !errors.As(err, &ise) || ise.LineIndex != 0 {
		t.Fatalf("want InsufficientStockError at line 0, got %v", err)
	}
	mustVerify(t, s)
}

// 时钟回退拒绝且不改变状态与时钟；被拒绝操作不推进时钟。
func TestClockRegression(t *testing.T) {
	s := newTestSystem(t)
	if err := s.CreateOrder(10, "T1", "W1", "W2", []Line{{Product: "A", Qty: 10}}); err != nil {
		t.Fatal(err)
	}
	if err := s.ShipOrder(9, "T1"); !errors.Is(err, ErrClockRegression) {
		t.Fatalf("want ErrClockRegression, got %v", err)
	}
	// 被拒绝后时钟仍是 10：等于 10 的操作可接受。
	if err := s.ShipOrder(10, "T1"); err != nil {
		t.Fatalf("op at last accepted time should succeed: %v", err)
	}
	// 参数非法优先于时钟回退。
	if err := s.Receive(0, "T1", 0, -5); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("want ErrInvalidParam before clock check, got %v", err)
	}
	// 时钟回退优先于单不存在。
	if err := s.ShipOrder(5, "NOPE"); !errors.Is(err, ErrClockRegression) {
		t.Fatalf("want ErrClockRegression before not-found, got %v", err)
	}
	// 单不存在优先于状态/业务。
	if err := s.Receive(10, "NOPE", 0, 1); !errors.Is(err, ErrOrderNotFound) {
		t.Fatalf("want ErrOrderNotFound, got %v", err)
	}
	mustVerify(t, s)
}

// 发出只允许一次；关闭后不可再收货；未发出的单不可收货/关闭。
func TestStateMachineGuards(t *testing.T) {
	s := newTestSystem(t)
	if err := s.CreateOrder(0, "T1", "W1", "W2", []Line{{Product: "A", Qty: 10}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Receive(1, "T1", 0, 1); !errors.Is(err, ErrOrderNotShipped) {
		t.Fatalf("receive before ship: want ErrOrderNotShipped, got %v", err)
	}
	if err := s.CloseOrder(1, "T1"); !errors.Is(err, ErrOrderNotShipped) {
		t.Fatalf("close before ship: want ErrOrderNotShipped, got %v", err)
	}
	if err := s.Recover(1, "T1", 0, 1); !errors.Is(err, ErrOrderNotClosed) {
		t.Fatalf("recover before close: want ErrOrderNotClosed, got %v", err)
	}
	if err := s.ShipOrder(2, "T1"); err != nil {
		t.Fatal(err)
	}
	if err := s.ShipOrder(3, "T1"); !errors.Is(err, ErrOrderAlreadyShipped) {
		t.Fatalf("double ship: want ErrOrderAlreadyShipped, got %v", err)
	}
	if err := s.Receive(4, "T1", 0, 10); err != nil {
		t.Fatal(err)
	}
	if err := s.CloseOrder(5, "T1"); err != nil {
		t.Fatal(err)
	}
	if err := s.Receive(6, "T1", 0, 1); !errors.Is(err, ErrOrderClosed) {
		t.Fatalf("receive after close: want ErrOrderClosed, got %v", err)
	}
	mustVerify(t, s)
}

// 发出后源仓冻结扣除、在途出现；收货后在途减少、目的仓可用增加。
func TestShipAndReceiveStockFlow(t *testing.T) {
	s := newTestSystem(t)
	if err := s.CreateOrder(0, "T1", "W1", "W2", []Line{{Product: "A", Qty: 40}}); err != nil {
		t.Fatal(err)
	}
	if err := s.ShipOrder(1, "T1"); err != nil {
		t.Fatal(err)
	}
	if got := stockOf(t, s, "W1", "A"); got.Available != 60 || got.Frozen != 0 {
		t.Fatalf("after ship: %+v, want available 60 frozen 0", got)
	}
	if err := s.Receive(2, "T1", 0, 15); err != nil {
		t.Fatal(err)
	}
	if err := s.Receive(3, "T1", 0, 15); err != nil {
		t.Fatal(err)
	}
	if got := stockOf(t, s, "W2", "A"); got.Available != 30 {
		t.Fatalf("dst available = %d, want 30", got.Available)
	}
	mustVerify(t, s)
}

// 源仓与目的仓相同、数量为负、行下标越界等参数非法。
func TestInvalidParams(t *testing.T) {
	s := newTestSystem(t)
	if err := s.CreateOrder(0, "T1", "W1", "W1", []Line{{Product: "A", Qty: 1}}); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("same src/dst: want ErrInvalidParam, got %v", err)
	}
	if err := s.CreateOrder(0, "T1", "W1", "W2", nil); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("empty lines: want ErrInvalidParam, got %v", err)
	}
	if err := s.CreateOrder(0, "T1", "W1", "W2", []Line{{Product: "A", Qty: 0}}); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("zero qty: want ErrInvalidParam, got %v", err)
	}
	if err := s.CreateOrder(0, "T1", "W1", "W3", []Line{{Product: "A", Qty: 1}}); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("unknown warehouse: want ErrInvalidParam, got %v", err)
	}
	if err := s.CreateOrder(0, "T1", "W1", "W2", []Line{{Product: "A", Qty: 1}}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateOrder(0, "T1", "W1", "W2", []Line{{Product: "A", Qty: 1}}); !errors.Is(err, ErrDuplicateOrder) {
		t.Fatalf("duplicate id: want ErrDuplicateOrder, got %v", err)
	}
	if err := s.ShipOrder(1, "T1"); err != nil {
		t.Fatal(err)
	}
	if err := s.Receive(2, "T1", 5, 1); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("line index out of range: want ErrInvalidParam, got %v", err)
	}
	mustVerify(t, s)
}
