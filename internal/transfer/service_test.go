package transfer_test

import (
	"testing"

	"ontology/internal/transfer"
)

func newSvc() *transfer.Service {
	return transfer.NewService(
		transfer.Config{TolerancePermille: 100, CloseWaitSeconds: 60},
		map[string]map[string]int64{
			"WH1": {"A": 100, "B": 10},
			"WH2": {"A": 0, "C": 5},
		},
	)
}

func mustOK(t *testing.T, err error, op string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s unexpected error: %v", op, err)
	}
}

func TestToleranceFloorAndExact(t *testing.T) {
	// issued=15, permille=100 → tolerance=floor(1.5)=1；收到 16 恰好等于容忍额。
	svc := transfer.NewService(
		transfer.Config{TolerancePermille: 100, CloseWaitSeconds: 60},
		map[string]map[string]int64{"WH1": {"A": 15}},
	)
	mustOK(t, svc.Create("T1", "WH1", "WH2", []transfer.Line{{Item: "A", Qty: 15}}, 1), "create")
	mustOK(t, svc.Ship("T1", 2), "ship")
	mustOK(t, svc.Receive("T1", "A", 16, 3), "receive-16")
	if err := svc.Receive("T1", "A", 1, 4); !transfer.IsCode(err, transfer.ErrOverReceipt) {
		t.Fatalf("expect over receipt, got %v", err)
	}
	lines, _ := svc.OrderLines("T1")
	if lines[0].Received != 16 {
		t.Fatalf("received=%d want 16", lines[0].Received)
	}
}

func TestCloseWaitBoundary(t *testing.T) {
	svc := newSvc()
	mustOK(t, svc.Create("T1", "WH1", "WH2", []transfer.Line{{Item: "A", Qty: 100}}, 0), "create")
	mustOK(t, svc.Ship("T1", 10), "ship")
	mustOK(t, svc.Receive("T1", "A", 50, 11), "receive")

	// ship=10, wait=60 → 69 差一秒，拒绝。
	if err := svc.Close("T1", 69); !transfer.IsCode(err, transfer.ErrCloseTooEarly) {
		t.Fatalf("expect close too early at 69, got %v", err)
	}
	// 恰好 70 满时长，允许。
	mustOK(t, svc.Close("T1", 70), "close-exact")
	lines, _ := svc.OrderLines("T1")
	if lines[0].Shortage != 50 {
		t.Fatalf("shortage=%d want 50", lines[0].Shortage)
	}
}

func TestCloseEarlyWhenFullyReceived(t *testing.T) {
	svc := newSvc()
	mustOK(t, svc.Create("T1", "WH1", "WH2", []transfer.Line{{Item: "A", Qty: 100}}, 0), "create")
	mustOK(t, svc.Ship("T1", 10), "ship")
	mustOK(t, svc.Receive("T1", "A", 100, 11), "receive")
	mustOK(t, svc.Close("T1", 12), "early-close")
}

func TestShortageAndOverageCoexist(t *testing.T) {
	// A 容忍 10，收 110（超收 10）；B 收 0（短缺 10）。
	svc := newSvc()
	mustOK(t, svc.Create("T1", "WH1", "WH2",
		[]transfer.Line{{Item: "A", Qty: 100}, {Item: "B", Qty: 10}}, 0), "create")
	mustOK(t, svc.Ship("T1", 0), "ship")
	mustOK(t, svc.Receive("T1", "A", 110, 1), "receive-A")
	mustOK(t, svc.Close("T1", 60), "close")
	lines, _ := svc.OrderLines("T1")
	byItem := map[string]transfer.LineState{}
	for _, ln := range lines {
		byItem[ln.Item] = ln
	}
	if byItem["A"].Overage != 10 || byItem["B"].Shortage != 10 {
		t.Fatalf("unexpected diff: A=%+v B=%+v", byItem["A"], byItem["B"])
	}
	if bad := svc.VerifyAll(); bad != "" {
		t.Fatalf("conservation broken at item %q", bad)
	}
}

