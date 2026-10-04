package creation

import (
	"errors"
	"math/big"
	"sync"
	"testing"

	"ontology/basket"
)

func exampleItems() []Item {
	return []Item{
		{Sym: "a", Qty: 100, Flag: N},
		{Sym: "b", Qty: 200, Flag: A, Prem: 1050},
		{Sym: "c", Qty: 1, Flag: M, Fixed: 5000},
	}
}

func bi(value int64) *big.Int { return big.NewInt(value) }

func mustProcessor(t *testing.T, items []Item, e, ratio, q int64) *Processor {
	t.Helper()
	p, err := New(items, e, ratio, q)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return p
}

func setupExample(t *testing.T, bHold int64) *Processor {
	t.Helper()
	return setupExampleWithQ(t, bHold, 10)
}

func setupExampleWithQ(t *testing.T, bHold, q int64) *Processor {
	t.Helper()
	p := mustProcessor(t, exampleItems(), -300, 60, q)
	mustOK(t, p.SetPrice(1, "a", 10))
	mustOK(t, p.SetPrice(1, "b", 21))
	mustOK(t, p.Credit(2, "acct", "a", 100))
	mustOK(t, p.Credit(2, "acct", "b", bHold))
	mustOK(t, p.CreditCash(2, "acct", 10000))
	return p
}

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error %v", err)
	}
}

func TestExampleCreateRedeemSettlement(t *testing.T) {
	p := setupExample(t, 150)
	mustOK(t, p.Create(3, "id1", "acct", 1))
	if got := p.Cash("acct").Int64(); got != 4139 {
		t.Fatalf("cash after create = %d, want 4139", got)
	}
	if got := p.FundHolding("a").Int64(); got != 100 {
		t.Fatalf("fund a = %d, want 100", got)
	}
	if got := p.FundHolding("b").Int64(); got != 150 {
		t.Fatalf("fund b = %d, want 150", got)
	}
	if !errors.Is(p.Redeem(4, "acct", 1), ErrInsufficientUnit) {
		t.Fatal("same-day redeem must be locked")
	}

	mustOK(t, p.SetPrice(5, "b", 22))
	payments, err := p.EndOfDay(6)
	mustOK(t, err)
	if len(payments) != 1 || payments[0].Refund.Int64() != 61 {
		t.Fatalf("payments = %+v, want one refund 61", payments)
	}
	if got := p.Cash("acct").Int64(); got != 4200 {
		t.Fatalf("cash after refund = %d, want 4200", got)
	}

	mustOK(t, p.Redeem(7, "acct", 1))
	if got := p.Cash("acct").Int64(); got != 9884 {
		t.Fatalf("cash after redeem = %d, want 9884", got)
	}
	if got := p.Holding("acct", "a").Int64(); got != 100 {
		t.Fatalf("account a = %d, want 100", got)
	}
	if got := p.Holding("acct", "b").Int64(); got != 150 {
		t.Fatalf("account b = %d, want 150", got)
	}
}

