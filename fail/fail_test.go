package fail

import (
	"testing"

	"ontology/instr"
)

func TestBlame(t *testing.T) {
	cases := []struct {
		name          string
		a, b, r       int64
		seller, buyer bool
	}{
		{"no-fault", 5, 5, 5, false, false},
		{"seller-only", 4, 5, 5, true, false},
		{"buyer-only", 5, 4, 5, false, true},
		{"both", 4, 3, 5, true, true},
		{"both-zero", 0, 0, 5, true, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, bb := Blame(c.a, c.b, c.r)
			if s != c.seller || bb != c.buyer {
				t.Fatalf("Blame(%d,%d,%d) = (%v,%v), want (%v,%v)",
					c.a, c.b, c.r, s, bb, c.seller, c.buyer)
			}
		})
	}
}

func TestPenalty(t *testing.T) {
	cases := []struct {
		name           string
		r, price, rate int64
		want           int64
	}{
		{"exact", 1000, 10, 10000, 10000},
		{"ceil-6.6", 600, 11, 10, 7},
		{"ceil-4.4", 400, 11, 10, 5},
		{"ceil-2.2", 400, 11, 5, 3},
		{"zero-rate", 400, 11, 0, 0},
		{"zero-remaining", 0, 11, 10, 0},
		{"one-up", 1, 1, 1, 1},
		{"big", 1_000_000_000, 1_000_000, 10000, 1_000_000_000_000_000},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Penalty(c.r, c.price, c.rate); got != c.want {
				t.Fatalf("Penalty(%d,%d,%d) = %d, want %d",
					c.r, c.price, c.rate, got, c.want)
			}
		})
	}
}

func TestCompensation(t *testing.T) {
	cases := []struct {
		name                   string
		r, price, amount, paid int64
		want                   int64
	}{
		{"positive", 600, 11, 10005, 4002, 597},
		{"zero-when-covered", 400, 10, 6100, 2033, 0},
		{"exact-zero", 100, 10, 1000, 0, 0},
		{"no-payment-yet", 100, 11, 1000, 0, 100},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Compensation(c.r, c.price, c.amount, c.paid); got != c.want {
				t.Fatalf("Compensation(%d,%d,%d,%d) = %d, want %d",
					c.r, c.price, c.amount, c.paid, got, c.want)
			}
		})
	}
}

// TestProcessOverdue 直接驱动逾期处理：卖方有责→买入并记赔付；
// 仅买方有责→取消无赔付；日龄不足→不动；罚金与赔付不动现金。
func TestProcessOverdue(t *testing.T) {
	b, err := instr.New(100, 10, 5, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.SetPrice(0, "X", 11); err != nil {
		t.Fatal(err)
	}
	// 卖方有责、日龄恰为 A=2：强制买入，赔付 597。
	i1 := &instr.Instruction{Seq: 0, ID: "i1", Seller: "S", Buyer: "B", Sym: "X",
		Qty: 1000, Amount: 10005, Sd: 2, Delivered: 400, Paid: 4002,
		Status: instr.Open, SellerFault: true}
	// 仅买方有责、日龄 2：取消，无赔付。
	i2 := &instr.Instruction{Seq: 1, ID: "i2", Seller: "S", Buyer: "B", Sym: "X",
		Qty: 600, Amount: 6100, Sd: 2, Delivered: 200, Paid: 2033,
		Status: instr.Open, BuyerFault: true}
	// 日龄 1 < A：不动。
	i3 := &instr.Instruction{Seq: 2, ID: "i3", Seller: "S", Buyer: "B", Sym: "X",
		Qty: 100, Amount: 1000, Sd: 3, Status: instr.Open, SellerFault: true}
	b.Lock()
	ProcessOverdue(b, []*instr.Instruction{i1, i2, i3}, 4)
	b.Unlock()
	if i1.Status != instr.BoughtIn {
		t.Fatalf("i1 status = %v, want bought-in", i1.Status)
	}
	if i2.Status != instr.Cancelled {
		t.Fatalf("i2 status = %v, want cancelled", i2.Status)
	}
	if i3.Status != instr.Open {
		t.Fatalf("i3 status = %v, want open", i3.Status)
	}
	snap := b.Snapshot()
	if got := snap.Accounts["S"].Payable; got != 597 {
		t.Fatalf("S payable = %d, want 597", got)
	}
	if got := snap.Accounts["B"].Receivable; got != 597 {
		t.Fatalf("B receivable = %d, want 597", got)
	}
	if got := snap.Accounts["S"].Cash; got != 0 {
		t.Fatalf("cash must be untouched, got %d", got)
	}
}
