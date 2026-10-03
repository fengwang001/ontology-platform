package quota

import "testing"

func mustAdd(t *testing.T, r *Registry, name string, rate, burst int64, prio int) *Tenant {
	t.Helper()
	tn, err := r.Add(name, rate, burst, prio)
	if err != nil {
		t.Fatalf("Add(%s): %v", name, err)
	}
	return tn
}

// rate*delta 可达 1e21，必须先封顶，余额不得溢出也不得低于 0。
func TestRefillOverflowSafe(t *testing.T) {
	r := NewRegistry()
	tn := mustAdd(t, r, "big", MaxRate, MaxBurst, 0)
	tn.Spend(0, tn.cap()) // 清空
	if got := tn.Balance(1_000_000_000_000); got != tn.cap() {
		t.Fatalf("balance=%d want capped %d", got, tn.cap())
	}
	tn.Refund(1_000_000_000_000, 0) // 落盘
	if tn.LastRefill() != 1_000_000_000_000 || tn.Tokens() != tn.cap() {
		t.Fatalf("state=(%d,%d)", tn.Tokens(), tn.LastRefill())
	}
}

func TestLazySettleAndExactBoundary(t *testing.T) {
	r := NewRegistry()
	tn := mustAdd(t, r, "t", 10, 100, 0)
	tn.Spend(0, 60000) // 40000
	// 虚拟余额恰等：通过；差 1：不足。
	if !tn.CanSpend(1000, 50000) { // 40000+10*1000=50000
		t.Fatal("exact boundary should pass")
	}
	if tn.CanSpend(1000, 50001) {
		t.Fatal("off-by-one should fail")
	}
	if tn.Tokens() != 40000 || tn.LastRefill() != 0 {
		t.Fatal("virtual check must not mutate state")
	}
	tn.Spend(1000, 50000)
	if tn.Tokens() != 0 || tn.LastRefill() != 1000 {
		t.Fatalf("state=(%d,%d)", tn.Tokens(), tn.LastRefill())
	}
}

func TestRefundCapsAndSettles(t *testing.T) {
	r := NewRegistry()
	tn := mustAdd(t, r, "t", 10, 100, 0)
	tn.Spend(0, 60000) // 40000, lastRefill=0
	tn.Refund(1000, 60000)
	// 40000 + 10000 补充 + 60000 退款 = 110000 -> 封顶 100000
	if tn.Tokens() != 100000 || tn.LastRefill() != 1000 {
		t.Fatalf("state=(%d,%d)", tn.Tokens(), tn.LastRefill())
	}
}

func TestAddValidationOrder(t *testing.T) {
	r := NewRegistry()
	if _, err := r.Add("", 1, 1, 0); err != ErrInvalidParam {
		t.Fatalf("err=%v", err)
	}
	mustAdd(t, r, "dup", 1, 1, 0)
	// 重复优先于数量上限：填满后重复名仍报重复？不——先查重再查上限。
	for i := 1; i < MaxTenants; i++ {
		mustAdd(t, r, string(rune(i+256)), 1, 1, 0)
	}
	if _, err := r.Add("dup", 1, 1, 0); err != ErrDuplicateName {
		t.Fatalf("err=%v want ErrDuplicateName", err)
	}
	if _, err := r.Add("new", 1, 1, 0); err != ErrTooManyTenants {
		t.Fatalf("err=%v want ErrTooManyTenants", err)
	}
}
