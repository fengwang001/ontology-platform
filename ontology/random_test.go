package ontology

import (
	"fmt"
	"math/rand"
	"testing"
)

type oracle struct {
	thresholds []int64
	rates      []int64
	capFee     int64
	carryRate  int64
	period     int64
	accounts   map[string]*oracleAccount
	trades     map[string]*oracleTrade
}

type oracleAccount struct {
	start  int64
	trades []*oracleTrade
}

type oracleTrade struct {
	tid    string
	acct   string
	amount int64
	period int64
	active bool
}

type oracleCancel struct {
	fee     int64
	changes []FeeChange
	err     error
}

func newOracle(thresholds, rates []int64, capFee, carryRate int64) *oracle {
	return &oracle{
		thresholds: append([]int64(nil), thresholds...),
		rates:      append([]int64(nil), rates...),
		capFee:     capFee,
		carryRate:  carryRate,
		accounts:   make(map[string]*oracleAccount),
		trades:     make(map[string]*oracleTrade),
	}
}

func floorDiv(value int64) int64 {
	return value / 10_000
}

func (o *oracle) feeAt(cumulative int64) int64 {
	var fee int64
	last := int64(0)
	for index, threshold := range o.thresholds {
		if cumulative <= threshold {
			if cumulative > last {
				fee += floorDiv((cumulative - last) * o.rates[index])
			}
			if fee > o.capFee {
				return o.capFee
			}
			return fee
		}
		fee += floorDiv((threshold - last) * o.rates[index])
		last = threshold
	}
	if cumulative > last {
		fee += floorDiv((cumulative - last) * o.rates[len(o.thresholds)])
	}
	if fee > o.capFee {
		return o.capFee
	}
	return fee
}

func (o *oracle) trade(acct string, tid string, amount int64) (int64, error) {
	if acct == "" || tid == "" || amount < 1 || amount > 1_000_000_000 {
		return 0, ErrInvalidArgument
	}
	if _, exists := o.trades[tid]; exists {
		return 0, ErrDuplicateTrade
	}
	account := o.accounts[acct]
	start := int64(0)
	total := int64(0)
	if account != nil {
		start = account.start
		for _, trade := range account.trades {
			if trade.period == o.period && trade.active {
				total += trade.amount
			}
		}
	}
	if start+total+amount > maxBillingAmount {
		return 0, ErrCumulativeLimit
	}

	if account == nil {
		account = &oracleAccount{}
		o.accounts[acct] = account
	}
	record := &oracleTrade{tid: tid, acct: acct, amount: amount, period: o.period, active: true}
	account.trades = append(account.trades, record)
	o.trades[tid] = record
	return o.feeAt(start+total+amount) - o.feeAt(start+total), nil
}

func (o *oracle) cancel(tid string) (int64, []FeeChange, error) {
	if tid == "" {
		return 0, nil, ErrInvalidArgument
	}
	record, exists := o.trades[tid]
	if !exists {
		return 0, []FeeChange{}, ErrTradeNotFound
	}
	if !record.active {
		return 0, []FeeChange{}, ErrTradeCanceled
	}
	if record.period != o.period {
		return 0, []FeeChange{}, ErrPeriodClosed
	}

	account := o.accounts[record.acct]
	oldFees := map[string]int64{}
	for _, current := range account.trades {
		if current.period == o.period && current.active {
			cumulative := account.start
			var previous int64
			for _, item := range account.trades {
				if item.period == o.period && item.active {
					previous = cumulative
					cumulative += item.amount
					if item == current {
						oldFees[item.tid] = o.feeAt(cumulative) - o.feeAt(previous)
						break
					}
				}
			}
		}
	}

	oldFee := oldFees[tid]
	record.active = false
	cumulative := account.start
	changes := []FeeChange{}
	for _, current := range account.trades {
		if current.period != o.period || !current.active {
			continue
		}
		previous := cumulative
		cumulative += current.amount
		newFee := o.feeAt(cumulative) - o.feeAt(previous)
		if oldFees[current.tid] != newFee {
			changes = append(changes, FeeChange{Tid: current.tid, OldFee: oldFees[current.tid], NewFee: newFee})
		}
	}
	return oldFee, changes, nil
}

func (o *oracle) nextPeriod() {
	for _, account := range o.accounts {
		total := int64(0)
		for _, trade := range account.trades {
			if trade.period == o.period && trade.active {
				total += trade.amount
			}
		}
		account.start = floorDiv((account.start + total) * o.carryRate)
	}
	o.period++
}

