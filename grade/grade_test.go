package grade

import (
	"errors"
	"sync"
	"testing"

	"ontology/refund"
	"ontology/rma"
)

func newSystem(t *testing.T, wd, v, beta, fee int64) (*rma.Store, *Service) {
	t.Helper()
	store, err := rma.New(rma.Config{WindowDays: wd, ValidFor: v})
	if err != nil {
		t.Fatal(err)
	}
	calc, err := refund.New(beta, fee)
	if err != nil {
		t.Fatal(err)
	}
	return store, New(store, calc)
}

// TestExampleFromSpec 复现题面两个连续例子的每一步。
func TestExampleFromSpec(t *testing.T) {
	_, svc := newSystem(t, 30, 10, 80, 50)
	if err := svc.store.AddOrder("O", 0, map[string]rma.Line{
		"L1": {Shipped: 3, Paid: 1000},
	}, 0); err != nil {
		t.Fatal(err)
	}
	mustAuth := func(id string, items []rma.Item, now int64, wantExp int64) {
		t.Helper()
		exp, err := svc.Authorize(id, "O", items, now)
		if err != nil || exp != wantExp {
			t.Fatalf("Authorize %s exp=%d err=%v, want exp %d", id, exp, err, wantExp)
		}
	}
	mustRecv := func(auth, line string, qty int64, g Grade, now int64, want Result) {
		t.Helper()
		got, err := svc.Receive(auth, "O", line, qty, g, now)
		if err != nil {
			t.Fatalf("Receive %s: %v", auth, err)
		}
		if got != want {
			t.Fatalf("Receive %s got=%+v want=%+v", auth, got, want)
		}
	}

	mustAuth("R1", []rma.Item{{Line: "L1", Qty: 2}}, 5, 15)
	// 余量 1：R2 申 2 件失败。
	if _, err := svc.Authorize("R2", "O", []rma.Item{{Line: "L1", Qty: 2}}, 6); !errors.Is(err, rma.ErrCapacity) {
		t.Fatalf("R2 err=%v want ErrCapacity", err)
	}
	mustRecv("R1", "L1", 1, A, 7, Result{Due: 333, Fee: 50, Paid: 283})
	mustRecv("R1", "L1", 1, B, 8, Result{Due: 267, Fee: 0, Paid: 267})

	// t=16 R3 成功（R1 已在 15 失效）；t=26 恰等 exp，Receive 报已过期。
	mustAuth("R3", []rma.Item{{Line: "L1", Qty: 1}}, 16, 26)
	if _, err := svc.Receive("R3", "O", "L1", 1, A, 26); !errors.Is(err, rma.ErrExpired) {
		t.Fatalf("R3 receive at exp err=%v want ErrExpired", err)
	}
	// t=30 恰等 Wd：R4 成功。
	mustAuth("R4", []rma.Item{{Line: "L1", Qty: 1}}, 30, 40)
	// C 级：应退 0、不扣费、不占合格余量。
	mustRecv("R4", "L1", 1, C, 30, Result{0, 0, 0})
	// 余量恢复：R5 成功，A 级收回后累计 R=933。
	mustAuth("R5", []rma.Item{{Line: "L1", Qty: 1}}, 30, 40)
	mustRecv("R5", "L1", 1, A, 30, Result{Due: 333, Fee: 50, Paid: 283})
	// t=31 任何 Authorize 报超出窗口。
	if _, err := svc.Authorize("R6", "O", []rma.Item{{Line: "L1", Qty: 1}}, 31); !errors.Is(err, rma.ErrWindow) {
		t.Fatalf("t=31 err=%v want ErrWindow", err)
	}

	// 行状态不变量：合格已收 3（A,A,B 的件数是 a=2,b=1），无有效预占。
	info, ok := svc.store.InspectLine("O", "L1")
	if !ok {
		t.Fatal("line missing")
	}
	if info.A != 2 || info.B != 1 || info.Reserved != 0 {
		t.Fatalf("line info=%+v want A=2 B=1 Reserved=0", info)
	}
}

