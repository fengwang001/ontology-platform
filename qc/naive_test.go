package qc

import (
	"math/rand"
	"reflect"
	"testing"
)

type randomOp struct {
	kind      string
	now       int64
	lowValue  int64
	highValue int64
	reportID  string
}

func TestRandomSequencesAgainstNaiveModel(t *testing.T) {
	values := []int64{-32, -31, -21, -20, -11, -10, -1, 0, 1, 10, 11, 20, 21, 31, 32}

	for sequence := 0; sequence < 1500; sequence++ {
		random := rand.New(rand.NewSource(int64(sequence + 1)))
		system := NewSystem()
		naive := newNaiveSystem()
		spec := AssaySpec{
			InstrumentID: "instrument-a",
			AssayID:      "glucose",
			Low:          LevelSpec{Target: 0, StandardDeviation: 10},
			High:         LevelSpec{Target: 100, StandardDeviation: 5},
			Validity:     20,
		}
		if err := system.RegisterAssay(0, spec); err != nil {
			t.Fatalf("sequence %d register: %v", sequence, err)
		}
		if err := naive.registerAssay(0, spec); err != nil {
			t.Fatalf("sequence %d naive register: %v", sequence, err)
		}

		var clock int64
		var reportCount int
		for step := 0; step < 60; step++ {
			clock += int64(random.Intn(8))
			op := randomOp{kind: "run", now: clock}
			switch random.Intn(100) {
			case 0, 1, 2, 3, 4:
				op.kind = "calibrate"
			case 5, 6, 7, 8, 9, 10:
				op.kind = "review"
			case 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32, 33, 34:
				op.kind = "issue"
			}
			if random.Intn(10) == 0 {
				op.now--
			}

			switch op.kind {
			case "run":
				op.lowValue = values[random.Intn(len(values))]
				op.highValue = 100 + values[random.Intn(len(values))]
				input := RunInput{InstrumentID: spec.InstrumentID, AssayID: spec.AssayID, LowValue: op.lowValue, HighValue: op.highValue}
				got, gotErr := system.SubmitRun(op.now, input)
				want, wantErr := naive.submitRun(op.now, input)
				t.Logf("seq=%d step=%d input=run now=%d low=%d high=%d output=%+v err=%v basis=lowDeviation=%d highDeviation=%d rules=%v warning=%t stateOutOfControl=%t",
					sequence, step, op.now, op.lowValue, op.highValue, got, gotErr, got.Low.Deviation, got.High.Deviation, got.Rules, got.Warning, got.Outage)
				if !sameError(gotErr, wantErr) || !reflect.DeepEqual(got, want) {
					t.Fatalf("run mismatch:\ninput=%+v\ngot=(%+v,%v)\nwant=(%+v,%v)", input, got, gotErr, want, wantErr)
				}
			case "issue":
				op.reportID = uniqueReportID(&reportCount)
				gotErr := system.IssueReport(op.now, op.reportID, spec.InstrumentID, spec.AssayID)
				wantErr := naive.issueReport(op.now, op.reportID, spec.InstrumentID, spec.AssayID)
				t.Logf("seq=%d step=%d input=issue now=%d report=%s output_err=%v naive_err=%v basis=reportAccepted=%t",
					sequence, step, op.now, op.reportID, gotErr, wantErr, gotErr == nil)
				if !sameError(gotErr, wantErr) {
					t.Fatalf("issue mismatch: got=%v want=%v", gotErr, wantErr)
				}
			case "calibrate":
				gotErr := system.Calibrate(op.now, spec.InstrumentID, spec.AssayID)
				wantErr := naive.calibrate(op.now, spec.InstrumentID, spec.AssayID)
				t.Logf("seq=%d step=%d input=calibrate now=%d output_err=%v naive_err=%v basis=clearsSequencesAndRestoresControl",
					sequence, step, op.now, gotErr, wantErr)
				if !sameError(gotErr, wantErr) {
					t.Fatalf("calibrate mismatch: got=%v want=%v", gotErr, wantErr)
				}
			case "review":
				op.reportID = "r" + itoa(random.Intn(maxInt(1, reportCount)))
				got, gotErr := system.ReviewReport(op.now, op.reportID)
				want, wantErr := naive.reviewReport(op.now, op.reportID)
				t.Logf("seq=%d step=%d input=review now=%d report=%s output=%+v err=%v naive=%+v naiveErr=%v basis=pendingOnly",
					sequence, step, op.now, op.reportID, got, gotErr, want, wantErr)
				if !sameError(gotErr, wantErr) || !reflect.DeepEqual(got, want) {
					t.Fatalf("review mismatch: got=(%+v,%v) want=(%+v,%v)", got, gotErr, want, wantErr)
				}
			}

			compareAllReportStatuses(t, system, naive)
		}
	}
}

func uniqueReportID(counter *int) string {
	id := "r" + itoa(*counter)
	*counter++
	return id
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	negative := value < 0
	if negative {
		value = -value
	}
	var digits []byte
	for value > 0 {
		digits = append([]byte{byte('0' + value%10)}, digits...)
		value /= 10
	}
	if negative {
		digits = append([]byte{'-'}, digits...)
	}
	return string(digits)
}

func sameError(got, want error) bool {
	return got == want
}

func compareAllReportStatuses(t *testing.T, system *System, naive *naiveSystem) {
	t.Helper()
	for id, want := range naive.reports {
		got, err := system.ReportStatus(id)
		if err != nil || got != want.Status {
			t.Fatalf("report %s status mismatch: got=(%q,%v), want=%q", id, got, err, want.Status)
		}
	}
}
