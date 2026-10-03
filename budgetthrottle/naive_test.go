package budgetthrottle

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"testing"
)

type naiveResult struct {
	charged int64
	err     error
}

type naiveOp struct {
	kind   string
	amount int64
	now    int64
}

type naiveState struct {
	budget  int64
	length  int64
	catchUp int64
	targets []int64
	spent   int64
	maxNow  int64
	period  int
	allow   int64
	ps      int64
	started bool
}

func newNaiveState(budget int64, length int64, catchUp int64, targets []int64) *naiveState {
	return &naiveState{
		budget:  budget,
		length:  length,
		catchUp: catchUp,
		targets: targets,
		period:  -1,
	}
}

func (s *naiveState) validNow(now int64) bool {
	return now >= 0 && now < int64(len(s.targets))*s.length
}

func (s *naiveState) target(period int) int64 {
	if period < 0 {
		return 0
	}
	return s.targets[period]
}

func (s *naiveState) allowanceFor(period int) int64 {
	if s.started && s.period == period {
		return s.allow
	}
	previous := s.target(period - 1)
	current := s.target(period)
	periodBudget := current - previous
	deficit := previous - s.spent
	if deficit < 0 {
		deficit = 0
	}
	catchUpCap := periodBudget * s.catchUp / 100
	allowance := periodBudget + minInt64(deficit, catchUpCap)
	remaining := s.budget - s.spent
	if allowance > remaining {
		allowance = remaining
	}
	return allowance
}

func (s *naiveState) run(op naiveOp) naiveResult {
	if !s.validNow(op.now) {
		return naiveResult{err: ErrInvalidArgument}
	}
	if op.kind != "allowance" && (op.amount < 1 || op.amount > 1_000_000_000_000) {
		return naiveResult{err: ErrInvalidArgument}
	}
	if op.kind != "allowance" && op.now < s.maxNow {
		return naiveResult{err: ErrClockRolledBack}
	}

	period := int(op.now / s.length)
	allowance := s.allowanceFor(period)

	switch op.kind {
	case "try":
		if s.spent+op.amount > s.budget {
			return naiveResult{err: ErrBudgetExhausted}
		}
		if op.amount > allowance {
			return naiveResult{err: ErrAmountTooLarge}
		}
		ps := int64(0)
		if s.started && s.period == period {
			ps = s.ps
		}
		if ps+op.amount > allowance {
			return naiveResult{err: ErrRateLimited}
		}
		if !s.started || s.period != period {
			s.started = true
			s.period = period
			s.allow = allowance
			s.ps = 0
		}
		s.spent += op.amount
		s.ps += op.amount
		s.maxNow = op.now
		return naiveResult{}
	case "upto":
		remaining := allowance
		if s.started && s.period == period {
			remaining = allowance - s.ps
		}
		charged := minInt64(op.amount, remaining)
		if charged == 0 {
			return naiveResult{err: ErrRateLimited}
		}
		if !s.started || s.period != period {
			s.started = true
			s.period = period
			s.allow = allowance
			s.ps = 0
		}
		s.spent += charged
		s.ps += charged
		s.maxNow = op.now
		return naiveResult{charged: charged}
	case "refund":
		if op.amount > s.spent {
			return naiveResult{err: ErrRefundTooLarge}
		}
		if !s.started || s.period != period {
			s.started = true
			s.period = period
			s.allow = allowance
			s.ps = 0
		}
		s.spent -= op.amount
		s.ps -= minInt64(s.ps, op.amount)
		s.maxNow = op.now
		return naiveResult{}
	case "allowance":
		if s.started && period < s.period {
			return naiveResult{charged: 0}
		}
		if s.started && period == s.period {
			return naiveResult{charged: s.allow - s.ps}
		}
		return naiveResult{charged: allowance}
	default:
		return naiveResult{err: ErrInvalidArgument}
	}
}