func TestRecoverExactAndOneOver(t *testing.T) {
	svc := newSvc()
	mustOK(t, svc.Create("T1", "WH1", "WH2", []transfer.Line{{Item: "A", Qty: 100}}, 0), "create")
	mustOK(t, svc.Ship("T1", 0), "ship")
	mustOK(t, svc.Receive("T1", "A", 80, 1), "receive")
	mustOK(t, svc.Close("T1", 60), "close")

	// 短缺恰为 20：找回 20 恰好清空。
	mustOK(t, svc.Recover("T1", "A", 20, 61), "recover-exact")
	// 再找回 1：无短缺。
	if err := svc.Recover("T1", "A", 1, 62); !transfer.IsCode(err, transfer.ErrNoShortage) {
		t.Fatalf("expect no shortage, got %v", err)
	}
}

func TestRecoverExceed(t *testing.T) {
	svc := newSvc()
	mustOK(t, svc.Create("T2", "WH1", "WH2", []transfer.Line{{Item: "A", Qty: 5}}, 0), "create")
	mustOK(t, svc.Ship("T2", 0), "ship")
	mustOK(t, svc.Close("T2", 60), "close")
	if err := svc.Recover("T2", "A", 6, 61); !transfer.IsCode(err, transfer.ErrRecoveryExceed) {
		t.Fatalf("expect recovery exceed, got %v", err)
	}
	mustOK(t, svc.Recover("T2", "A", 5, 62), "recover-all")
	if err := svc.Recover("T2", "A", 1, 63); !transfer.IsCode(err, transfer.ErrNoShortage) {
		t.Fatalf("expect no shortage, got %v", err)
	}
}

func TestCancelReleasesFrozen(t *testing.T) {
	svc := newSvc()
	mustOK(t, svc.Create("T1", "WH1", "WH2", []transfer.Line{{Item: "A", Qty: 40}}, 0), "create")
	if a, f := svc.Stock("WH1", "A"); a != 60 || f != 40 {
		t.Fatalf("after create avail=%d frozen=%d", a, f)
	}
	mustOK(t, svc.Cancel("T1", 1), "cancel")
	if a, f := svc.Stock("WH1", "A"); a != 100 || f != 0 {
		t.Fatalf("after cancel avail=%d frozen=%d", a, f)
	}
	// 各状态下取消均须可区分地拒绝。
	mustOK(t, svc.Create("T2", "WH1", "WH2", []transfer.Line{{Item: "A", Qty: 5}}, 2), "create2")
	mustOK(t, svc.Ship("T2", 3), "ship2")
	err := svc.Cancel("T2", 4)
	if !transfer.IsCode(err, transfer.ErrInvalidState) {
		t.Fatalf("expect invalid state, got %v", err)
	}
	mustOK(t, svc.Close("T2", 63), "close2")
	err = svc.Cancel("T2", 64)
	if !transfer.IsCode(err, transfer.ErrInvalidState) {
		t.Fatalf("expect invalid state on closed, got %v", err)
	}
	err = svc.Cancel("T1", 65)
	if !transfer.IsCode(err, transfer.ErrInvalidState) {
		t.Fatalf("expect invalid state on cancelled, got %v", err)
	}
	if err := svc.Cancel("NOPE", 66); !transfer.IsCode(err, transfer.ErrTransferNotFound) {
		t.Fatalf("expect not found, got %v", err)
	}
}

func TestCreateAtomicInsufficient(t *testing.T) {
	svc := newSvc()
	// A 充足、B 只有 10 行下标 1 不足；整单拒绝且无残留。
	err := svc.Create("T1", "WH1", "WH2",
		[]transfer.Line{{Item: "A", Qty: 100}, {Item: "B", Qty: 11}}, 0)
	if !transfer.IsCode(err, transfer.ErrInsufficientStock) {
		t.Fatalf("expect insufficient, got %v", err)
	}
	if a, f := svc.Stock("WH1", "A"); a != 100 || f != 0 {
		t.Fatalf("A residual: avail=%d frozen=%d", a, f)
	}
	if a, f := svc.Stock("WH1", "B"); a != 10 || f != 0 {
		t.Fatalf("B residual: avail=%d frozen=%d", a, f)
	}
	// 最小不足行：第 0 行即不足时报第 0 行。
	err = svc.Create("T2", "WH1", "WH2",
		[]transfer.Line{{Item: "B", Qty: 11}, {Item: "A", Qty: 101}}, 0)
	if !transfer.IsCode(err, transfer.ErrInsufficientStock) {
		t.Fatalf("expect insufficient, got %v", err)
	}
}

