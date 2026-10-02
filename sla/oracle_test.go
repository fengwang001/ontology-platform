package sla

import (
	"fmt"
	"math/rand"
	"testing"
)

type randomOperation struct {
	name  string
	start int
	end   int
	fee   int64
}

type randomMonth struct {
	fee      int64
	reports  []randomOperation
	excludes []randomOperation
	revokes  []randomOperation
}

type randomOracle struct {
	config Config
	months []randomMonth
	open   bool
	streak int
	paid   map[int]int64
	month  int
}

func TestRandomSequencesAgainstMinuteOracle(t *testing.T) {
	rng := rand.New(rand.NewSource(20261002))

	for iteration := 0; iteration < 2000; iteration++ {
		config := randomOracleConfig(rng)
		settlement, err := New(config)
		if err != nil {
			t.Fatalf("iteration %d New(%+v): %v", iteration, config, err)
		}
		oracle := newRandomOracle(config)

		operations := make([]randomOperation, 0, 80)
		monthCount := 1 + rng.Intn(24)
		for month := 0; month < monthCount; month++ {
			fee := int64(1 + rng.Intn(10_000))
			operations = append(operations, randomOperation{name: "NewMonth", fee: fee})
			operationCount := rng.Intn(10)
			for i := 0; i < operationCount; i++ {
				operations = append(operations, randomOperationFor(rng, config.LengthMinutes))
			}
			operations = append(operations, randomOperation{name: "Close"})
		}

		t.Logf("iteration=%d config=%+v operations=%v", iteration, config, operations)
		for stepIndex, operation := range operations {
			actualError := applyOperation(settlement, operation)
			oracleError := oracle.apply(operation)
			if !sameSentinel(actualError, oracleError) {
				t.Fatalf("iteration=%d step=%d operation=%+v actual error=%v oracle error=%v",
					iteration, stepIndex, operation, actualError, oracleError)
			}

			if operation.name == "Close" && actualError == nil && oracleError == nil {
				actual, closeError := settlement.Close()
				expected, oracleCloseError := oracle.close()
				if closeError != nil || oracleCloseError != nil {
					t.Fatalf("iteration=%d duplicated close state: actual=%v oracle=%v",
						iteration, closeError, oracleCloseError)
				}
				if actual != expected {
					t.Fatalf("iteration=%d close actual=%+v oracle=%+v",
						iteration, actual, expected)
				}
				year := oracle.month / 12
				t.Logf("iteration=%d month=%d operation=%+v result=%+v basis=merged-failures then exclusion-union then jitter D=%d floor((L-D)*1e6/L)=%d base-from-tier base=%d escalation=%d annual-credit=%d credit-after-cap=%d",
					iteration, oracle.month-1, operation, actual, actual.DowntimeMinutes,
					actual.Availability, actual.BasePercent, actual.AppliedPercent,
					oracle.paid[year]-actual.Credit, actual.Credit)
			}
		}

		if settlement.streak != oracle.streak || settlement.month != oracle.month {
			t.Fatalf("iteration=%d state actual(month=%d streak=%d) oracle(month=%d streak=%d)",
				iteration, settlement.month, settlement.streak, oracle.month, oracle.streak)
		}
		for year, expectedPaid := range oracle.paid {
			if settlement.annualPaidCredits[year] != expectedPaid {
				t.Fatalf("iteration=%d year=%d paid actual=%d oracle=%d",
					iteration, year, settlement.annualPaidCredits[year], expectedPaid)
			}
			if expectedPaid > config.AnnualCreditLimit {
				t.Fatalf("iteration=%d year=%d paid %d exceeds Y=%d",
					iteration, year, expectedPaid, config.AnnualCreditLimit)
			}
		}
	}
}

func randomOracleConfig(rng *rand.Rand) Config {
	length := 1 + rng.Intn(80)
	tier1 := 500000 + rng.Intn(500001)
	tier2 := 250000 + rng.Intn(tier1-250000)
	tier3 := 1 + rng.Intn(tier2-1)
	credit1 := 1 + rng.Intn(33)
	credit2 := credit1 + 1 + rng.Intn(33)
	credit3 := credit2 + 1 + rng.Intn(100-credit2)

	return Config{
		LengthMinutes:       length,
		JitterMinutes:       rng.Intn(length + 1),
		Tier1Availability:   int64(tier1),
		Tier2Availability:   int64(tier2),
		Tier3Availability:   int64(tier3),
		Tier1Credit:         credit1,
		Tier2Credit:         credit2,
		Tier3Credit:         credit3,
		EscalationStep:      rng.Intn(101),
		EscalationCapMonths: rng.Intn(13),
		AnnualCreditLimit:   int64(rng.Intn(3001)),
		MonthlyExcludeLimit: rng.Intn(length + 1),
	}
}

func randomOperationFor(rng *rand.Rand, length int) randomOperation {
	start := rng.Intn(length)
	end := start + 1 + rng.Intn(length-start)

	switch rng.Intn(4) {
	case 0:
		if rng.Intn(5) == 0 {
			return randomOperation{name: "Report", start: length, end: length}
		}
		return randomOperation{name: "Report", start: start, end: end}
	case 1:
		if rng.Intn(5) == 0 {
			return randomOperation{name: "AddExclude", start: -1, end: end}
		}
		return randomOperation{name: "AddExclude", start: start, end: end}
	case 2:
		return randomOperation{name: "Revoke", start: start, end: end}
	default:
		return randomOperation{name: "Report", start: start, end: end}
	}
}