func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func TestRandomSequencesMatchNaiveSimulation(t *testing.T) {
	rng := rand.New(rand.NewPCG(1262, 42))
	const sequences = 2000
	logPath := "random-sequences.log"
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatalf("create log: %v", err)
	}
	defer logFile.Close()
	t.Logf("random input/output/reason log: %s", logPath)

	type recordedConfig struct {
		budget  int64
		periods int
		length  int64
		weights []int64
		catchUp int64
		ops     []naiveOp
		results []naiveResult
	}
	var records []recordedConfig

	for sequence := 0; sequence < sequences; sequence++ {
		periods := 1 + rng.IntN(12)
		length := int64(1 + rng.IntN(5))
		budget := int64(1 + rng.IntN(200))
		catchUp := int64(rng.IntN(201))
		weights := make([]int64, periods)
		for i := range weights {
			weights[i] = int64(1 + rng.IntN(5))
		}

		throttler, err := NewThrottler(budget, periods, length, weights, catchUp)
		if err != nil {
			t.Fatalf("sequence=%d constructor failed: %v", sequence, err)
		}
		naive := newNaiveState(budget, length, catchUp, append([]int64(nil), throttler.targets...))
		record := recordedConfig{
			budget:  budget,
			periods: periods,
			length:  length,
			weights: append([]int64(nil), weights...),
			catchUp: catchUp,
		}

		ops := 1 + rng.IntN(40)
		for step := 0; step < ops; step++ {
			op := randomNaiveOp(t, rng, periods, length)
			record.ops = append(record.ops, op)
			got, reason := executeAgainstThrottler(throttler, op)
			want := naive.run(op)
			record.results = append(record.results, got)
			fmt.Fprintf(logFile, "sequence=%d step=%d input=%s B=%d n=%d L=%d weights=%v m=%d output=(charged=%d,err=%v) expected=(charged=%d,err=%v) reason=%s state=(spent=%d,ps=%d,period=%d)\n",
				sequence, step, formatNaiveOp(op), budget, periods, length, weights, catchUp,
				got.charged, errorName(got.err), want.charged, errorName(want.err), reason,
				throttler.spent, throttler.periodSpent, throttler.period)

			if got.charged != want.charged || !errors.Is(got.err, want.err) {
				t.Fatalf("sequence=%d step=%d input=%s B=%d n=%d L=%d w=%v m=%d got=(%d,%v) want=(%d,%v)",
					sequence, step, formatNaiveOp(op), budget, periods, length, weights, catchUp,
					got.charged, got.err, want.charged, want.err)
			}

			if throttler.spent != naive.spent || throttler.periodSpent != naive.ps ||
				throttler.period != naive.period || throttler.maxNow != naive.maxNow ||
				throttler.allowance != naive.allow || throttler.started != naive.started {
				t.Fatalf("sequence=%d state mismatch after %s", sequence, formatNaiveOp(op))
			}
			if throttler.spent < 0 || throttler.spent > throttler.budget {
				t.Fatalf("sequence=%d spent=%d outside [0,%d]", sequence, throttler.spent, throttler.budget)
			}
			if throttler.started {
				if throttler.periodSpent < 0 || throttler.periodSpent > throttler.allowance {
					t.Fatalf("sequence=%d ps=%d outside [0,A=%d]", sequence, throttler.periodSpent, throttler.allowance)
				}
				if op.kind != "allowance" && got.err == nil &&
					throttler.spent > throttler.targets[throttler.period] {
					t.Fatalf("sequence=%d spent=%d > tgt[%d]=%d", sequence, throttler.spent, throttler.period, throttler.targets[throttler.period])
				}
			}
		}
		records = append(records, record)
	}

	for sequence, record := range records {
		replay, err := NewThrottler(record.budget, record.periods, record.length, record.weights, record.catchUp)
		if err != nil {
			t.Fatalf("replay sequence=%d constructor: %v", sequence, err)
		}
		for step, op := range record.ops {
			got, _ := executeAgainstThrottler(replay, op)
			want := record.results[step]
			if got.charged != want.charged || !errors.Is(got.err, want.err) {
				t.Fatalf("replay sequence=%d step=%d input=%s got=(%d,%v) want=(%d,%v)",
					sequence, step, formatNaiveOp(op), got.charged, got.err, want.charged, want.err)
			}
			fmt.Fprintf(logFile, "replay sequence=%d step=%d input=%s output=(charged=%d,err=%v)\n",
				sequence, step, formatNaiveOp(op), got.charged, errorName(got.err))
		}
	}
}

func errorName(err error) string {
	if err == nil {
		return "nil"
	}
	return err.Error()
}

func randomNaiveOp(t *testing.T, rng *rand.Rand, periods int, length int64) naiveOp {
	t.Helper()
	kindRoll := rng.IntN(100)
	var kind string
	switch {
	case kindRoll < 40:
		kind = "try"
	case kindRoll < 65:
		kind = "upto"
	case kindRoll < 85:
		kind = "refund"
	default:
		kind = "allowance"
	}

	var now int64
	if rng.IntN(8) == 0 {
		now = int64(rng.IntN(periods)) * length
		if rng.IntN(2) == 0 && now > 0 {
			now--
		}
	} else {
		now = int64(rng.IntN(periods)) * length
		if length > 1 {
			now += int64(rng.IntN(int(length)))
		}
	}
	if rng.IntN(10) == 0 {
		now = int64(rng.IntN(periods)) * length
	}

	amount := int64(1 + rng.IntN(220))
	if rng.IntN(10) == 0 {
		amount = 1_000_000_000_001
	}
	if rng.IntN(10) == 0 {
		now = int64(periods) * length
	}
	if rng.IntN(10) == 0 {
		now = -1
	}

	return naiveOp{kind: kind, amount: amount, now: now}
}

func executeAgainstThrottler(throttler *Throttler, op naiveOp) (naiveResult, string) {
	switch op.kind {
	case "try":
		err := throttler.Try(op.amount, op.now)
		return naiveResult{err: err}, resultReason(err)
	case "upto":
		charged, err := throttler.TryUpTo(op.amount, op.now)
		return naiveResult{charged: charged, err: err}, resultReason(err)
	case "refund":
		err := throttler.Refund(op.amount, op.now)
		return naiveResult{err: err}, resultReason(err)
	case "allowance":
		charged, err := throttler.Allowance(op.now)
		return naiveResult{charged: charged, err: err}, resultReason(err)
	default:
		return naiveResult{err: ErrInvalidArgument}, "unknown operation"
	}
}

func resultReason(err error) string {
	switch {
	case errors.Is(err, ErrInvalidArgument):
		return "invalid argument"
	case errors.Is(err, ErrClockRolledBack):
		return "clock rollback"
	case errors.Is(err, ErrBudgetExhausted):
		return "daily budget exhausted"
	case errors.Is(err, ErrAmountTooLarge):
		return "single amount exceeds period allowance"
	case errors.Is(err, ErrRateLimited):
		return "remaining period allowance is insufficient"
	case errors.Is(err, ErrRefundTooLarge):
		return "refund exceeds spent"
	case err == nil:
		return "accepted"
	default:
		return err.Error()
	}
}

func formatNaiveOp(op naiveOp) string {
	return fmt.Sprintf("%s(amount=%d,now=%d)", op.kind, op.amount, op.now)
}