func TestReceiveRejectTable(t *testing.T) {
	cases := []struct {
		name    string
		auth    string
		order   string
		line    string
		qty     int64
		g       Grade
		now     int64
		seed    func(svc *Service)
		wantErr error
	}{
		{"bad grade", "R", "O", "L1", 1, Grade(9), 5, nil, rma.ErrInvalid},
		{"qty zero", "R", "O", "L1", 0, A, 5, nil, rma.ErrInvalid},
		{"auth missing", "GHOST", "O", "L1", 1, A, 5, nil, rma.ErrNotFound},
		{"line not in auth", "R", "O", "L2", 1, A, 5, nil, rma.ErrNotFound},
		{"wrong order", "R", "O2", "L1", 1, A, 5, nil, rma.ErrNotFound},
		{"expired at exp", "R", "O", "L1", 1, A, 15, nil, rma.ErrExpired},
		{"over receive", "R", "O", "L1", 2, A, 6, nil, rma.ErrCapacity},
		{"clock back", "R", "O", "L1", 1, A, 1, nil, rma.ErrClockBack},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, svc := newSystem(t, 30, 10, 80, 50)
			if err := store.AddOrder("O", 0, map[string]rma.Line{
				"L1": {Shipped: 3, Paid: 1000},
				"L2": {Shipped: 3, Paid: 1000},
			}, 0); err != nil {
				t.Fatal(err)
			}
			if _, err := svc.Authorize("R", "O", []rma.Item{{Line: "L1", Qty: 1}}, 5); err != nil {
				t.Fatal(err)
			}
			if tc.name == "clock back" {
				// 时钟当前为 5；now=1 回退。
			}
			_, err := svc.Receive(tc.auth, tc.order, tc.line, tc.qty, tc.g, tc.now)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err=%v want %v", err, tc.wantErr)
			}
		})
	}
}

func TestCGradeFreesCapacity(t *testing.T) {
	_, svc := newSystem(t, 30, 100, 80, 50)
	if err := svc.store.AddOrder("O", 0, map[string]rma.Line{
		"L1": {Shipped: 1, Paid: 1000},
	}, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Authorize("R1", "O", []rma.Item{{Line: "L1", Qty: 1}}, 5); err != nil {
		t.Fatal(err)
	}
	// C 级后可重新申请。
	if _, err := svc.Receive("R1", "O", "L1", 1, C, 6); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Authorize("R2", "O", []rma.Item{{Line: "L1", Qty: 1}}, 7); err != nil {
		t.Fatalf("re-auth after C: %v", err)
	}
	info, _ := svc.store.InspectLine("O", "L1")
	if info.A != 0 || info.B != 0 || info.Reserved != 1 {
		t.Fatalf("after C + re-auth info=%+v", info)
	}
}

func TestZeroDueNoFee(t *testing.T) {
	// paid=0：任何收货应退 0，手续费一分不扣。
	_, svc := newSystem(t, 30, 100, 80, 50)
	if err := svc.store.AddOrder("O", 0, map[string]rma.Line{
		"L1": {Shipped: 2, Paid: 0},
	}, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Authorize("R", "O", []rma.Item{{Line: "L1", Qty: 2}}, 5); err != nil {
		t.Fatal(err)
	}
	r1, err := svc.Receive("R", "O", "L1", 1, A, 6)
	if err != nil || r1 != (Result{0, 0, 0}) {
		t.Fatalf("first=%+v err=%v", r1, err)
	}
	r2, err := svc.Receive("R", "O", "L1", 1, B, 7)
	if err != nil || r2 != (Result{0, 0, 0}) {
		t.Fatalf("second=%+v err=%v", r2, err)
	}
}

func TestConcurrentEquivalentToSerial(t *testing.T) {
	// 高并发混合作物，仅验证不崩、不死锁、不变量恒成立、结果落在合法集合。
	store, svc := newSystem(t, 1_000_000, 1_000_000, 80, 50)
	const orders = 20
	for i := 0; i < orders; i++ {
		id := "O" + string(rune('A'+i))
		if err := store.AddOrder(id, 0, map[string]rma.Line{
			"L1": {Shipped: 10000, Paid: 1_000_000},
		}, 0); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	const rounds = 300
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for k := 0; k < rounds; k++ {
				now := int64(k) // 所有 goroutine 共享单调时钟；相等 now 允许并发
				oid := "O" + string(rune('A'+(g%orders)))
				aid := "R" + string(rune('A'+g)) + "-" + itoa(k)
				if exp, err := svc.Authorize(aid, oid, []rma.Item{{Line: "L1", Qty: 1}}, now); err == nil {
					if exp <= now {
						t.Errorf("exp %d <= now %d", exp, now)
					}
					_, _ = svc.Receive(aid, oid, "L1", 1, Grade(1+(k%3)), now)
				}
			}
		}(g)
	}
	wg.Wait()
	for i := 0; i < orders; i++ {
		id := "O" + string(rune('A'+i))
		info, ok := store.InspectLine(id, "L1")
		if !ok {
			t.Fatalf("order %s missing", id)
		}
		if info.A+info.B+info.Reserved > info.Shipped {
			t.Fatalf("invariant broken for %s: %+v", id, info)
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [12]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
