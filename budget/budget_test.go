package budget

import "testing"

func TestChargeReleaseAndLimit(t *testing.T) {
	b := New(10)
	if !b.TryCharge(6) {
		t.Fatal("charge 6 should succeed")
	}
	if !b.TryCharge(4) {
		t.Fatal("charge up to the limit should succeed")
	}
	if b.Used() != 10 {
		t.Fatalf("used = %d, want 10", b.Used())
	}
	if b.TryCharge(1) {
		t.Fatal("charge beyond the limit must fail")
	}
	if b.Used() != 10 {
		t.Fatalf("failed charge changed used to %d", b.Used())
	}
	b.Release(10)
	if b.Used() != 0 {
		t.Fatalf("used after release = %d, want 0", b.Used())
	}
}

func TestRejectedChargeLeavesStateUntouched(t *testing.T) {
	b := New(5)
	if !b.TryCharge(5) {
		t.Fatal("charge 5 should succeed")
	}
	for i := 0; i < 3; i++ {
		if b.TryCharge(1) {
			t.Fatal("overflow charge must be rejected")
		}
	}
	if b.Used() != 5 || b.Limit() != 5 {
		t.Fatalf("state drifted: used=%d limit=%d", b.Used(), b.Limit())
	}
	if b.TryCharge(-1) {
		t.Fatal("negative charge must be rejected")
	}
}