func TestCreationTable(t *testing.T) {
	tests := []struct {
		name    string
		run     func(t *testing.T)
		wantErr error
	}{
		{"ratio equality passes", func(t *testing.T) {
			p := setupExampleWithQ(t, 150, 3)
			mustOK(t, p.Create(3, "id", "acct", 1))
		}, nil},
		{"ratio one over rejects", func(t *testing.T) {
			p := setupExample(t, 140)
			err := p.Create(3, "id", "acct", 1)
			wantError(t, err, ErrRatioExceeded)
			if got := p.Cash("acct").Int64(); got != 10000 {
				t.Fatalf("cash changed after reject: %d", got)
			}
			if got := p.Holding("acct", "a").Int64(); got != 100 {
				t.Fatalf("a changed after reject: %d", got)
			}
		}, ErrRatioExceeded},
		{"daily quota equality", func(t *testing.T) {
			p := setupExampleWithQ(t, 150, 1)
			mustOK(t, p.Create(3, "id1", "acct", 1))
			err := p.Create(4, "id2", "acct", 1)
			wantError(t, err, ErrDailyLimit)
			_, err = p.EndOfDay(6)
			mustOK(t, err)
			mustOK(t, p.Credit(7, "acct2", "a", 100))
			mustOK(t, p.Credit(7, "acct2", "b", 150))
			mustOK(t, p.CreditCash(7, "acct2", 10000))
			mustOK(t, p.Create(8, "id2", "acct2", 1))
		}, nil},
		{"nonpositive net skips cash", func(t *testing.T) {
			items := []Item{{Sym: "m", Qty: 1, Flag: basket.Mandatory, Fixed: 5}}
			p := mustProcessor(t, items, -10, 100, 1)
			mustOK(t, p.Credit(1, "acct", "x", 1))
			mustOK(t, p.Create(2, "id", "acct", 1))
			if got := p.FundCash().Int64(); got != -5 {
				t.Fatalf("fund cash = %d, want -5", got)
			}
		}, nil},
		{"collect shortfall becomes debt", func(t *testing.T) {
			p := setupExample(t, 150)
			mustOK(t, p.Create(3, "id", "acct", 1))
			mustOK(t, p.SetPrice(4, "b", 1_000_000))
			_, err := p.EndOfDay(5)
			mustOK(t, err)
			if got := p.Cash("acct").Int64(); got != 0 {
				t.Fatalf("cash = %d, want 0", got)
			}
			if got := p.Debt("acct").Int64(); got != 49_994_700 {
				t.Fatalf("debt = %d, want 49994700", got)
			}
		}, nil},
		{"smallest N shortage item", func(t *testing.T) {
			items := []Item{
				{Sym: "z", Qty: 1, Flag: N},
				{Sym: "a", Qty: 1, Flag: N},
			}
			p := mustProcessor(t, items, 0, 0, 1)
			mustOK(t, p.SetPrice(1, "a", 1))
			mustOK(t, p.SetPrice(1, "z", 1))
			mustOK(t, p.CreditCash(2, "acct", 1))
			err := p.Create(3, "id", "acct", 1)
			var detail itemError
			if !errors.As(err, &detail) || detail.Item() != "a" {
				t.Fatalf("err = %#v, want item a", err)
			}
		}, ErrInsufficientSec},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.run(t)
		})
	}
}

func wantError(t *testing.T, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("error = %v, want %v", got, want)
	}
}

func TestRejectionOrder(t *testing.T) {
	p := setupExample(t, 150)
	mustOK(t, p.Create(3, "id", "acct", 1))
	wantError(t, p.Create(-1, "id", "acct", 1), ErrInvalidArgument)
	wantError(t, p.Create(2, "id", "acct", 1), ErrClockRollback)
	wantError(t, p.Create(4, "id", "missing", 1), ErrNotFound)
	wantError(t, p.Create(4, "id", "acct", 1), ErrDuplicateID)
	wantError(t, p.Redeem(4, "acct", 2), ErrInsufficientUnit)
}

func TestTouchedIgnoresUnrelatedHoldings(t *testing.T) {
	for _, count := range []int{10, 10_000} {
		t.Run("", func(t *testing.T) {
			items := []Item{{Sym: "core", Qty: 1, Flag: N}}
			p := mustProcessor(t, items, 0, 0, 1)
			mustOK(t, p.SetPrice(1, "core", 1))
			mustOK(t, p.Credit(2, "acct", "core", 1))
			for i := 0; i < count; i++ {
				mustOK(t, p.Credit(2, "acct", "extra-"+itoa(i), 1))
			}
			mustOK(t, p.Create(3, "id", "acct", 1))
			if p.touched != 1 {
				t.Fatalf("with %d unrelated holdings, touched = %d, want 1", count, p.touched)
			}
		})
	}
}

func TestConcurrentCreditsAreSerializable(t *testing.T) {
	p := mustProcessor(t, exampleItems(), 0, 100, 1000)
	var group sync.WaitGroup
	ready := make(chan struct{})
	for i := 0; i < 100; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			<-ready
			if err := p.Credit(1, "acct", "outside", 1); err != nil {
				t.Errorf("credit error = %v", err)
			}
		}()
	}
	close(ready)
	group.Wait()
	if got := p.Holding("acct", "outside").Int64(); got != 100 {
		t.Fatalf("outside holding = %d, want 100", got)
	}
	mustOK(t, p.CreditCash(2, "acct", 100))
	if got := p.Cash("acct").Int64(); got != 100 {
		t.Fatalf("cash = %d, want 100", got)
	}
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	var bytes []byte
	for value > 0 {
		bytes = append([]byte{byte('0' + value%10)}, bytes...)
		value /= 10
	}
	return string(bytes)
}
