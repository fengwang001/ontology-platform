package creation

import (
	"math/big"
	"sync"

	"ontology/basket"
	"ontology/subst"
)

type Item = basket.Item
type Flag = basket.Flag
type EndOfDayPayment = subst.EndOfDayPayment

const (
	N = basket.Forbidden
	A = basket.Allowed
	M = basket.Mandatory
)

type account struct {
	holdings    map[string]*big.Int
	cash        *big.Int
	units       *big.Int
	lockedToday int64
}

type Processor struct {
	mu sync.Mutex

	basket    *basket.Basket
	book      *subst.Book
	accounts  map[string]*account
	fundStock map[string]*big.Int
	fundCash  *big.Int

	clock     int64
	usedIDs   map[string]struct{}
	usedToday int64
	touched   int
}

type createPlan struct {
	item    basket.Item
	need    *big.Int
	deliver *big.Int
	deficit *big.Int
	price   int64
	charged *big.Int
	fixed   *big.Int
}

type redeemPlan struct {
	item    basket.Item
	need    *big.Int
	deliver *big.Int
	payment *big.Int
}

func New(items []Item, cashDiff int64, maxRatio int64, maxDailyUnits int64) (*Processor, error) {
	list, err := basket.New(items, cashDiff, maxRatio, maxDailyUnits)
	if err != nil {
		return nil, err
	}
	return &Processor{
		basket:    list,
		book:      subst.NewBook(),
		accounts:  make(map[string]*account),
		fundStock: make(map[string]*big.Int),
		fundCash:  new(big.Int),
		usedIDs:   make(map[string]struct{}),
	}, nil
}

