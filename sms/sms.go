package sms

import (
	"errors"
	"sync"
)

var (
	ErrInvalidArgument   = errors.New("sms: invalid argument")
	ErrAccountNotFound   = errors.New("sms: account not found")
	ErrClockRolledBack   = errors.New("sms: clock rolled back")
	ErrTooManySegments   = errors.New("sms: too many segments")
	ErrInsufficientFunds = errors.New("sms: insufficient funds")
)

type Meter struct {
	periodLength      int64
	baseFirst         int64
	firstPrice        int64
	secondPrice       int64
	internationalRate int
	mu                sync.Mutex
	accounts          map[string]*accountState
	maxNow            int64
}

type accountState struct {
	balance int64
	period  int64
	used    int64
	first   int64
}

type SplitResult struct {
	Encoding  string
	Intervals [][2]int
}

type SendResult struct {
	Encoding string
	Segments int
	Cost     int64
}

func New(P, T0, p1, p2 int64, MI int) (*Meter, error) {
	if P < 1 || P > 1_000_000_000 ||
		T0 < 0 || T0 > 1_000_000 ||
		p1 < 0 || p1 > 1_000_000 ||
		p2 < 0 || p2 > 1_000_000 ||
		MI < 100 || MI > 1000 {
		return nil, ErrInvalidArgument
	}
	return &Meter{
		periodLength:      P,
		baseFirst:         T0,
		firstPrice:        p1,
		secondPrice:       p2,
		internationalRate: MI,
		accounts:          make(map[string]*accountState),
	}, nil
}

func (m *Meter) Deposit(account string, amount int64) error {
	if account == "" || amount < 1 || amount > 1_000_000_000_000 {
		return ErrInvalidArgument
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	record := m.accounts[account]
	if record == nil {
		record = &accountState{first: m.baseFirst}
		m.accounts[account] = record
	}
	if record.balance > 1_000_000_000_000_000-amount {
		return ErrInvalidArgument
	}
	record.balance += amount
	return nil
}

func (m *Meter) Split(text string) (SplitResult, error) {
	return splitText(text)
}

func (m *Meter) Quote(account, text string, international bool, now int64) (SendResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	result, _, _, err := m.evaluate(account, text, international, now)
	return result, err
}

func (m *Meter) Send(account, text string, international bool, now int64) (SendResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	result, record, next, err := m.evaluate(account, text, international, now)
	if err != nil {
		return result, err
	}

	record.balance -= result.Cost
	record.period = next.period
	record.used = next.used
	record.first = next.first
	if now > m.maxNow {
		m.maxNow = now
	}
	return result, nil
}

func (m *Meter) evaluate(account, text string, international bool, now int64) (SendResult, *accountState, accountState, error) {
	if account == "" {
		return SendResult{}, nil, accountState{}, ErrInvalidArgument
	}
	if now < 0 || now > 1_000_000_000_000 {
		return SendResult{}, nil, accountState{}, ErrInvalidArgument
	}
	encoding, totalUnits, err := analyzeText(text)
	if err != nil {
		return SendResult{}, nil, accountState{}, err
	}

	record := m.accounts[account]
	if record == nil {
		return SendResult{}, nil, accountState{}, ErrAccountNotFound
	}
	if now < m.maxNow {
		return SendResult{}, nil, accountState{}, ErrClockRolledBack
	}

	split := packText(text, encoding, totalUnits)
	if len(split.Intervals) > 10 {
		return SendResult{}, nil, accountState{}, ErrTooManySegments
	}

	current := rollAccount(*record, m.periodLength, m.baseFirst, now)
	segments := int64(len(split.Intervals))
	var domestic int64
	for index := int64(1); index <= segments; index++ {
		if current.used+index <= current.first {
			domestic += m.firstPrice
		} else {
			domestic += m.secondPrice
		}
	}

	cost := domestic
	if international {
		cost = (domestic*int64(m.internationalRate) + 99) / 100
	}
	if cost > current.balance {
		return SendResult{}, nil, accountState{}, ErrInsufficientFunds
	}

	result := SendResult{
		Encoding: split.Encoding,
		Segments: len(split.Intervals),
		Cost:     cost,
	}
	current.used += segments
	return result, record, current, nil
}

func rollAccount(record accountState, periodLength, initialFirst, now int64) accountState {
	currentPeriod := now / periodLength
	if currentPeriod == record.period {
		return record
	}

	carriedSegments := int64(0)
	if currentPeriod == record.period+1 {
		carriedSegments = record.used
	}
	return accountState{
		balance: record.balance,
		period:  currentPeriod,
		used:    0,
		first:   initialFirst + carriedSegments/4,
	}
}
