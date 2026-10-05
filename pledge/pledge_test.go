package pledge

import (
	"errors"
	"fmt"
	"testing"

	"ontology/haircut"
)

// 建一个在库持有 kinds 种债券的账户，返回库。
func setupLibrary(kinds int) *Library {
	l := NewLibrary()
	for i := 0; i < kinds; i++ {
		bond := fmt.Sprintf("B%05d", i)
		l.Credit("acct", bond, 10)
		if err := l.PledgeIn("acct", bond, 10, 90); err != nil {
			panic(err)
		}
	}
	return l
}

// Repo 与 PledgeOut 的额度检查触碰的在库债券记录数不超过 1，
// 与账户在库债券种数无关（5 种与 5000 种两档对照）。
func TestTouchedCapCheck(t *testing.T) {
	for _, kinds := range []int{5, 5000} {
		t.Run(fmt.Sprintf("kinds=%d", kinds), func(t *testing.T) {
			l := setupLibrary(kinds)
			a, _ := l.Get("acct")
			if a.Cap != int64(kinds)*9 {
				t.Fatalf("Cap=%d, want %d", a.Cap, kinds*9)
			}
			before := l.touched
			if !l.HasCapacity("acct", a.Cap-a.Use) {
				t.Fatal("HasCapacity 取等应通过")
			}
			if d := l.touched - before; d > 1 {
				t.Fatalf("Repo 额度检查触碰 %d 条记录，超过 1", d)
			}
			before = l.touched
			if err := l.PledgeOut("acct", "B00000", 1, 90); err != nil {
				t.Fatal(err)
			}
			if d := l.touched - before; d > 1 {
				t.Fatalf("PledgeOut 额度检查触碰 %d 条记录，超过 1", d)
			}
			a, _ = l.Get("acct")
			if a.Cap != int64(kinds)*9-9+8 {
				t.Fatalf("出库后 Cap=%d, want %d", a.Cap, kinds*9-9+8)
			}
		})
	}
}

// SetRate 触碰的账户数等于在库持有该券的账户数。
func TestTouchedRetrate(t *testing.T) {
	l := NewLibrary()
	for _, acct := range []string{"a1", "a2", "a3"} {
		l.Credit(acct, "X", 10)
		if err := l.PledgeIn(acct, "X", 10, 100); err != nil {
			t.Fatal(err)
		}
	}
	l.Credit("a4", "Y", 10)
	if err := l.PledgeIn("a4", "Y", 10, 100); err != nil {
		t.Fatal(err)
	}
	// a2 出库清零后不再是在库持有者。
	if err := l.PledgeOut("a2", "X", 10, 100); err != nil {
		t.Fatal(err)
	}
	before := l.touched
	l.Retrate("X", 100, 50)
	if d := l.touched - before; d != 2 {
		t.Fatalf("Retrate 触碰 %d 个账户，want 2", d)
	}
	for _, acct := range []string{"a1", "a3"} {
		a, _ := l.Get(acct)
		if a.Cap != 5 {
			t.Fatalf("%s Cap=%d, want 5", acct, a.Cap)
		}
	}
	a2, _ := l.Get("a2")
	if a2.Cap != 0 {
		t.Fatalf("a2 Cap=%d, want 0", a2.Cap)
	}
}

func TestPledgeInOutErrors(t *testing.T) {
	l := NewLibrary()
	l.Credit("acct", "B", 10)
	if err := l.PledgeIn("acct", "B", 11, 90); !errors.Is(err, haircut.ErrInsufficientAvail) {
		t.Fatalf("err=%v, want 可用不足", err)
	}
	if err := l.PledgeIn("acct", "B", 10, 90); err != nil {
		t.Fatal(err)
	}
	if err := l.PledgeOut("acct", "B", 11, 90); !errors.Is(err, haircut.ErrInsufficientStock) {
		t.Fatalf("err=%v, want 库存不足", err)
	}
	// 占用顶满额度后出库应报标准券不足。
	l.AddUse("acct", 9)
	if err := l.PledgeOut("acct", "B", 1, 90); !errors.Is(err, haircut.ErrInsufficientCap) {
		t.Fatalf("err=%v, want 标准券不足", err)
	}
	l.ReleaseUse("acct", 9)
	if err := l.PledgeOut("acct", "B", 10, 90); err != nil {
		t.Fatal(err)
	}
	a, _ := l.Get("acct")
	if a.Cap != 0 || a.Pos["B"].Avail != 10 || a.Pos["B"].Pledged != 0 {
		t.Fatalf("state=%+v pos=%+v", a, a.Pos["B"])
	}
}

func TestDeficitsSorted(t *testing.T) {
	l := NewLibrary()
	for _, acct := range []string{"b", "a", "c"} {
		l.CreditCash(acct, 1)
		l.AddUse(acct, 5)
	}
	l.CreditCash("ok", 1)
	ds := l.Deficits()
	if len(ds) != 3 || ds[0].Acct != "a" || ds[1].Acct != "b" || ds[2].Acct != "c" {
		t.Fatalf("Deficits=%+v", ds)
	}
	for _, d := range ds {
		if d.Gap != 5 {
			t.Fatalf("Gap=%d, want 5", d.Gap)
		}
	}
}
