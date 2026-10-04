package subst

import (
	"math/big"
	"testing"
)

func TestSettleTable(t *testing.T) {
	tests := []struct {
		name       string
		charged    int64
		price      int64
		cash       int64
		wantCash   int64
		wantRefund int64
		wantDebt   int64
	}{
		{"refund", 1161, 22, 4139, 4200, 61, 0},
		{"collect with enough cash", 1161, 24, 4139, 4100, 0, 0},
		{"collect with insufficient cash", 1161, 24, 20, 0, 0, 19},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			book := NewBook()
			book.SetPrice("b", tt.price)
			book.AddPending(PendingCreate{
				Account: "acct",
				ID:      "id",
				Parts: []PendingPart{{
					Sym:     "b",
					Deficit: 50,
					Charged: big.NewInt(tt.charged),
				}},
			})
			cash := map[string]*big.Int{"acct": big.NewInt(tt.cash)}
			payments := book.Settle(cash)
			if len(payments) != 1 {
				t.Fatalf("payments = %d, want 1", len(payments))
			}
			if got := cash["acct"].Int64(); got != tt.wantCash {
				t.Fatalf("cash = %d, want %d", got, tt.wantCash)
			}
			if got := payments[0].Refund.Int64(); got != tt.wantRefund {
				t.Fatalf("refund = %d, want %d", got, tt.wantRefund)
			}
			if got := book.Debt("acct").Int64(); got != tt.wantDebt {
				t.Fatalf("debt = %d, want %d", got, tt.wantDebt)
			}
		})
	}
}
