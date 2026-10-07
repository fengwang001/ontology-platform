package pharmacy_test

import (
	"fmt"
	"testing"

	. "ontology/pharmacy"
)

// buildHistory 构造含 history 张已取消历史处方（药品 H）的引擎，
// 目标药品 T 上有 bo 张各欠 boQty 的欠药处方。
func buildHistory(t *testing.T, history, bo, boQty int) *Engine {
	t.Helper()
	e, err := NewEngine(Config{R: 5})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	if err := e.RegisterDrug(0, "T", 1, true); err != nil {
		t.Fatalf("RegisterDrug T: %v", err)
	}
	if err := e.RegisterDrug(0, "H", 1, true); err != nil {
		t.Fatalf("RegisterDrug H: %v", err)
	}
	for i := 0; i < history; i++ {
		id := fmt.Sprintf("h%d", i)
		if err := e.AcceptPrescription(0, RxInput{ID: id, Patient: "p", IssueTime: 0, Lines: []LineInput{{DrugID: "H", Qty: 1}}}); err != nil {
			t.Fatalf("Accept h: %v", err)
		}
		if err := e.CancelPrescription(0, id); err != nil {
			t.Fatalf("Cancel h: %v", err)
		}
	}
	for i := 0; i < bo; i++ {
		id := fmt.Sprintf("b%d", i)
		if err := e.AcceptPrescription(0, RxInput{ID: id, Patient: "p", IssueTime: 0, Lines: []LineInput{{DrugID: "T", Qty: boQty}}}); err != nil {
			t.Fatalf("Accept b: %v", err)
		}
	}
	return e
}

// 到货再分配的开销只与当前欠药行数相关，与历史处方总数无关。
// 证明方式：两档历史规模下，内部扫描步数完全相同且等于理论值。
func TestInboundReallocCostIndependentOfHistory(t *testing.T) {
	var steps []uint64
	for _, scale := range []int{1_000, 100_000} {
		e := buildHistory(t, scale, 5, 10) // 5 张欠药处方，各欠 10
		e.ResetStats()
		if err := e.Inbound(1, "T", 25); err != nil { // 满足 2 行 + 第 3 行部分
			t.Fatalf("Inbound: %v", err)
		}
		steps = append(steps, e.Stats().BackorderScanSteps)
	}
	// 理论值：扫描 行1(得10) 行2(得10) 行3(得5) 行4(可用量0,停止) = 4 步。
	if steps[0] != 4 || steps[1] != 4 {
		t.Fatalf("扫描步数应为 4 且与历史规模无关: %v", steps)
	}
}

// 失效再分配的开销只与被释放预留数及欠药行数相关。
func TestExpiryReallocCostIndependentOfHistory(t *testing.T) {
	type result struct{ events, released, allocated, scanned uint64 }
	var results []result
	for _, scale := range []int{1_000, 100_000} {
		e := buildHistory(t, scale, 0, 0)
		if err := e.Inbound(0, "T", 10); err != nil {
			t.Fatalf("Inbound: %v", err)
		}
		// 1 张预留 10 的处方（R=5，失效时刻 6）+ 3 张各欠 4 的欠药处方。
		if err := e.AcceptPrescription(0, RxInput{ID: "hold", Patient: "p", IssueTime: 0, Lines: []LineInput{{DrugID: "T", Qty: 10}}}); err != nil {
			t.Fatalf("Accept hold: %v", err)
		}
		for i := 0; i < 3; i++ {
			id := fmt.Sprintf("w%d", i)
			if err := e.AcceptPrescription(0, RxInput{ID: id, Patient: "p", IssueTime: 0, Lines: []LineInput{{DrugID: "T", Qty: 4}}}); err != nil {
				t.Fatalf("Accept w: %v", err)
			}
		}
		e.ResetStats()
		if _, err := e.QueryDrug(6, "T"); err != nil { // 触发失效与连锁再分配
			t.Fatalf("QueryDrug: %v", err)
		}
		s := e.Stats()
		results = append(results, result{s.EventsProcessed, s.ReservationsReleased, s.ReservationsAllocated, s.BackorderScanSteps})
	}
	// 理论值：处理 1 个失效事件，释放 1 个预留，再分配 3 个新预留（4+4+2），
	// 扫描 4 步（行1、行2、行3、行3 再次发现可用量 0 停止）。
	want := result{1, 1, 3, 4}
	for i, got := range results {
		if got != want {
			t.Fatalf("规模档 %d: 开销应为 %+v（与历史无关），实际 %+v", i, want, got)
		}
	}
}

// 作废的欠药行被即时摘除：大规模作废后，到货再分配扫描步数为 0。
// 若采用懒删除，此处的扫描步数会随历史欠药行数线性增长。
func TestVoidedBackordersRemovedEagerly(t *testing.T) {
	const n = 20_000
	e := buildHistory(t, 0, n, 5) // n 张欠药处方
	for i := 0; i < n; i++ {
		if err := e.CancelPrescription(0, fmt.Sprintf("b%d", i)); err != nil {
			t.Fatalf("Cancel: %v", err)
		}
	}
	e.ResetStats()
	if err := e.Inbound(1, "T", 100); err != nil {
		t.Fatalf("Inbound: %v", err)
	}
	if s := e.Stats().BackorderScanSteps; s != 0 {
		t.Fatalf("作废欠药行未即时摘除：扫描步数 %d", s)
	}
	if d, _ := e.QueryDrug(1, "T"); d.BackorderTotal != 0 {
		t.Fatalf("作废后欠药总量应为 0: %+v", d)
	}
}
