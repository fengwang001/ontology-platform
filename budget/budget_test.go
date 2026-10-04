package budget

import (
	"errors"
	"testing"
)

func TestNewValidation(t *testing.T) {
	if _, err := NewDataset("", 10); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("empty dataset name: %v", err)
	}
	if _, err := NewDataset("d", 0); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("zero budget: %v", err)
	}
	if _, err := NewDataset("d", MaxBudget+1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("budget over max: %v", err)
	}
	if _, err := NewAnalyst("a", -1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("negative budget: %v", err)
	}
}

func TestDatasetLedger(t *testing.T) {
	d, err := NewDataset("D", 1000)
	if err != nil {
		t.Fatal(err)
	}
	if d.Remaining() != 1000 {
		t.Fatalf("initial remaining = %d", d.Remaining())
	}
	if !d.CanReserve(1000) {
		t.Fatal("exact-full reserve must be allowed")
	}
	if d.CanReserve(1001) {
		t.Fatal("over-full reserve must be rejected")
	}
	d.Reserve(254)
	if d.Remaining() != 746 {
		t.Fatalf("remaining after reserve = %d", d.Remaining())
	}
	d.Settle(254, 200)
	if d.Used != 200 || d.Resv != 0 || d.Remaining() != 800 {
		t.Fatalf("after settle: used=%d resv=%d rem=%d", d.Used, d.Resv, d.Remaining())
	}
	d.Reserve(100)
	d.Settle(100, 100)
	if d.Used != 300 {
		t.Fatalf("used after full cancel-running style settle = %d", d.Used)
	}
}

func TestAnalystWindows(t *testing.T) {
	a, err := NewAnalyst("a", 300)
	if err != nil {
		t.Fatal(err)
	}
	if a.Window(0) != 0 || a.WindowRemaining(0) != 300 {
		t.Fatal("fresh window must be empty")
	}
	if !a.CanWindow(0, 300) || a.CanWindow(0, 301) {
		t.Fatal("exact-full only")
	}
	a.AddWindow(0, 254)
	a.AddWindow(0, 46)
	if a.Window(0) != 300 || a.WindowRemaining(0) != 0 {
		t.Fatalf("window 0 = %d", a.Window(0))
	}
	if a.Window(1) != 0 {
		t.Fatal("new window must start empty")
	}
	// 结算差额只退回查询所属窗口
	a.SetWindow(0, 254, 200)
	if a.Window(0) != 246 {
		t.Fatalf("window 0 after settle = %d", a.Window(0))
	}
	a.SetWindow(0, 46, 0)
	if a.Window(0) != 200 {
		t.Fatalf("window 0 after cancel = %d", a.Window(0))
	}
}
