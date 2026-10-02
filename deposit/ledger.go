package deposit

import (
	"math/big"
	"sort"
)

var bigD = big.NewInt(D)

// ledger is an immutable view of daily flows, rate changes and credited
// interest used to evaluate day-end balances and accumulated products.
type ledger struct {
	flows      map[int64]int64
	flowDays   []int64 // sorted keys of flows
	flowPrefix []int64 // flowPrefix[i] = sum of flows over flowDays[:i+1]
	rates      map[int64]int64
	rateDays   []int64 // sorted keys of rates
	credits    []credit
	credPrefix []int64 // credPrefix[i] = sum of credits[:i+1] amounts
}

func newLedger(flows, rates map[int64]int64, credits []credit) *ledger {
	l := &ledger{flows: flows, rates: rates, credits: credits}
	l.flowDays = sortedKeys(flows)
	l.flowPrefix = make([]int64, len(l.flowDays))
	sum := int64(0)
	for i, d := range l.flowDays {
		sum += flows[d]
		l.flowPrefix[i] = sum
	}
	l.rateDays = sortedKeys(rates)
	l.credPrefix = make([]int64, len(credits))
	sum = 0
	for i, c := range credits {
		sum += c.amount
		l.credPrefix[i] = sum
	}
	return l
}

func sortedKeys(m map[int64]int64) []int64 {
	keys := make([]int64, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}

// flowSumThrough returns the net flow of all days <= k.
func (l *ledger) flowSumThrough(k int64) int64 {
	i := sort.Search(len(l.flowDays), func(i int) bool { return l.flowDays[i] > k })
	if i == 0 {
		return 0
	}
	return l.flowPrefix[i-1]
}

// creditedThrough returns interest credited on days <= k.
func (l *ledger) creditedThrough(k int64) int64 {
	i := sort.Search(len(l.credits), func(i int) bool { return l.credits[i].day > k })
	if i == 0 {
		return 0
	}
	return l.credPrefix[i-1]
}

// rateAt returns the annual rate in effect on day k (0 if never set).
func (l *ledger) rateAt(k int64) int64 {
	i := sort.Search(len(l.rateDays), func(i int) bool { return l.rateDays[i] > k })
	if i == 0 {
		return 0
	}
	return l.rates[l.rateDays[i-1]]
}

// balanceAt returns the day-end balance of day k.
func (l *ledger) balanceAt(k int64) int64 {
	return l.flowSumThrough(k) + l.creditedThrough(k)
}

// sumProducts returns Σ B_k·r_k for k in the half-open interval [from, to).
func (l *ledger) sumProducts(from, to int64) *big.Int {
	total := new(big.Int)
	bal := l.balanceAt(from - 1)
	ci := sort.Search(len(l.credits), func(i int) bool { return l.credits[i].day > from-1 })
	for k := from; k < to; k++ {
		bal += l.flows[k]
		for ci < len(l.credits) && l.credits[ci].day <= k {
			bal += l.credits[ci].amount
			ci++
		}
		if r := l.rateAt(k); r != 0 {
			t := big.NewInt(bal)
			total.Add(total, t.Mul(t, big.NewInt(r)))
		}
	}
	return total
}
