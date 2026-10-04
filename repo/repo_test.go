package repo

import (
	"errors"
	"testing"

	"ontology/haircut"
)

func mustOK(t *testing.T, err error, ctx string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", ctx, err)
	}
}

func wantErr(t *testing.T, err error, target error, ctx string) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("%s: got %v want %v", ctx, err, target)
	}
}

func newExample(t *testing.T) *System {
	t.Helper()
	s := New()
	mustOK(t, s.AddBond(0, []byte("A"), 90, 99), "add A")
	mustOK(t, s.AddBond(0, []byte("B"), 75, 98), "add B")
	mustOK(t, s.Credit(0, []byte("u"), []byte("A"), 200), "credit A")
	mustOK(t, s.Credit(0, []byte("u"), []byte("B"), 200), "credit B")
	mustOK(t, s.CreditCash(0, []byte("w"), 1), "spectator")
	return s
}

func TestWorkedExampleDeficitPhase(t *testing.T) {
	s := newExample(t)
	mustOK(t, s.PledgeIn(0, []byte("u"), []byte("A"), 105), "in A")
	mustOK(t, s.PledgeIn(0, []byte("u"), []byte("B"), 50), "in B")
	if a := s.Acct([]byte("u")); a.Cap != 131 {
		t.Fatalf("Cap=%d want 131", a.Cap)
	}
	mustOK(t, s.Repo(0, []byte("r1"), []byte("u"), 13050, 7, 250), "repo")
	rp := s.GetRepo([]byte("r1"))
	if rp.Due != 7 || rp.DueAmt != 13057 || rp.Status != StatusOpen {
		t.Fatalf("repo=%+v", rp)
	}
	if a := s.Acct([]byte("u")); a.Cash != 13050 || a.Use != 131 {
		t.Fatalf("cash=%d use=%d", a.Cash, a.Use)
	}
	// 额度取等后再多融必拒
	wantErr(t, s.Repo(0, []byte("r2"), []byte("u"), 100, 1, 0), haircut.ErrCap, "cap boundary")
	// 出 B 1：Cap 130 < Use 131
	wantErr(t, s.PledgeOut(0, []byte("u"), []byte("B"), 1), haircut.ErrCap, "out B")
	// 下调 A 到 89：Cap 130，欠库 1
	mustOK(t, s.SetRate(0, []byte("A"), 89), "rate cut")
	ds := s.Deficits()
	if len(ds) != 1 || string(ds[0].Acct) != "u" || ds[0].Missing != 1 {
		t.Fatalf("deficits=%+v", ds)
	}
	// 欠库时 PledgeOut（含零折算券）与 Repo 一律拒绝
	mustOK(t, s.AddBond(1, []byte("Z"), 0, 10), "add zero rate Z")
	mustOK(t, s.Credit(1, []byte("u"), []byte("Z"), 5), "credit Z")
	mustOK(t, s.PledgeIn(1, []byte("u"), []byte("Z"), 5), "in Z allowed")
	wantErr(t, s.PledgeOut(1, []byte("u"), []byte("Z"), 1), haircut.ErrDeficit, "out zero bond")
	wantErr(t, s.PledgeOut(1, []byte("u"), []byte("B"), 1), haircut.ErrDeficit, "out B")
	wantErr(t, s.Repo(1, []byte("r9"), []byte("u"), 1, 1, 0), haircut.ErrDeficit, "repo in deficit")
	// 补库解除：B +2，floor(52*75/100)=39，Cap=93+39=132
	mustOK(t, s.PledgeIn(2, []byte("u"), []byte("B"), 2), "in B 2")
	if a := s.Acct([]byte("u")); a.Cap != 132 || a.Use != 131 {
		t.Fatalf("Cap=%d Use=%d", a.Cap, a.Use)
	}
	if len(s.Deficits()) != 0 {
		t.Fatal("deficit not cleared")
	}
}

func TestDefaultDisposalExactExample(t *testing.T) {
	s := newExample(t)
	mustOK(t, s.PledgeIn(0, []byte("u"), []byte("A"), 105), "in A")
	mustOK(t, s.PledgeIn(0, []byte("u"), []byte("B"), 52), "in B")
	// Cap=94+39=133，Use=131
	mustOK(t, s.Repo(0, []byte("r1"), []byte("u"), 13050, 7, 250), "repo")
	// 旁观者入口推进到第 7 日触发结算；u 现金 13050 < 13057
	mustOK(t, s.CreditCash(7, []byte("w"), 1), "day 7 settle")
	rp := s.GetRepo([]byte("r1"))
	if rp.Status != StatusDefault || rp.BadDebt != 0 {
		t.Fatalf("status=%d badDebt=%d", rp.Status, rp.BadDebt)
	}
	a := s.Acct([]byte("u"))
	// A 105 张全卖 10395，余 2662；B ceil(2662/98)=28 张卖 2744，退 82
	if a.Cash != 13132 {
		t.Fatalf("cash=%d want 13132", a.Cash)
	}
	if a.Pledged["B"] != 24 {
		t.Fatalf("B left=%d want 24", a.Pledged["B"])
	}
	if a.Pledged["A"] != 0 {
		t.Fatalf("A left=%d want 0", a.Pledged["A"])
	}
	if a.Cap != 18 || a.Use != 0 {
		t.Fatalf("Cap=%d want 18, Use=%d want 0", a.Cap, a.Use)
	}
}