func randomConfig(random *rand.Rand) ([]int64, []int64, int64, int64) {
	thresholdCount := 1 + random.Intn(3)
	thresholds := make([]int64, thresholdCount)
	threshold := int64(0)
	for index := range thresholds {
		threshold += int64(1 + random.Intn(10_000))
		thresholds[index] = threshold
	}
	rates := make([]int64, thresholdCount+1)
	for index := range rates {
		rates[index] = int64(random.Intn(10_001))
	}
	capFee := int64(random.Intn(20))
	carryRate := []int64{0, 5000, 10000, int64(random.Intn(10_001))}[random.Intn(4)]
	return thresholds, rates, capFee, carryRate
}

func TestRandomSequencesAgainstNaiveOracle(t *testing.T) {
	for sequence := 0; sequence < 2000; sequence++ {
		random := rand.New(rand.NewSource(int64(sequence + 1)))
		thresholds, rates, capFee, carryRate := randomConfig(random)
		billing, err := NewCumulativeBilling(thresholds, rates, capFee, carryRate)
		if err != nil {
			t.Fatalf("sequence %d setup: %v", sequence, err)
		}
		model := newOracle(thresholds, rates, capFee, carryRate)
		accounts := []string{"a", "b"}
		tidIndex := 0

		t.Logf("sequence=%d input config={thresholds:%v rates:%v cap:%d rho:%d} basis=naive recomputation oracle", sequence, thresholds, rates, capFee, carryRate)
		for operation := 0; operation < 24; operation++ {
			choice := random.Intn(10)
			switch {
			case choice < 7:
				acct := accounts[random.Intn(len(accounts))]
				tidIndex++
				tid := fmt.Sprintf("s%d-%d", sequence, tidIndex)
				amount := int64(1 + random.Intn(5_000))
				if random.Intn(12) == 0 {
					tid = ""
				}
				got, gotErr := billing.Trade(acct, tid, amount)
				want, wantErr := model.trade(acct, tid, amount)
				t.Logf("sequence=%d op=%d input Trade={account:%q tid:%q amount:%d} output={fee:%d error:%v} oracle={fee:%d error:%v}", sequence, operation, acct, tid, amount, got, gotErr, want, wantErr)
				if got != want || errorName(gotErr) != errorName(wantErr) {
					t.Fatalf("Trade mismatch: got=(%d,%v) want=(%d,%v)", got, gotErr, want, wantErr)
				}
			case choice < 9:
				tid := ""
				if tidIndex > 0 {
					tid = fmt.Sprintf("s%d-%d", sequence, 1+random.Intn(tidIndex))
				}
				if random.Intn(12) == 0 {
					tid = "missing"
				}
				oldFee, changes, gotErr := billing.Cancel(tid)
				wantFee, wantChanges, wantErr := model.cancel(tid)
				t.Logf("sequence=%d op=%d input Cancel={tid:%q} output={oldFee:%d changes:%v error:%v} oracle={oldFee:%d changes:%v error:%v}", sequence, operation, tid, oldFee, changes, gotErr, wantFee, wantChanges, wantErr)
				if oldFee != wantFee || errorName(gotErr) != errorName(wantErr) || !sameChanges(changes, wantChanges) {
					t.Fatalf("Cancel mismatch: got=(%d,%+v,%v) want=(%d,%+v,%v)", oldFee, changes, gotErr, wantFee, wantChanges, wantErr)
				}
			default:
				billing.NextPeriod()
				model.nextPeriod()
				t.Logf("sequence=%d op=%d input NextPeriod={} output={period:%d} oracle={period:%d} basis=atomic floor carry for every known account", sequence, operation, billing.CurrentPeriod(), model.period)
			}

			if billing.CurrentPeriod() != model.period {
				t.Fatalf("period mismatch: got=%d want=%d", billing.CurrentPeriod(), model.period)
			}
			for _, acct := range accounts {
				gotStart, gotExists := billing.AccountStart(acct)
				account, wantExists := model.accounts[acct]
				if gotExists != wantExists || (wantExists && gotStart != account.start) {
					t.Fatalf("account %s start mismatch: got=(%d,%v) want=(%d,%v)", acct, gotStart, gotExists, func() int64 {
						if account == nil {
							return 0
						}
						return account.start
					}(), wantExists)
				}
			}
		}
	}
}

func sameChanges(got, want []FeeChange) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}

func errorName(err error) string {
	switch err {
	case nil:
		return "nil"
	case ErrInvalidArgument:
		return "invalid"
	case ErrDuplicateTrade:
		return "duplicate"
	case ErrCumulativeLimit:
		return "limit"
	case ErrTradeNotFound:
		return "not-found"
	case ErrTradeCanceled:
		return "canceled"
	case ErrPeriodClosed:
		return "closed"
	default:
		return err.Error()
	}
}