func (p *Processor) Credit(now int64, acct, sym string, amount int64) error {
	if !validTime(now) || acct == "" || sym == "" || amount < 1 || amount > 1_000_000_000_000 {
		return ErrInvalidArgument
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if now < p.clock {
		return ErrClockRollback
	}
	current := p.ensureAccount(acct)
	addStock(current.holdings, sym, big.NewInt(amount))
	p.clock = now
	return nil
}

func (p *Processor) CreditCash(now int64, acct string, amount int64) error {
	if !validTime(now) || acct == "" || amount < 1 || amount > 1_000_000_000_000 {
		return ErrInvalidArgument
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if now < p.clock {
		return ErrClockRollback
	}
	current := p.ensureAccount(acct)
	current.cash.Add(current.cash, big.NewInt(amount))
	p.clock = now
	return nil
}

func (p *Processor) SetPrice(now int64, sym string, price int64) error {
	if !validTime(now) || sym == "" || price < 1 || price > 1_000_000 {
		return ErrInvalidArgument
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if now < p.clock {
		return ErrClockRollback
	}
	p.book.SetPrice(sym, price)
	p.clock = now
	return nil
}

func (p *Processor) Create(now int64, id, acct string, units int64) error {
	if !validTime(now) || id == "" || acct == "" || units < 1 || units > 1_000_000 {
		return ErrInvalidArgument
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if now < p.clock {
		return ErrClockRollback
	}
	current, ok := p.accounts[acct]
	if !ok {
		return ErrNotFound
	}
	if _, duplicate := p.usedIDs[id]; duplicate {
		return ErrDuplicateID
	}
	items := p.basket.Items()
	if !p.hasRequiredPrices(items) {
		return ErrNotFound
	}
	if p.usedToday+units > p.basket.MaxDailyUnits() || p.usedToday+units < 0 {
		return ErrDailyLimit
	}

	p.touched = 0
	plans := make([]createPlan, 0, len(items))
	substitution := new(big.Int)
	marketValue := new(big.Int)
	charges := new(big.Int)
	fixedTotal := new(big.Int)

	for _, item := range items {
		need := new(big.Int).Mul(big.NewInt(item.Qty), big.NewInt(units))
		entry := createPlan{
			item:    item,
			need:    need,
			deliver: new(big.Int),
			deficit: new(big.Int),
			charged: new(big.Int),
			fixed:   new(big.Int),
		}
		p.touched++
		owned := stockOf(current.holdings, item.Sym)
		switch item.Flag {
		case N:
			entry.price, _ = p.book.Price(item.Sym)
			if owned.Cmp(need) < 0 {
				return itemError{cause: ErrInsufficientSec, item: item.Sym}
			}
			entry.deliver.Set(need)
			marketValue.Add(marketValue, new(big.Int).Mul(need, big.NewInt(entry.price)))
		case A:
			entry.price, _ = p.book.Price(item.Sym)
			entry.deliver.Set(owned)
			if entry.deliver.Cmp(need) > 0 {
				entry.deliver.Set(need)
			}
			entry.deficit.Sub(need, entry.deliver)
			entry.charged = ceilDiv(
				new(big.Int).Mul(new(big.Int).Mul(entry.deficit, big.NewInt(entry.price)), big.NewInt(10_000+item.Prem)),
				big.NewInt(10_000),
			)
			substitution.Add(substitution, new(big.Int).Mul(entry.deficit, big.NewInt(entry.price)))
			marketValue.Add(marketValue, new(big.Int).Mul(need, big.NewInt(entry.price)))
			charges.Add(charges, entry.charged)
		case M:
			entry.fixed.Mul(big.NewInt(item.Fixed), big.NewInt(units))
			substitution.Add(substitution, entry.fixed)
			marketValue.Add(marketValue, entry.fixed)
			fixedTotal.Add(fixedTotal, entry.fixed)
		}
		plans = append(plans, entry)
	}

	left := new(big.Int).Mul(substitution, big.NewInt(100))
	right := new(big.Int).Mul(big.NewInt(p.basket.MaxRatio()), marketValue)
	if left.Cmp(right) > 0 {
		return ErrRatioExceeded
	}
	cashDifference := new(big.Int).Mul(big.NewInt(p.basket.CashDiff()), big.NewInt(units))
	netPayable := new(big.Int).Add(new(big.Int).Add(charges, fixedTotal), cashDifference)
	if netPayable.Sign() > 0 && current.cash.Cmp(netPayable) < 0 {
		return ErrInsufficientCash
	}

	pendingParts := make([]subst.PendingPart, 0)
	for _, entry := range plans {
		if entry.deliver.Sign() > 0 {
			current.holdings[entry.item.Sym] = new(big.Int).Sub(stockOf(current.holdings, entry.item.Sym), entry.deliver)
			addStock(p.fundStock, entry.item.Sym, entry.deliver)
		}
		if entry.item.Flag == A && entry.deficit.Sign() > 0 {
			pendingParts = append(pendingParts, subst.PendingPart{
				Sym:     entry.item.Sym,
				Deficit: entry.deficit.Int64(),
				Charged: new(big.Int).Set(entry.charged),
				PremBps: entry.item.Prem,
			})
		}
	}
	current.cash.Sub(current.cash, netPayable)
	p.fundCash.Add(p.fundCash, netPayable)
	current.units.Add(current.units, big.NewInt(units))
	current.lockedToday += units
	p.usedToday += units
	p.usedIDs[id] = struct{}{}
	p.book.AddPending(subst.PendingCreate{Account: acct, ID: id, Units: units, Parts: pendingParts})
	p.clock = now
	return nil
}

func (p *Processor) Redeem(now int64, acct string, units int64) error {
	if !validTime(now) || acct == "" || units < 1 || units > 1_000_000 {
		return ErrInvalidArgument
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if now < p.clock {
		return ErrClockRollback
	}
	current, ok := p.accounts[acct]
	if !ok {
		return ErrNotFound
	}
	available := new(big.Int).Sub(current.units, big.NewInt(current.lockedToday))
	if available.Cmp(big.NewInt(units)) < 0 {
		return ErrInsufficientUnit
	}
	items := p.basket.Items()
	if !p.hasRequiredPrices(items) {
		return ErrNotFound
	}

	plans := make([]redeemPlan, 0, len(items))
	income := new(big.Int)
	for _, item := range items {
		need := new(big.Int).Mul(big.NewInt(item.Qty), big.NewInt(units))
		entry := redeemPlan{item: item, need: need, deliver: new(big.Int), payment: new(big.Int)}
		switch item.Flag {
		case N:
			if stockOf(p.fundStock, item.Sym).Cmp(need) < 0 {
				return itemError{cause: ErrInsufficientInv, item: item.Sym}
			}
			entry.deliver.Set(need)
		case A:
			price, _ := p.book.Price(item.Sym)
			entry.deliver.Set(stockOf(p.fundStock, item.Sym))
			if entry.deliver.Cmp(need) > 0 {
				entry.deliver.Set(need)
			}
			deficit := new(big.Int).Sub(need, entry.deliver)
			entry.payment = floorDiv(
				new(big.Int).Mul(new(big.Int).Mul(deficit, big.NewInt(price)), big.NewInt(10_000-item.Prem)),
				big.NewInt(10_000),
			)
			income.Add(income, entry.payment)
		case M:
			entry.payment.Mul(big.NewInt(item.Fixed), big.NewInt(units))
			income.Add(income, entry.payment)
		}
		plans = append(plans, entry)
	}
	cashDifference := new(big.Int).Mul(big.NewInt(p.basket.CashDiff()), big.NewInt(units))
	netIncome := new(big.Int).Add(income, cashDifference)
	if netIncome.Sign() < 0 && current.cash.Cmp(new(big.Int).Neg(netIncome)) < 0 {
		return ErrInsufficientCash
	}

	for _, entry := range plans {
		if entry.deliver.Sign() > 0 {
			p.fundStock[entry.item.Sym] = new(big.Int).Sub(stockOf(p.fundStock, entry.item.Sym), entry.deliver)
			addStock(current.holdings, entry.item.Sym, entry.deliver)
		}
	}
	current.cash.Add(current.cash, netIncome)
	p.fundCash.Sub(p.fundCash, netIncome)
	current.units.Sub(current.units, big.NewInt(units))
	p.clock = now
	return nil
}

func (p *Processor) EndOfDay(now int64) ([]EndOfDayPayment, error) {
	if !validTime(now) {
		return nil, ErrInvalidArgument
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if now < p.clock {
		return nil, ErrClockRollback
	}
	cashByAccount := make(map[string]*big.Int, len(p.accounts))
	for name, current := range p.accounts {
		cashByAccount[name] = current.cash
	}
	payments := p.book.Settle(cashByAccount)
	for _, payment := range payments {
		p.fundCash.Sub(p.fundCash, payment.Refund)
		due := new(big.Int).Add(payment.Collected, payment.Shortfall)
		p.fundCash.Add(p.fundCash, due)
	}
	p.usedToday = 0
	for _, current := range p.accounts {
		current.lockedToday = 0
	}
	p.clock = now
	return payments, nil
}

func (p *Processor) Cash(acct string) *big.Int {
	p.mu.Lock()
	defer p.mu.Unlock()
	if current, ok := p.accounts[acct]; ok {
		return new(big.Int).Set(current.cash)
	}
	return nil
}

func (p *Processor) Holding(acct, sym string) *big.Int {
	p.mu.Lock()
	defer p.mu.Unlock()
	if current, ok := p.accounts[acct]; ok {
		return new(big.Int).Set(stockOf(current.holdings, sym))
	}
	return nil
}

func (p *Processor) Units(acct string) *big.Int {
	p.mu.Lock()
	defer p.mu.Unlock()
	if current, ok := p.accounts[acct]; ok {
		return new(big.Int).Set(current.units)
	}
	return nil
}

func (p *Processor) FundHolding(sym string) *big.Int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return new(big.Int).Set(stockOf(p.fundStock, sym))
}

func (p *Processor) FundCash() *big.Int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return new(big.Int).Set(p.fundCash)
}

func (p *Processor) Debt(acct string) *big.Int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.book.Debt(acct)
}

func (p *Processor) ensureAccount(acct string) *account {
	current, ok := p.accounts[acct]
	if !ok {
		current = &account{holdings: make(map[string]*big.Int), cash: new(big.Int), units: new(big.Int)}
		p.accounts[acct] = current
	}
	return current
}

func (p *Processor) hasRequiredPrices(items []basket.Item) bool {
	for _, item := range items {
		if item.Flag == M {
			continue
		}
		if _, ok := p.book.Price(item.Sym); !ok {
			return false
		}
	}
	return true
}

func addStock(stocks map[string]*big.Int, sym string, amount *big.Int) {
	stocks[sym] = new(big.Int).Add(stockOf(stocks, sym), amount)
}

func stockOf(stocks map[string]*big.Int, sym string) *big.Int {
	if amount, ok := stocks[sym]; ok {
		return amount
	}
	return new(big.Int)
}

func ceilDiv(value, divisor *big.Int) *big.Int {
	result := new(big.Int).Quo(value, divisor)
	if new(big.Int).Rem(value, divisor).Sign() != 0 {
		result.Add(result, big.NewInt(1))
	}
	return result
}

func floorDiv(value, divisor *big.Int) *big.Int {
	return new(big.Int).Quo(value, divisor)
}

func validTime(now int64) bool {
	return now >= 0 && now <= 1_000_000_000_000
}