func TestClockRollback(t *testing.T) {
	svc := newSvc()
	mustOK(t, svc.Create("T1", "WH1", "WH2", []transfer.Line{{Item: "A", Qty: 1}}, 10), "create")
	err := svc.Ship("T1", 9)
	if !transfer.IsCode(err, transfer.ErrClockRollback) {
		t.Fatalf("expect clock rollback, got %v", err)
	}
	// 被拒操作未改时钟：t=10 仍然有效。
	mustOK(t, svc.Ship("T1", 10), "ship-at-10")
}

func TestRejectPriority(t *testing.T) {
	svc := newSvc()
	// 参数非法优先于时钟回退。
	if err := svc.Receive("", "A", 0, -1); !transfer.IsCode(err, transfer.ErrInvalidArgument) {
		t.Fatalf("got %v", err)
	}
	// 时钟回退优先于单据不存在。
	mustOK(t, svc.Create("T1", "WH1", "WH2", []transfer.Line{{Item: "A", Qty: 1}}, 10), "create")
	if err := svc.Ship("NOPE", 9); !transfer.IsCode(err, transfer.ErrClockRollback) {
		t.Fatalf("expect rollback before not-found, got %v", err)
	}
	// 单据不存在优先于状态错误。
	if err := svc.Ship("NOPE", 11); !transfer.IsCode(err, transfer.ErrTransferNotFound) {
		t.Fatalf("got %v", err)
	}
	// 状态不符优先于业务拒绝（已关闭单收货既不是 shipped，也无需判超收）。
	mustOK(t, svc.Ship("T1", 11), "ship")
	mustOK(t, svc.Close("T1", 71), "close")
	if err := svc.Receive("T1", "A", 100000, 72); !transfer.IsCode(err, transfer.ErrInvalidState) {
		t.Fatalf("expect invalid state, got %v", err)
	}
}

func TestCloseBlocksReceive(t *testing.T) {
	svc := newSvc()
	mustOK(t, svc.Create("T1", "WH1", "WH2", []transfer.Line{{Item: "A", Qty: 100}}, 0), "create")
	mustOK(t, svc.Ship("T1", 0), "ship")
	mustOK(t, svc.Receive("T1", "A", 100, 1), "receive")
	mustOK(t, svc.Close("T1", 2), "close")
	if err := svc.Receive("T1", "A", 1, 3); !transfer.IsCode(err, transfer.ErrInvalidState) {
		t.Fatalf("expect invalid state, got %v", err)
	}
}

func TestValidation(t *testing.T) {
	svc := newSvc()
	cases := []struct {
		name         string
		id, src, dst string
		lines        []transfer.Line
		at           int64
	}{
		{"empty id", "", "WH1", "WH2", []transfer.Line{{Item: "A", Qty: 1}}, 0},
		{"same warehouse", "T", "WH1", "WH1", []transfer.Line{{Item: "A", Qty: 1}}, 0},
		{"empty lines", "T", "WH1", "WH2", nil, 0},
		{"zero qty", "T", "WH1", "WH2", []transfer.Line{{Item: "A", Qty: 0}}, 0},
		{"duplicate item", "T", "WH1", "WH2",
			[]transfer.Line{{Item: "A", Qty: 1}, {Item: "A", Qty: 1}}, 0},
	}
	for _, c := range cases {
		if err := svc.Create(c.id, c.src, c.dst, c.lines, c.at); !transfer.IsCode(err, transfer.ErrInvalidArgument) {
			t.Fatalf("%s: got %v", c.name, err)
		}
	}
}
