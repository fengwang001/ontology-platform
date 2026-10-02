package burstmeter

import (
	"errors"
	"sync"
)

var (
	ErrInvalidArgument = errors.New("invalid argument")
	ErrSequenceRewind  = errors.New("sequence rewind")
	ErrSequenceGap     = errors.New("sequence gap")
)

type DebtBatch struct {
	Minute    int64
	Remaining int64
}

type TickResult struct {
	Fee       int64
	Balance   int64
	Debt      int64
	Dropped   int64
	Throttled int64
}

type Snapshot struct {
	Balance              int64
	LaunchBalance        int64
	Debt                 int64
	DroppedTotal         int64
	ThrottledTotal       int64
	BilledTotal          int64
	CreditedTotal        int64
	BalanceConsumedTotal int64
	LaunchConsumedTotal  int64
	RepaidTotal          int64
	NextMinute           int64
	Mode                 int
	Batches              []DebtBatch
}

type Meter struct {
	mu                   sync.Mutex
	cores                int64
	baseline             int64
	creditPerTick        int64
	balanceCap           int64
	launchBalance        int64
	window               int64
	price                int64
	debtCap              int64
	balance              int64
	debtBatches          []DebtBatch
	droppedTotal         int64
	throttledTotal       int64
	billedTotal          int64
	creditedTotal        int64
	balanceConsumedTotal int64
	launchConsumedTotal  int64
	repaidTotal          int64
	nextMinute           int64
	mode                 int
}

func NewMeter(cores int64, baselinePercent int64, balanceCap int64, launchCredits int64, settlementWindow int64, unitPrice int64, debtCap int64, mode int) (*Meter, error) {
	if cores < 1 || cores > 64 ||
		baselinePercent < 1 || baselinePercent > 100 ||
		balanceCap < 0 || balanceCap > 1_000_000_000_000 ||
		launchCredits < 0 || launchCredits > balanceCap ||
		settlementWindow < 1 || settlementWindow > 1_000_000 ||
		unitPrice < 0 || unitPrice > 1_000_000 ||
		debtCap < 0 || debtCap > 1_000_000_000_000 ||
		(mode != 0 && mode != 1) {
		return nil, ErrInvalidArgument
	}

	return &Meter{
		cores:         cores,
		baseline:      baselinePercent,
		creditPerTick: cores * baselinePercent,
		balanceCap:    balanceCap,
		launchBalance: launchCredits,
		window:        settlementWindow,
		price:         unitPrice,
		debtCap:       debtCap,
		mode:          mode,
	}, nil
}

func (m *Meter) Tick(minute int64, utilization int64) (TickResult, error) {
	if minute < 0 || utilization < 0 || utilization > 100 {
		return TickResult{}, ErrInvalidArgument
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if minute < m.nextMinute {
		return TickResult{}, ErrSequenceRewind
	}
	if minute > m.nextMinute {
		return TickResult{}, ErrSequenceGap
	}

	result := TickResult{}

	// Step 1: expire and bill mature debt batches before this minute's credit.
	kept := make([]DebtBatch, 0, len(m.debtBatches))
	for _, batch := range m.debtBatches {
		if minute-batch.Minute >= m.window {
			result.Fee += batch.Remaining * m.price
			continue
		}
		kept = append(kept, batch)
	}
	m.debtBatches = kept

	// Step 2: credit first repays the oldest debt, then the remainder enters bal.
	remainingCredit := m.creditPerTick
	for i := 0; i < len(m.debtBatches); i++ {
		if remainingCredit == 0 {
			break
		}
		payment := min(remainingCredit, m.debtBatches[i].Remaining)
		m.debtBatches[i].Remaining -= payment
		remainingCredit -= payment
		m.repaidTotal += payment
	}
	if len(m.debtBatches) > 0 && m.debtBatches[0].Remaining == 0 {
		firstOpen := 0
		for firstOpen < len(m.debtBatches) && m.debtBatches[firstOpen].Remaining == 0 {
			firstOpen++
		}
		m.debtBatches = append([]DebtBatch(nil), m.debtBatches[firstOpen:]...)
	}
	m.creditedTotal += m.creditPerTick
	m.balance += remainingCredit

	// Step 3: launch credits are consumed before balance, then debt or throttle.
	need := m.cores * utilization
	launchUsed := min(m.launchBalance, need)
	m.launchBalance -= launchUsed
	m.launchConsumedTotal += launchUsed
	need -= launchUsed

	balanceUsed := min(m.balance, need)
	m.balance -= balanceUsed
	m.balanceConsumedTotal += balanceUsed
	need -= balanceUsed

	if need > 0 {
		outstanding := int64(0)
		for _, batch := range m.debtBatches {
			outstanding += batch.Remaining
		}
		room := m.debtCap - outstanding
		if m.mode == 1 && room > 0 {
			added := min(need, room)
			m.debtBatches = append(m.debtBatches, DebtBatch{
				Minute:    minute,
				Remaining: added,
			})
			need -= added
		}
		result.Throttled = need
		m.throttledTotal += need
	}

	// Step 4: cap the balance after consumption; the excess is discarded.
	if m.balance > m.balanceCap {
		result.Dropped = m.balance - m.balanceCap
		m.balance = m.balanceCap
		m.droppedTotal += result.Dropped
	}

	m.billedTotal += result.Fee
	m.nextMinute = minute + 1

	result.Balance = m.balance
	for _, batch := range m.debtBatches {
		result.Debt += batch.Remaining
	}
	return result, nil
}

func (m *Meter) SetMode(mode int) (int64, error) {
	if mode != 0 && mode != 1 {
		return 0, ErrInvalidArgument
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	fee := int64(0)
	if m.mode == 1 && mode == 0 {
		for _, batch := range m.debtBatches {
			fee += batch.Remaining * m.price
		}
		m.debtBatches = nil
		m.billedTotal += fee
	}
	m.mode = mode
	return fee, nil
}

func (m *Meter) Snapshot() Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()

	snapshot := Snapshot{
		Balance:              m.balance,
		LaunchBalance:        m.launchBalance,
		DroppedTotal:         m.droppedTotal,
		ThrottledTotal:       m.throttledTotal,
		BilledTotal:          m.billedTotal,
		CreditedTotal:        m.creditedTotal,
		BalanceConsumedTotal: m.balanceConsumedTotal,
		LaunchConsumedTotal:  m.launchConsumedTotal,
		RepaidTotal:          m.repaidTotal,
		NextMinute:           m.nextMinute,
		Mode:                 m.mode,
		Batches:              make([]DebtBatch, len(m.debtBatches)),
	}
	copy(snapshot.Batches, m.debtBatches)
	for _, batch := range m.debtBatches {
		snapshot.Debt += batch.Remaining
	}
	return snapshot
}