func newRandomOracle(config Config) *randomOracle {
	return &randomOracle{
		config: config,
		paid:   make(map[int]int64),
	}
}

func (o *randomOracle) apply(operation randomOperation) error {
	valid := operation.start >= 0 && operation.start < operation.end &&
		operation.end <= o.config.LengthMinutes

	switch operation.name {
	case "NewMonth":
		if operation.fee < 1 || operation.fee > 1_000_000_000_000 {
			return ErrInvalidArgument
		}
		if o.open {
			return ErrMonthAlreadyOpen
		}
		o.open = true
		o.months = append(o.months, randomMonth{fee: operation.fee})
		return nil
	case "Report":
		if !valid {
			return ErrInvalidArgument
		}
		if !o.open {
			return ErrNoOpenMonth
		}
		month := &o.months[len(o.months)-1]
		month.reports = append(month.reports, operation)
		return nil
	case "AddExclude":
		if !valid {
			return ErrInvalidArgument
		}
		if !o.open {
			return ErrNoOpenMonth
		}
		month := &o.months[len(o.months)-1]
		candidate := append(append([]randomOperation(nil), month.excludes...), operation)
		if o.unionLength(candidate) > o.config.MonthlyExcludeLimit {
			return ErrExcludeLimitExceeded
		}
		month.excludes = candidate
		return nil
	case "Revoke":
		if !valid {
			return ErrInvalidArgument
		}
		if !o.open {
			return ErrNoOpenMonth
		}
		month := &o.months[len(o.months)-1]
		for i, reported := range month.reports {
			if reported.start == operation.start && reported.end == operation.end {
				month.reports = append(month.reports[:i], month.reports[i+1:]...)
				month.revokes = append(month.revokes, operation)
				return nil
			}
		}
		return ErrNotFound
	case "Close":
		if !o.open {
			return ErrNoOpenMonth
		}
		return nil
	default:
		return fmt.Errorf("unknown operation %q", operation.name)
	}
}

func (o *randomOracle) close() (CloseResult, error) {
	if !o.open {
		return CloseResult{}, ErrNoOpenMonth
	}

	month := o.months[len(o.months)-1]
	failedMinutes := make([]bool, o.config.LengthMinutes)
	excludedMinutes := make([]bool, o.config.LengthMinutes)

	for _, failure := range month.reports {
		for minute := failure.start; minute < failure.end; minute++ {
			failedMinutes[minute] = true
		}
	}
	for _, excluded := range month.excludes {
		for minute := excluded.start; minute < excluded.end; minute++ {
			excludedMinutes[minute] = true
		}
	}

	downtime := 0
	for start := 0; start < len(failedMinutes); {
		if !failedMinutes[start] || excludedMinutes[start] {
			start++
			continue
		}
		end := start + 1
		for end < len(failedMinutes) && failedMinutes[end] && !excludedMinutes[end] {
			end++
		}
		if end-start >= o.config.JitterMinutes {
			downtime += end - start
		}
		start = end
	}

	availability := int64(o.config.LengthMinutes-downtime) * 1_000_000 /
		int64(o.config.LengthMinutes)
	result := CloseResult{DowntimeMinutes: downtime, Availability: availability}

	switch {
	case availability >= o.config.Tier1Availability:
		result.BasePercent = 0
	case availability >= o.config.Tier2Availability:
		result.BasePercent = o.config.Tier1Credit
	case availability >= o.config.Tier3Availability:
		result.BasePercent = o.config.Tier2Credit
	default:
		result.BasePercent = o.config.Tier3Credit
	}

	year := o.month / 12
	if result.BasePercent == 0 {
		o.streak = 0
	} else {
		escalatedMonths := o.streak
		if escalatedMonths > o.config.EscalationCapMonths {
			escalatedMonths = o.config.EscalationCapMonths
		}
		result.AppliedPercent = result.BasePercent + o.config.EscalationStep*escalatedMonths
		if result.AppliedPercent > 100 {
			result.AppliedPercent = 100
		}

		result.Credit = (month.fee*int64(result.AppliedPercent) + 99) / 100
		remaining := o.config.AnnualCreditLimit - o.paid[year]
		if result.Credit > remaining {
			result.Credit = remaining
		}
		o.paid[year] += result.Credit
		o.streak++
	}

	o.open = false
	o.month++
	return result, nil
}

func (o *randomOracle) unionLength(operations []randomOperation) int {
	marked := make([]bool, o.config.LengthMinutes)
	for _, operation := range operations {
		for minute := operation.start; minute < operation.end; minute++ {
			marked[minute] = true
		}
	}
	total := 0
	for _, covered := range marked {
		if covered {
			total++
		}
	}
	return total
}

func applyOperation(settlement *Settlement, operation randomOperation) error {
	switch operation.name {
	case "NewMonth":
		return settlement.NewMonth(operation.fee)
	case "Report":
		return settlement.Report(operation.start, operation.end)
	case "AddExclude":
		return settlement.AddExclude(operation.start, operation.end)
	case "Revoke":
		return settlement.Revoke(operation.start, operation.end)
	case "Close":
		return nil
	default:
		return fmt.Errorf("unknown operation %q", operation.name)
	}
}

func sameSentinel(actual, expected error) bool {
	return actual == expected
}
