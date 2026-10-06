package qc

import (
	"fmt"
	"testing"
	"time"
)

func TestScalingRuleEvaluation(t *testing.T) {
	small := measureRuleEvaluation(1_000)
	large := measureRuleEvaluation(100_000)
	ratio := float64(large) / float64(small)
	if ratio > 3 {
		t.Fatalf("rule evaluation scaled with history: small=%v large=%v ratio=%.2f", small, large, ratio)
	}
	t.Logf("rule evaluation uses fixed counters: history=%d duration=%v", 1_000, small)
	t.Logf("rule evaluation uses fixed counters: history=%d duration=%v ratio=%.2f", 100_000, large, ratio)
}

func TestScalingRetrospectiveMarking(t *testing.T) {
	for _, size := range []int{1_000, 100_000} {
		state := buildReportsForTracing(size)
		start := time.Now()
		markReportsForReview(state, 1_000_000)
		elapsed := time.Since(start)
		marked := 0
		for _, patientReport := range state.reports {
			if patientReport.status == ReportPending {
				marked++
			}
		}
		if marked != size {
			t.Fatalf("marked = %d, want %d", marked, size)
		}
		t.Logf("retrospective marking touches only candidate reports: reports=%d marked=%d duration=%v per_report=%v",
			size, marked, elapsed, elapsed/time.Duration(size))
	}
}

func measureRuleEvaluation(history int) time.Duration {
	state := &assayState{
		lowCfg:  levelConfig{target: 100, sd: 10},
		highCfg: levelConfig{target: 50, sd: 5},
	}
	state.low = levelState{lastSign: 1, sideStreak: history, overOneStreak: history, overTwoStreak: history}
	const iterations = 200_000
	start := time.Now()
	for index := 0; index < iterations; index++ {
		probe := *state
		rules, outcome := evaluateRun(&probe, 111, 50)
		if outcome != OutcomeReject || len(rules) == 0 {
			panic(fmt.Sprintf("expected rule trigger: %+v %s", rules, outcome))
		}
	}
	return time.Since(start) / iterations
}

func buildReportsForTracing(size int) *assayState {
	state := &assayState{reports: make([]*patientReport, 0, size)}
	for index := 0; index < size; index++ {
		state.reports = append(state.reports, &patientReport{
			id:       fmt.Sprintf("report-%d", index),
			issuedAt: int64(index + 1),
			status:   ReportIssued,
		})
	}
	return state
}