func TestRepaidOnDueDayExact(t *testing.T) {
	s := newExample(t)
	mustOK(t, s.PledgeIn(0, []byte("u"), []byte("A"), 105), "in A")
	mustOK(t, s.Repo(0, []byte("r1"), []byte("u"), 1000, 1, 0), "repo due day1")
	mustOK(t, s.CreditCash(1, []byte("w"), 1), "day 1")
	rp := s.GetRepo([]byte("r1"))
	if rp.Status != StatusRepaid || rp.DueAmt != 1000 || rp.Due != 1 {
		t.Fatalf("rp=%+v", rp)
	}
	a := s.Acct([]byte("u"))
	if a.Cash != 0 || a.Use != 0 || a.Pledged["A"] != 105 {
		t.Fatalf("cash=%d use=%d pledgedA=%d", a.Cash, a.Use, a.Pledged["A"])
	}
}

func TestDuesOrderWithInterestCausedDefault(t *testing.T) {
	s := New()
	mustOK(t, s.AddBond(0, []byte("A"), 100, 10), "bond price 10")
	mustOK(t, s.Credit(0, []byte("u"), []byte("A"), 100), "credit")
	mustOK(t, s.PledgeIn(0, []byte("u"), []byte("A"), 100), "pledge Cap=100")
	// r1 day3 融 3000(occ30)；r2 day5 融 7000(occ70)，r2 利率 10000bp
	mustOK(t, s.Repo(0, []byte("r1"), []byte("u"), 3000, 3, 0), "r1")
	mustOK(t, s.Repo(0, []byte("r2"), []byte("u"), 7000, 5, 10000), "r2")
	// day3：r1 购回，现金 10000-3000=7000；r2 未到期
	mustOK(t, s.CreditCash(0, []byte("w"), 1), "w")
	mustOK(t, s.CreditCash(3, []byte("w"), 1), "day3")
	if s.GetRepo([]byte("r1")).Status != StatusRepaid {
		t.Fatal("r1 repaid")
	}
	// day5：r2 应还 7096，现金 7000 不足 -> 违约；A 价 10，ceil(7096/10)=710 张，
	// 在库 100 张全卖得 1000，坏账 6096；无余款。
	mustOK(t, s.CreditCash(5, []byte("w"), 1), "day5")
	r2 := s.GetRepo([]byte("r2"))
	if r2.Status != StatusDefault {
		t.Fatalf("r2 status=%d want default", r2.Status)
	}
	if r2.BadDebt != 6096 {
		t.Fatalf("badDebt=%d want 6096", r2.BadDebt)
	}
	a := s.Acct([]byte("u"))
	if a.Pledged["A"] != 0 || a.Cash != 7000 || a.Cap != 0 || a.Use != 0 {
		t.Fatalf("pledged=%d cash=%d cap=%d use=%d", a.Pledged["A"], a.Cash, a.Cap, a.Use)
	}
}

func TestSameDayDuesOrderedByAcceptSeq(t *testing.T) {
	// 同日到期两笔，均足额，验证按接受序号扣款后两笔都购回，且金额可复现。
	s := New()
	mustOK(t, s.AddBond(0, []byte("A"), 100, 100), "bond")
	mustOK(t, s.Credit(0, []byte("u"), []byte("A"), 100), "credit")
	mustOK(t, s.PledgeIn(0, []byte("u"), []byte("A"), 50), "pledge Cap50")
	mustOK(t, s.Repo(0, []byte("r1"), []byte("u"), 2000, 4, 0), "r1 occ20")
	mustOK(t, s.Repo(0, []byte("r2"), []byte("u"), 3000, 4, 0), "r2 occ30")
	mustOK(t, s.CreditCash(0, []byte("w"), 1), "w")
	// 现金 5000，r1 扣 2000 余 3000，r2 扣 3000 余 0
	mustOK(t, s.CreditCash(4, []byte("w"), 1), "day4")
	if s.GetRepo([]byte("r1")).Status != StatusRepaid || s.GetRepo([]byte("r2")).Status != StatusRepaid {
		t.Fatal("both repaid")
	}
	if cash := s.Acct([]byte("u")).Cash; cash != 0 {
		t.Fatalf("cash=%d want 0", cash)
	}
	// 反序接受（先大额后小额）重放结果一致：
	s2 := New()
	mustOK(t, s2.AddBond(0, []byte("A"), 100, 100), "bond")
	mustOK(t, s2.Credit(0, []byte("u"), []byte("A"), 100), "credit")
	mustOK(t, s2.PledgeIn(0, []byte("u"), []byte("A"), 50), "pledge")
	mustOK(t, s2.Repo(0, []byte("r1"), []byte("u"), 3000, 4, 0), "r1 big")
	mustOK(t, s2.Repo(0, []byte("r2"), []byte("u"), 2000, 4, 0), "r2 small")
	mustOK(t, s2.CreditCash(0, []byte("w"), 1), "w")
	mustOK(t, s2.CreditCash(4, []byte("w"), 1), "day4")
	if cash := s2.Acct([]byte("u")).Cash; cash != 0 {
		t.Fatalf("reversed cash=%d", cash)
	}
}
