package repo

import "testing"

// 利息侵蚀致违约：处置跨两种券，最后一种多卖余款退现金，坏账为 0。
func TestDefaultDisposalRefundAcrossBonds(t *testing.T) {
	s := New()
	mustOK(t, s.AddBond(0, []byte("A"), 0, 10), "bond A cheap rate0")
	mustOK(t, s.AddBond(0, []byte("B"), 100, 100), "bond B")
	mustOK(t, s.Credit(0, []byte("u"), []byte("A"), 100), "credit A")
	mustOK(t, s.Credit(0, []byte("u"), []byte("B"), 100), "credit B")
	mustOK(t, s.PledgeIn(0, []byte("u"), []byte("A"), 100), "in A")
	mustOK(t, s.PledgeIn(0, []byte("u"), []byte("B"), 100), "in B Cap=100")
	// r1=9900(occ99) day5 r=10000；r2=100(occ1) day20。到手现金 10000。
	mustOK(t, s.Repo(0, []byte("r1"), []byte("u"), 9900, 5, 10000), "r1")
	mustOK(t, s.Repo(0, []byte("r2"), []byte("u"), 100, 20, 0), "r2")
	mustOK(t, s.CreditCash(0, []byte("w"), 1), "w")
	// r1 应还 9900+ceil(9900*10000*5/3650000)=9900+136=10036 > 现金 10000 -> 违约
	mustOK(t, s.CreditCash(5, []byte("w"), 1), "day5")
	r1 := s.GetRepo([]byte("r1"))
	if r1.Status != StatusDefault || r1.BadDebt != 0 {
		t.Fatalf("r1 status=%d bad=%d", r1.Status, r1.BadDebt)
	}
	// 处置：A 100 张价 10 全卖得 1000，欠 9036；B ceil(9036/100)=91 张得 9100，退 64。
	a := s.Acct([]byte("u"))
	if a.Pledged["A"] != 0 {
		t.Fatalf("A left=%d want 0", a.Pledged["A"])
	}
	if a.Pledged["B"] != 9 {
		t.Fatalf("B left=%d want 9", a.Pledged["B"])
	}
	if a.Cash != 10000+64 {
		t.Fatalf("cash=%d want 10064", a.Cash)
	}
	// 处置后 Cap=9，Use=1（r2 未到期），不欠库；r2 day20 正常购回。
	if a.Cap != 9 || a.Use != 1 {
		t.Fatalf("cap=%d use=%d", a.Cap, a.Use)
	}
	// day20：r2 应还 100，现金充足购回
	mustOK(t, s.CreditCash(20, []byte("w"), 1), "day20")
	if s.GetRepo([]byte("r2")).Status != StatusRepaid {
		t.Fatal("r2 should repay")
	}
}

func TestDefaultThenRateCutDeficit(t *testing.T) {
	s := New()
	mustOK(t, s.AddBond(0, []byte("A"), 0, 10), "bond A")
	mustOK(t, s.AddBond(0, []byte("B"), 100, 100), "bond B")
	mustOK(t, s.Credit(0, []byte("u"), []byte("A"), 100), "credit A")
	mustOK(t, s.Credit(0, []byte("u"), []byte("B"), 100), "credit B")
	mustOK(t, s.PledgeIn(0, []byte("u"), []byte("A"), 100), "in A")
	mustOK(t, s.PledgeIn(0, []byte("u"), []byte("B"), 100), "in B")
	mustOK(t, s.Repo(0, []byte("r1"), []byte("u"), 9900, 5, 10000), "r1 occ99")
	mustOK(t, s.Repo(0, []byte("r2"), []byte("u"), 100, 20, 0), "r2 occ1")
	mustOK(t, s.CreditCash(0, []byte("w"), 1), "w")
	mustOK(t, s.CreditCash(5, []byte("w"), 1), "day5 default")
	if s.GetRepo([]byte("r1")).Status != StatusDefault {
		t.Fatal("r1 default")
	}
	a := s.Acct([]byte("u"))
	if a.Cap != 9 || a.Use != 1 {
		t.Fatalf("cap=%d use=%d", a.Cap, a.Use)
	}
	// 处置后 B 余 9 张。下调 B 折算率到 0：Cap=0 < Use=1，欠库
	mustOK(t, s.SetRate(6, []byte("B"), 0), "cut B to 0")
	ds := s.Deficits()
	if len(ds) != 1 || ds[0].Missing != 1 {
		t.Fatalf("deficits=%+v", ds)
	}
	// 欠库期间 r2 仍在；day20 结算 r2：现金 10064>=100 正常购回，占用释放后欠库解除
	mustOK(t, s.CreditCash(20, []byte("w"), 1), "day20")
	if s.GetRepo([]byte("r2")).Status != StatusRepaid {
		t.Fatal("r2 repaid despite deficit (settlement not gated by deficit)")
	}
	if len(s.Deficits()) != 0 {
		t.Fatalf("deficit should clear after r2 release: %+v", s.Deficits())
	}
}
