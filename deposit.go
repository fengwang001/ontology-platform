package ontology

type LedgerEntry struct {
	Now     int
	House   string
	Amount  int
	Reason  string
	Balance int
}

type depositAccount struct {
	balance int
	ledger  []LedgerEntry
}

func newDepositAccount() *depositAccount { return &depositAccount{} }

type depositBank struct {
	accounts map[string]*depositAccount
}

func newDepositBank() *depositBank {
	return &depositBank{accounts: map[string]*depositAccount{}}
}

func (b *depositBank) account(house string) *depositAccount {
	a := b.accounts[house]
	if a == nil {
		a = newDepositAccount()
		b.accounts[house] = a
	}
	return a
}

func (a *depositAccount) credit(now int, house string, amount int, reason string) {
	a.balance += amount
	a.ledger = append(a.ledger, LedgerEntry{
		Now: now, House: house, Amount: amount, Reason: reason, Balance: a.balance,
	})
}

func (a *depositAccount) penalty(now int, house string, amount int) int {
	paid := a.balance
	if amount < paid {
		paid = amount
	}
	a.balance -= paid
	a.ledger = append(a.ledger, LedgerEntry{
		Now: now, House: house, Amount: -paid, Reason: "penalty", Balance: a.balance,
	})
	return paid
}

func (a *depositAccount) refund(now int, house string) int {
	paid := a.balance
	a.balance = 0
	if paid > 0 {
		a.ledger = append(a.ledger, LedgerEntry{
			Now: now, House: house, Amount: -paid, Reason: "refund", Balance: 0,
		})
	}
	return paid
}

func (a *depositAccount) balanceValue() int { return a.balance }
