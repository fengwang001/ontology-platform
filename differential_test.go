package retrybudget

import (
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

type naiveEvent struct {
	at   int64
	cost int64
}

type naiveBudget struct {
	wd, wc, d, c, r, mx, cm int64
	latest                  int64
	deposits                []naiveEvent
	consumptions            []naiveEvent
}

type naiveResult struct {
	allowed bool
	balance int64
	reason  string
	err     error
}

func newNaiveBudget(cfg [7]int64) *naiveBudget {
	return &naiveBudget{
		wd: cfg[0],
		wc: cfg[1],
		d:  cfg[2],
		c:  cfg[3],
		r:  cfg[4],
		mx: cfg[5],
		cm: cfg[6],
	}
}

func (b *naiveBudget) validate(now int64) error {
	if now < 0 || now > 1_000_000_000_000_000 {
		return ErrInvalidTime
	}
	if now < b.latest {
		return ErrClockBacktrack
	}
	return nil
}

func (b *naiveBudget) activeDeposits(now int64) (int64, []int64) {
	var valid []int64
	var expired []int64
	for _, event := range b.deposits {
		if event.at+b.wd > now {
			valid = append(valid, event.at)
		} else {
			expired = append(expired, event.at)
		}
	}
	count := int64(len(valid))
	if count > b.cm {
		count = b.cm
	}
	return count, expired
}

func (b *naiveBudget) activeConsumptions(now int64) (int64, []naiveEvent, []int64) {
	var valid []naiveEvent
	var expired []int64
	for _, event := range b.consumptions {
		if event.at+b.wc > now {
			valid = append(valid, event)
		} else {
			expired = append(expired, event.at)
		}
	}
	var cost int64
	for _, event := range valid {
		cost += event.cost
	}
	return int64(len(valid)), valid, expired
}

func (b *naiveBudget) balance(now int64) int64 {
	depositCount, _ := b.activeDeposits(now)
	_, valid, _ := b.activeConsumptions(now)
	var frozenCost int64
	for _, event := range valid {
		frozenCost += event.cost
	}
	return b.r + b.d*depositCount - frozenCost
}

func (b *naiveBudget) request(now int64) naiveResult {
	if err := b.validate(now); err != nil {
		return naiveResult{err: err, reason: "rejected before ledger update"}
	}
	b.deposits = append(b.deposits, naiveEvent{at: now})
	b.latest = now
	depositCount, _ := b.activeDeposits(now)
	return naiveResult{
		balance: b.balance(now),
		reason:  fmt.Sprintf("accepted deposit; effectiveDeposits=min(%d,%d)=%d", len(b.deposits), b.cm, depositCount),
	}
}

func (b *naiveBudget) tryRetry(now int64) naiveResult {
	if err := b.validate(now); err != nil {
		return naiveResult{err: err, reason: "rejected before ledger update"}
	}

	k, validConsumptions, _ := b.activeConsumptions(now)
	multiplier := int64(1) + k
	if multiplier > b.mx {
		multiplier = b.mx
	}
	cost := b.c * multiplier
	depositCount, _ := b.activeDeposits(now)
	var frozenCost int64
	for _, event := range validConsumptions {
		frozenCost += event.cost
	}
	balance := b.r + b.d*depositCount - frozenCost

	result := naiveResult{balance: balance}
	if balance >= cost {
		b.consumptions = append(b.consumptions, naiveEvent{at: now, cost: cost})
		b.latest = now
		result.allowed = true
		result.balance = b.balance(now)
		result.reason = fmt.Sprintf("allowed: k=%d multiplier=min(%d,1+%d)=%d cost=%d balanceBefore=%d", k, b.mx, k, multiplier, cost, balance)
	} else {
		b.latest = now
		result.reason = fmt.Sprintf("denied: k=%d multiplier=min(%d,1+%d)=%d cost=%d balance=%d", k, b.mx, k, multiplier, cost, balance)
	}
	return result
}

func (b *naiveBudget) balanceOp(now int64) naiveResult {
	if err := b.validate(now); err != nil {
		return naiveResult{err: err, reason: "rejected before ledger update"}
	}
	b.latest = now
	depositCount, expiredDeposits := b.activeDeposits(now)
	k, validConsumptions, expiredConsumptions := b.activeConsumptions(now)
	var frozenCost int64
	for _, event := range validConsumptions {
		frozenCost += event.cost
	}
	return naiveResult{
		balance: b.balance(now),
		reason: fmt.Sprintf("effectiveDeposits=%d activeRetries=%d frozenCost=%d expiredDeposits=%v expiredRetries=%v",
			depositCount, k, frozenCost, expiredDeposits, expiredConsumptions),
	}
}

type differentialOperation struct {
	kind string
	now  int64
}

type actualResult struct {
	allowed bool
	balance int64
	err     error
}

func randomConfig(rng *rand.Rand) [7]int64 {
	return [7]int64{
		int64(rng.Intn(10)) + 1,
		int64(rng.Intn(15)) + 1,
		int64(rng.Intn(5)) + 1,
		int64(rng.Intn(5)) + 1,
		int64(rng.Intn(10)),
		int64(rng.Intn(4)) + 1,
		int64(rng.Intn(5)) + 1,
	}
}

func randomOperations(rng *rand.Rand, cfg [7]int64) []differentialOperation {
	const length = 45
	operations := make([]differentialOperation, 0, length)
	lastAccepted := int64(0)
	for i := 0; i < length; i++ {
		now := lastAccepted
		if rng.Intn(10) != 0 {
			now += int64(rng.Intn(12))
		}
		if rng.Intn(20) == 0 {
			now = lastAccepted - 1 - int64(rng.Intn(3))
		}
		if rng.Intn(30) == 0 {
			now = 1_000_000_000_000_000 + 1 + int64(rng.Intn(5))
		}

		kind := []string{"request", "retry", "balance"}[rng.Intn(3)]
		operations = append(operations, differentialOperation{kind: kind, now: now})
		if now >= 0 && now <= 1_000_000_000_000_000 && now >= lastAccepted {
			lastAccepted = now
		}
	}
	return operations
}

func sameError(a, b error) bool {
	return errors.Is(a, b) && errors.Is(b, a)
}

func TestRandomDifferentialAgainstNaiveScan(t *testing.T) {
	const groups = 2000
	rng := rand.New(rand.NewSource(1128))

	for group := 0; group < groups; group++ {
		cfg := randomConfig(rng)
		operations := randomOperations(rng, cfg)
		actual, err := NewRetryBudget(cfg[0], cfg[1], cfg[2], cfg[3], cfg[4], cfg[5], cfg[6])
		if err != nil {
			t.Fatalf("group %d config %v: %v", group, cfg, err)
		}
		replay, err := NewRetryBudget(cfg[0], cfg[1], cfg[2], cfg[3], cfg[4], cfg[5], cfg[6])
		if err != nil {
			t.Fatalf("replay group %d config %v: %v", group, cfg, err)
		}
		reference := newNaiveBudget(cfg)
		var log strings.Builder
		fmt.Fprintf(&log, "group=%d config={Wd:%d Wc:%d D:%d C:%d R:%d Mx:%d Cm:%d}\n",
			group, cfg[0], cfg[1], cfg[2], cfg[3], cfg[4], cfg[5], cfg[6])

		for index, operation := range operations {
			expected := naiveResult{}
			gotAllowed := false
			var gotBalance int64
			var gotErr error
			replayResult := actualResult{}

			switch operation.kind {
			case "request":
				expected = reference.request(operation.now)
				gotErr = actual.Request(operation.now)
				replayResult.err = replay.Request(operation.now)
			case "retry":
				expected = reference.tryRetry(operation.now)
				gotAllowed, gotErr = actual.TryRetry(operation.now)
				replayResult.allowed, replayResult.err = replay.TryRetry(operation.now)
			case "balance":
				expected = reference.balanceOp(operation.now)
				gotBalance, gotErr = actual.Balance(operation.now)
				replayResult.balance, replayResult.err = replay.Balance(operation.now)
			}
			if gotErr == nil {
				if operation.kind == "request" {
					gotBalance = actual.balanceAfterPrune()
				}
				if operation.kind == "retry" {
					gotBalance = actual.balanceAfterPrune()
				}
				if operation.kind == "request" {
					replayResult.balance = replay.balanceAfterPrune()
				}
				if operation.kind == "retry" {
					replayResult.balance = replay.balanceAfterPrune()
				}
			}

			fmt.Fprintf(&log, "%02d %-7s input={now:%d} output={allowed:%t balance:%d err:%v} basis=%s\n",
				index, operation.kind, operation.now, gotAllowed, gotBalance, gotErr, expected.reason)

			if !sameError(replayResult.err, gotErr) ||
				replayResult.allowed != gotAllowed ||
				replayResult.balance != gotBalance {
				t.Fatalf("group %d op %d replay mismatch: first=%+v replay=%+v\n%s",
					group, index, actualResult{allowed: gotAllowed, balance: gotBalance, err: gotErr}, replayResult, log.String())
			}

			if !sameError(gotErr, expected.err) {
				t.Fatalf("group %d op %d %s now=%d: error actual=%v reference=%v\n%s", group, index, operation.kind, operation.now, gotErr, expected.err, log.String())
			}
			if gotErr != nil {
				continue
			}
			if operation.kind == "retry" && gotAllowed != expected.allowed {
				t.Fatalf("group %d op %d allowed actual=%t reference=%t\n%s", group, index, gotAllowed, expected.allowed, log.String())
			}
			if gotBalance != expected.balance {
				t.Fatalf("group %d op %d %s now=%d balance actual=%d reference=%d\n%s", group, index, operation.kind, operation.now, gotBalance, expected.balance, log.String())
			}
		}

		if t.Failed() {
			t.Fatal(log.String())
		}
		t.Logf("differential group %d passed: %d operations\n%s", group, len(operations), log.String())
	}
}
