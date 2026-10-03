package ledger_test

import (
	"errors"
	"testing"

	"ontology/ledger"
)

func TestLedgerBasics(t *testing.T) {
	l := ledger.New()
	a := []byte("a")
	t.Logf("输入 Balance(从未出现) -> %d，判定未知账户余额/冻结均为 0", l.Balance(a))
	if l.Balance(a) != 0 || l.Avail(a) != 0 {
		t.Fatal("未知账户应视为 0")
	}
	if err := l.Deposit(a, 100); err != nil {
		t.Fatal(err)
	}
	if err := l.Freeze(a, 60); err != nil {
		t.Fatal(err)
	}
	t.Logf("Deposit100 + Freeze60 后 Avail=%d，判定相等通过、超出不足", l.Avail(a))
	if l.Avail(a) != 40 {
		t.Fatalf("Avail=%d want 40", l.Avail(a))
	}
	if err := l.Freeze(a, 41); !errors.Is(err, ledger.ErrInsufficient) {
		t.Fatalf("Freeze41 err=%v want ErrInsufficient", err)
	}
	if err := l.Freeze(a, 40); err != nil {
		t.Fatalf("恰好等于可用额应通过: %v", err)
	}
	l.Unfreeze(a, 40)
	l.Confirm(a, 60)
	if l.Balance(a) != 40 || l.Frozen(a) != 0 {
		t.Fatalf("Confirm 后 bal=%d fz=%d want 40/0", l.Balance(a), l.Frozen(a))
	}
}

func TestDepositLimits(t *testing.T) {
	l := ledger.New()
	a := []byte("a")
	for i := 0; i < ledger.MaxBalance/ledger.MaxDeposit; i++ {
		if err := l.Deposit(a, ledger.MaxDeposit); err != nil {
			t.Fatal(err)
		}
	}
	err := l.Deposit(a, 1)
	t.Logf("输入 Deposit 至超出 1e15 -> %v，判定 ErrLimit 且账户不变", err)
	if !errors.Is(err, ledger.ErrLimit) {
		t.Fatalf("err=%v want ErrLimit", err)
	}
	if l.Balance(a) != ledger.MaxBalance {
		t.Fatal("超限不得改账户")
	}
	for _, x := range []int64{0, -1, ledger.MaxDeposit + 1} {
		if err := l.Deposit(a, x); !errors.Is(err, ledger.ErrParam) {
			t.Fatalf("Deposit(%d) err=%v want ErrParam", x, err)
		}
	}
	if err := l.Deposit(nil, 1); !errors.Is(err, ledger.ErrParam) {
		t.Fatalf("空账户 err=%v want ErrParam", err)
	}
}
