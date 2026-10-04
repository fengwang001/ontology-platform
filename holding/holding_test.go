package holding

import (
	"errors"
	"testing"
)

func TestBookBasic(t *testing.T) {
	b := NewBook()
	if b.HasAcct([]byte("A")) {
		t.Fatal("account should not exist")
	}
	b.Deposit([]byte("A"), 100)
	if got, _, _ := b.Cash([]byte("A")); got != 100 {
		t.Fatalf("cash = %d", got)
	}
	if err := b.Trade([]byte("A"), []byte("S"), 10); err != nil {
		t.Fatal(err)
	}
	if err := b.Freeze([]byte("A"), []byte("S"), 4); err != nil {
		t.Fatal(err)
	}
	if err := b.Trade([]byte("A"), []byte("S"), -7); !errors.Is(err, ErrInsufficient) {
		t.Fatalf("want insufficient, got %v", err)
	}
	if err := b.Trade([]byte("A"), []byte("S"), -6); err != nil {
		t.Fatal(err)
	}
	if err := b.Unfreeze([]byte("A"), []byte("S"), 5); !errors.Is(err, ErrInsufficient) {
		t.Fatalf("want insufficient, got %v", err)
	}
	if err := b.Freeze([]byte("X"), []byte("S"), 1); !errors.Is(err, ErrNoAccount) {
		t.Fatalf("want no account, got %v", err)
	}
}

func TestBookApplyRounding(t *testing.T) {
	b := NewBook()
	b.Deposit([]byte("A"), 0)
	b.Deposit([]byte("B"), 0)
	if err := b.Trade([]byte("A"), []byte("S"), 1005); err != nil {
		t.Fatal(err)
	}
	if err := b.Freeze([]byte("A"), []byte("S"), 333); err != nil {
		t.Fatal(err)
	}
	if err := b.Trade([]byte("B"), []byte("S"), 7); err != nil {
		t.Fatal(err)
	}
	names, pos := b.SnapshotNamed([]byte("S"))
	if len(names) != 2 || names[0] != "A" || names[1] != "B" {
		t.Fatalf("snapshot = %v", names)
	}
	b.ResetTouched()
	aw := b.Apply([]byte("S"), names, pos, 30, 3, 767)
	if b.Touched() != 2 {
		t.Fatalf("touched = %d", b.Touched())
	}
	// A: q=1005 f=333 -> n=301 nf=99 m=3015 mf=999 frag=383
	if aw[0] != (Award{Shares: 301, SharesFroz: 99, Cash: 3015, CashFroz: 999, FragCash: 383}) {
		t.Fatalf("award A = %+v", aw[0])
	}
	// B: q=7 -> n=2 nf=0 m=21 mf=0 frag=floor(1*767/10)=76
	if aw[1].Shares != 2 || aw[1].SharesFroz != 0 || aw[1].Cash != 21 || aw[1].FragCash != 76 {
		t.Fatalf("award B = %+v", aw[1])
	}
	q, f, _ := b.Position([]byte("A"), []byte("S"))
	if q != 1306 || f != 432 {
		t.Fatalf("A pos = %d,%d", q, f)
	}
	avail, froz, _ := b.Cash([]byte("A"))
	// 可用现金 = (3015-999)+383 = 2399；冻结 = 999
	if avail != 2399 || froz != 999 {
		t.Fatalf("A cash = %d,%d", avail, froz)
	}
}
