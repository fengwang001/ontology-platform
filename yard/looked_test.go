package yard

import (
	"fmt"
	"testing"

	"ontology/appt"
)

// TestLookedBound 证明一次派位考察记录数与等待总车数无关：
// 等待 100 辆与 10000 辆两档，单次只释放一个月台，looked 应相同且 ≤ 6×(派出+1)。
func TestLookedBound(t *testing.T) {
	measure := func(n int) int {
		y := New(apptCfg())
		// Q 占住唯一月台 R1；其余 n 辆全部排队等待。
		if _, err := y.AddDock([]byte("R1"), Reefer, 0); err != nil {
			t.Fatal(err)
		}
		if _, err := y.CheckIn([]byte("Q"), Reefer, 0); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < n; i++ {
			id := fmt.Sprintf("W%05d", i)
			if _, err := y.CheckIn([]byte(id), Reefer, 1); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := y.Depart([]byte("Q"), 2); err != nil {
			t.Fatal(err)
		}
		y.mu.Lock()
		lk := y.looked
		y.mu.Unlock()
		return lk
	}
	lk100 := measure(100)
	lk10000 := measure(10000)
	t.Logf("looked: waiting=100 -> %d, waiting=10000 -> %d (bound 6×(1+1)=12)", lk100, lk10000)
	if lk100 > 12 || lk10000 > 12 {
		t.Fatalf("looked exceeds 6×(dispatched+1)=12: %d %d", lk100, lk10000)
	}
	if lk100 != lk10000 {
		t.Fatalf("looked must be independent of waiting population: %d vs %d", lk100, lk10000)
	}
}

// TestLookedBoundMultiDispatch 一次派出多辆时，looked ≤ 6×(派出数+1)。
func TestLookedBoundMultiDispatch(t *testing.T) {
	const n = 3000
	y := New(apptCfg())
	// 5 个冷藏位全被 Q0..Q4 占用，另有 n 辆冷藏车排队；一次 AddDock 第 6 个月台只放一辆，
	// 故改为连续 Depart 5 次集中制造多辆派出——这里在最后一次操作前先释放全部 5 个位：
	// 用一次「先加 5 个被占位月台、再一次性加入 5 个新月台」的方式让单次 dispatch 派出 5 辆。
	for i := 0; i < 5; i++ {
		id := fmt.Sprintf("O%02d", i)
		if _, err := y.AddDock([]byte(id), Reefer, 0); err != nil {
			t.Fatal(err)
		}
		if _, err := y.CheckIn([]byte("P"+id), Reefer, 0); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("W%05d", i)
		if _, err := y.CheckIn([]byte(id), Reefer, 1); err != nil {
			t.Fatal(err)
		}
	}
	// 一次性释放 5 个位需要 5 次 Depart；逐次都会派一辆。
	totalDispatched := 0
	for i := 0; i < 5; i++ {
		id := fmt.Sprintf("PO%02d", i)
		got, err := y.Depart([]byte(id), 2)
		if err != nil {
			t.Fatal(err)
		}
		y.mu.Lock()
		lk := y.looked
		y.mu.Unlock()
		if lk > 6*(len(got)+1) {
			t.Fatalf("looked %d exceeds 6×(%d+1)", lk, len(got))
		}
		totalDispatched += len(got)
	}
	if totalDispatched != 5 {
		t.Fatalf("want 5 dispatched, got %d", totalDispatched)
	}
}

func apptCfg() appt.Config { return appt.Config{S: 20, E: 30, L: 15, Wmax: 60, K: 2} }
