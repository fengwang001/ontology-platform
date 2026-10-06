package qc

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"

	"ontology/naive"
)

type testOperation struct {
	name       string
	now        int64
	instrument string
	assay      string
	reportID   string
	lowValue   int64
	highValue  int64
	targetLow  int64
	targetHigh int64
	sdLow      int64
	sdHigh     int64
	validFor   int64
}

func TestRandomComparisonAgainstNaiveModel(t *testing.T) {
	for sequence := 0; sequence < 1500; sequence++ {
		rng := rand.New(rand.NewSource(int64(sequence)*1_000_003 + 41))
		actual := NewSystem()
		reference := naive.New()
		instrument := "instrument"
		assay := fmt.Sprintf("assay-%d", sequence%5)
		lastNow := int64(0)
		reportCount := 0
		initialConfig := AssayConfig{
			Low:      LevelConfig{Target: rng.Int63n(41) - 20, SD: rng.Int63n(8) + 1},
			High:     LevelConfig{Target: rng.Int63n(41) - 20, SD: rng.Int63n(8) + 1},
			ValidFor: rng.Int63n(31),
		}

		if err := actual.RegisterAssay(0, instrument, assay, initialConfig); err != nil {
			t.Fatalf("sequence %d: initial registration failed: %v", sequence, err)
		}
		if err := reference.Register(0, instrument, assay, naive.Config{
			Low:      naive.Level{Target: initialConfig.Low.Target, SD: initialConfig.Low.SD},
			High:     naive.Level{Target: initialConfig.High.Target, SD: initialConfig.High.SD},
			ValidFor: initialConfig.ValidFor,
		}); err != nil {
			t.Fatalf("sequence %d: reference registration failed: %v", sequence, err)
		}

		steps := 80
		for step := 0; step < steps; step++ {
			if rng.Intn(10) == 0 {
				lastNow += rng.Int63n(5)
			}
			op := testOperation{
				name:       []string{"run", "run", "run", "issue", "review", "calibrate"}[rng.Intn(6)],
				now:        lastNow,
				instrument: instrument,
				assay:      assay,
				lowValue:   rng.Int63n(81) - 40,
				highValue:  rng.Int63n(81) - 40,
				targetLow:  rng.Int63n(41) - 20,
				targetHigh: rng.Int63n(41) - 20,
				sdLow:      rng.Int63n(8) + 1,
				sdHigh:     rng.Int63n(8) + 1,
				validFor:   rng.Int63n(31),
			}
			if rng.Intn(12) == 0 {
				op.now = lastNow - 1 - rng.Int63n(3)
			}
			if rng.Intn(12) == 0 {
				op.instrument = ""
			}
			if rng.Intn(12) == 0 {
				op.assay = "missing"
			}

			var actualErr error
			var actualRecord QCRecord
			var referenceErr error
			var referenceRecord naive.Record

			switch op.name {
			case "run":
				actualRecord, actualErr = actual.RunQC(op.now, op.instrument, op.assay, op.lowValue, op.highValue)
				referenceRecord, referenceErr = reference.RunQC(op.now, op.instrument, op.assay, op.lowValue, op.highValue)
			case "issue":
				op.reportID = fmt.Sprintf("r-%d-%d", sequence, reportCount)
				if rng.Intn(8) == 0 && reportCount > 0 {
					op.reportID = fmt.Sprintf("r-%d-0", sequence)
				}
				reportCount++
				actualErr = actual.IssueReport(op.now, op.instrument, op.assay, op.reportID)
				referenceErr = reference.IssueReport(op.now, op.instrument, op.assay, op.reportID)
			case "review":
				op.reportID = fmt.Sprintf("r-%d-%d", sequence, rng.Intn(maxInt(reportCount, 1)))
				actualErr = actual.ReviewReport(op.now, op.reportID)
				referenceErr = reference.ReviewReport(op.now, op.reportID)
			case "calibrate":
				actualErr = actual.Calibrate(op.now, op.instrument, op.assay)
				referenceErr = reference.Calibrate(op.now, op.instrument, op.assay)
			}

			t.Logf("sequence=%d step=%d op=%+v actual_record=%+v actual_err=%v reference_record=%+v reference_err=%v",
				sequence, step, op, actualRecord, actualErr, referenceRecord, referenceErr)
			assertSameError(t, sequence, step, actualErr, referenceErr)
			if actualErr == nil && op.name == "run" {
				if actualRecord.Outcome != RunOutcome(referenceRecord.Outcome) {
					t.Fatalf("sequence %d step %d: outcome %s != %s", sequence, step, actualRecord.Outcome, referenceRecord.Outcome)
				}
				if fmt.Sprint(actualRecord.Triggered) != fmt.Sprint(referenceRecord.Rules) {
					t.Fatalf("sequence %d step %d: rules %v != %v", sequence, step, actualRecord.Triggered, referenceRecord.Rules)
				}
			}
			for reportIndex := 0; reportIndex < reportCount; reportIndex++ {
				id := fmt.Sprintf("r-%d-%d", sequence, reportIndex)
				actualStatus, actualStatusErr := actual.ReportStatus(id)
				referenceStatus, referenceExists := reference.ReportStatus(id)
				if referenceExists {
					if actualStatusErr != nil || actualStatus != ReportStatus(referenceStatus) {
						t.Fatalf("sequence %d step %d report %s: %s/%v != %s", sequence, step, id, actualStatus, actualStatusErr, referenceStatus)
					}
				} else if actualStatusErr == nil {
					t.Fatalf("sequence %d step %d report %s unexpectedly exists: %s", sequence, step, id, actualStatus)
				}
			}

			if op.now >= 0 && op.now <= 1_000_000_000 && op.now >= lastNow && actualErr == nil {
				lastNow = op.now
			}
		}
	}
}

func assertSameError(t *testing.T, sequence, step int, actual error, reference error) {
	t.Helper()
	var actualTyped Error
	if errors.As(actual, &actualTyped) {
		if string(actualTyped.Code) != reference.Error() {
			t.Fatalf("sequence %d step %d: error %s != %v", sequence, step, actualTyped.Code, reference)
		}
	} else if actual != nil || reference != nil {
		t.Fatalf("sequence %d step %d: error %v != %v", sequence, step, actual, reference)
	}
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}
