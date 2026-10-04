package pledge

import (
	"errors"
	"strings"
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

func setupExample(t *testing.T) *System {
	t.Helper()
	s := New()
	mustOK(t, s.AddBond(0, []byte("A"), 90, 99), "add A")
	mustOK(t, s.AddBond(0, []byte("B"), 75, 98), "add B")
	mustOK(t, s.Credit(0, []byte("u"), []byte("A"), 200), "credit A")
	mustOK(t, s.Credit(0, []byte("u"), []byte("B"), 200), "credit B")
	return s
}

func TestCapPerBondRounding(t *testing.T) {
	s := setupExample(t)
	mustOK(t, s.PledgeIn(0, []byte("u"), []byte("A"), 105), "in A")
	mustOK(t, s.PledgeIn(0, []byte("u"), []byte("B"), 50), "in B")
	a := s.Acct([]byte("u"))
	if a.Cap != 131 {
		t.Fatalf("Cap=%d want 131 (94+37 per-bond floor)", a.Cap)
	}
	if a.Pledged["A"] != 105 || a.Pledged["B"] != 50 {
		t.Fatalf("pledged=%v", a.Pledged)
	}
}

func TestPledgeOutInsufficientAndCap(t *testing.T) {
	s := setupExample(t)
	mustOK(t, s.PledgeIn(1, []byte("u"), []byte("A"), 105), "in A")
	mustOK(t, s.PledgeIn(1, []byte("u"), []byte("B"), 50), "in B")
	// 模拟 Use=131（repo 包之外直接锁内核验）
	s.Begin()
	if !s.AddUseLocked("u", 131) {
		t.Fatal("add use 131 should pass at cap 131")
	}
	s.Commit()
	// 出 B 1 张：floor(49*75/100)=36，Cap=130 < Use=131
	err := s.PledgeOut(2, []byte("u"), []byte("B"), 1)
	wantErr(t, err, haircut.ErrCap, "out B 1")
	a := s.Acct([]byte("u"))
	if a.Pledged["B"] != 50 {
		t.Fatalf("pledged B should stay 50, got %d", a.Pledged["B"])
	}
	// 库存不足
	err = s.PledgeOut(2, []byte("u"), []byte("A"), 1000)
	wantErr(t, err, haircut.ErrStock, "out too many")
	// 释放占用后可以出库
	s.Begin()
	s.ReleaseUseLocked("u", 131)
	s.Commit()
	mustOK(t, s.PledgeOut(2, []byte("u"), []byte("B"), 1), "out B 1 after release")
	a = s.Acct([]byte("u"))
	if a.Cap != 130 || a.Avail["B"] != 151 {
		t.Fatalf("after out Cap=%d availB=%d", a.Cap, a.Avail["B"])
	}
}

func TestRateCutDeficitAndReplenish(t *testing.T) {
	s := setupExample(t)
	mustOK(t, s.PledgeIn(1, []byte("u"), []byte("A"), 105), "in A")
	mustOK(t, s.PledgeIn(1, []byte("u"), []byte("B"), 50), "in B")
	s.Begin()
	s.AddUseLocked("u", 131)
	s.Commit()
	mustOK(t, s.SetRate(3, []byte("A"), 89), "rate cut")
	ds := s.Deficits()
	if len(ds) != 1 || string(ds[0].Acct) != "u" || ds[0].Missing != 1 {
		t.Fatalf("deficits=%+v", ds)
	}
	// 欠库时出库（即使出折算率为 0 的券）一律拒绝
	mustOK(t, s.AddBond(3, []byte("Z"), 0, 10), "add zero-rate Z")
	mustOK(t, s.Credit(3, []byte("u"), []byte("Z"), 10), "credit Z")
	mustOK(t, s.PledgeIn(3, []byte("u"), []byte("Z"), 10), "in Z allowed in deficit")
	err := s.PledgeOut(3, []byte("u"), []byte("Z"), 1)
	wantErr(t, err, haircut.ErrDeficit, "out zero-rate bond in deficit")
	err = s.PledgeOut(3, []byte("u"), []byte("B"), 1)
	wantErr(t, err, haircut.ErrDeficit, "out B in deficit")
	// 补库解除：B 再入 2 张，floor(52*75/100)=39，Cap=132
	mustOK(t, s.PledgeIn(4, []byte("u"), []byte("B"), 2), "in B 2")
	if a := s.Acct([]byte("u")); a.Cap != 132 {
		t.Fatalf("Cap=%d want 132", a.Cap)
	}
	if len(s.Deficits()) != 0 {
		t.Fatalf("deficit should be cleared: %+v", s.Deficits())
	}
	mustOK(t, s.PledgeOut(4, []byte("u"), []byte("Z"), 1), "out Z after recovery")
}

func TestRejectOrder(t *testing.T) {
	s := setupExample(t)
	mustOK(t, s.CreditCash(2, []byte("u"), 1), "advance day to 2")
	// 参数非法优先于一切
	wantErr(t, s.PledgeOut(-1, []byte("u"), []byte("A"), 1), haircut.ErrInvalid, "bad day")
	wantErr(t, s.PledgeIn(2, nil, []byte("A"), 1), haircut.ErrInvalid, "nil acct")
	wantErr(t, s.PledgeIn(2, []byte("u"), []byte("A"), 0), haircut.ErrInvalid, "zero qty")
	// 日期回退优先于不存在
	wantErr(t, s.PledgeIn(0, []byte("nope"), []byte("X"), 1), haircut.ErrDayBackward, "backward vs missing")
	// 不存在优先于库存/额度
	wantErr(t, s.PledgeIn(2, []byte("u"), []byte("X"), 1), haircut.ErrNoBond, "no bond")
	wantErr(t, s.PledgeIn(2, []byte("ghost"), []byte("A"), 1), haircut.ErrNoAccount, "no acct")
	// 不存在优先于可用不足
	wantErr(t, s.Credit(2, []byte("u"), []byte("X"), 1), haircut.ErrNoBond, "credit no bond")
	// 可用不足
	wantErr(t, s.PledgeIn(2, []byte("u"), []byte("A"), 10000), haircut.ErrAvail, "avail")
	// 债券重复
	wantErr(t, s.AddBond(2, []byte("A"), 1, 1), haircut.ErrDupBond, "dup bond")
}

func TestDayNeverAdvancesOnReject(t *testing.T) {
	s := setupExample(t)
	_ = s.PledgeIn(9, []byte("u"), []byte("A"), 1)
	if got := s.Day(); got != 9 {
		t.Fatalf("day=%d want 9", got)
	}
	_ = s.PledgeIn(5, []byte("u"), []byte("A"), 1) // 日期回退，拒绝
	if got := s.Day(); got != 9 {
		t.Fatalf("day changed on reject: %d", got)
	}
}

func TestHoldingSumOnlyChangesOnCreditAndSell(t *testing.T) {
	// 可用+在库之和：入库出库不改变总额（违约处置在 repo 测试中覆盖）
	s := setupExample(t)
	mustOK(t, s.PledgeIn(2, []byte("u"), []byte("A"), 30), "in 30")
	mustOK(t, s.PledgeOut(2, []byte("u"), []byte("A"), 10), "out 10")
	a := s.Acct([]byte("u"))
	if a.Avail["A"]+a.Pledged["A"] != 200 {
		t.Fatalf("total A=%d want 200", a.Avail["A"]+a.Pledged["A"])
	}
}

func TestTouchedIndependentOfBondKinds(t *testing.T) {
	for _, kinds := range []int{5, 5000} {
		s := New()
		for i := 0; i < kinds; i++ {
			rate := 80
			if i == 0 {
				rate = 0
			}
			code := []byte("B" + strings.Repeat("x", 4) + itoa(i))
			mustOK(t, s.AddBond(0, code, rate, 100), "add")
			mustOK(t, s.Credit(0, []byte("u"), code, 100), "credit")
			mustOK(t, s.PledgeIn(0, []byte("u"), code, 100), "pledge in")
		}
		capNow := s.Acct([]byte("u")).Cap
		s.Begin()
		if !s.AddUseLocked("u", capNow) { // 取等通过
			t.Fatal("add use at cap")
		}
		if got := s.TouchedLocked(); got != 0 {
			t.Fatalf("kinds=%d Repo cap-check touched=%d want 0", kinds, got)
		}
		s.Commit()
		// 第一张券折算率为 0：出 1 张额度不变仍应 >=Use？否——改出有贡献的券更稳妥：
		// 用第 2 张（rate=80），出 100 张使 Cap 降 80，必触发 Cap 不足，且只触碰 1 条记录。
		second := []byte("B" + strings.Repeat("x", 4) + itoa(1))
		err := s.PledgeOut(1, []byte("u"), second, 100)
		wantErr(t, err, haircut.ErrCap, "out all of one bond")
		if got := s.touched; got != 1 {
			t.Fatalf("kinds=%d PledgeOut touched=%d want 1", kinds, got)
		}
		// 第一张零折算券出 1 张：额度不变，成功；触碰数仍只有 1
		first := []byte("B" + strings.Repeat("x", 4) + itoa(0))
		mustOK(t, s.PledgeOut(1, []byte("u"), first, 1), "out zero-rate bond")
		if got := s.touched; got != 1 {
			t.Fatalf("kinds=%d zero-rate PledgeOut touched=%d want 1", kinds, got)
		}
	}
}

func TestSetRateTouchedEqualsHolderAccounts(t *testing.T) {
	s := New()
	mustOK(t, s.AddBond(0, []byte("A"), 90, 99), "add A")
	holders := 60
	for i := 0; i < holders; i++ {
		acct := []byte("acct" + itoa(i))
		mustOK(t, s.Credit(0, acct, []byte("A"), 10), "credit")
		if i%2 == 0 { // 仅 30 个账户入库
			mustOK(t, s.PledgeIn(0, acct, []byte("A"), 10), "in")
		}
	}
	mustOK(t, s.SetRate(1, []byte("A"), 88), "set rate")
	got := s.touched
	if got != holders/2 {
		t.Fatalf("SetRate touched=%d want %d", got, holders/2)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
