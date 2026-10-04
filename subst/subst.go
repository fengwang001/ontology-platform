package subst

import "math/big"

type PendingPart struct {
	Sym     string
	Deficit int64
	Charged *big.Int
	PremBps int64
}

type PendingCreate struct {
	Account string
	ID      string
	Units   int64
	Parts   []PendingPart
}

type EndOfDayPayment struct {
	Account   string
	ID        string
	Sym       string
	Cost      *big.Int
	Refund    *big.Int
	Collected *big.Int
	Shortfall *big.Int
}

type Book struct {
	prices  map[string]int64
	pending []PendingCreate
	debt    map[string]*big.Int
}

func NewBook() *Book { return &Book{} }

func (b *Book) SetPrice(sym string, price int64) {
	if b.prices == nil {
		b.prices = make(map[string]int64)
	}
	b.prices[sym] = price
}

func (b *Book) Price(sym string) (int64, bool) {
	price, ok := b.prices[sym]
	return price, ok
}

func (b *Book) AddPending(pending PendingCreate) {
	b.pending = append(b.pending, pending)
}

func (b *Book) Debt(account string) *big.Int {
	if amount, ok := b.debt[account]; ok {
		return new(big.Int).Set(amount)
	}
	return new(big.Int)
}

func (b *Book) Settle(cash map[string]*big.Int) []EndOfDayPayment {
	payments := make([]EndOfDayPayment, 0)
	for _, pending := range b.pending {
		for _, part := range pending.Parts {
			price, _ := b.prices[part.Sym]
			cost := new(big.Int).Mul(big.NewInt(part.Deficit), big.NewInt(price))
			difference := new(big.Int).Sub(part.Charged, cost)
			payment := EndOfDayPayment{
				Account:   pending.Account,
				ID:        pending.ID,
				Sym:       part.Sym,
				Cost:      cost,
				Refund:    new(big.Int),
				Collected: new(big.Int),
				Shortfall: new(big.Int),
			}
			if difference.Sign() >= 0 {
				payment.Refund = difference
				cash[pending.Account].Add(cash[pending.Account], difference)
			} else {
				due := new(big.Int).Neg(difference)
				balance := cash[pending.Account]
				paid := new(big.Int)
				if balance.Cmp(due) >= 0 {
					paid.Set(due)
					balance.Sub(balance, due)
				} else {
					paid.Set(balance)
					balance.SetInt64(0)
					shortfall := new(big.Int).Sub(due, paid)
					payment.Shortfall = shortfall
					if b.debt == nil {
						b.debt = make(map[string]*big.Int)
					}
					current, ok := b.debt[pending.Account]
					if !ok {
						current = new(big.Int)
					}
					b.debt[pending.Account] = new(big.Int).Add(current, shortfall)
				}
				payment.Collected = paid
			}
			payments = append(payments, payment)
		}
	}
	b.pending = nil
	return payments
}
