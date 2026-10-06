package staffingtest

import (
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"testing"

	"ontology/staffing"
)

func resultEqual(a, b Result) bool {
	if a.OK != b.OK || a.Code != b.Code || a.Index != b.Index {
		return false
	}
	if a.OK {
		if len(a.IDs) != len(b.IDs) {
			return false
		}
		for i := range a.IDs {
			if a.IDs[i] != b.IDs[i] {
				return false
			}
		}
	}
	return true
}

// TestNaiveDifferential：大量随机操作序列与独立朴素模型逐步对照。
// 环境变量 STAFFING_LOG=path 时把每步输入/输出/判定依据写入文件。
func TestNaiveDifferential(t *testing.T) {
	logPath := os.Getenv("STAFFING_LOG")
	var logf *os.File
	if logPath != "" {
		f, err := os.Create(logPath)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		logf = f
	}

	const trials = 400
	const opsPerTrial = 150
	for trial := 0; trial < trials; trial++ {
		rng := rand.New(rand.NewSource(int64(9000 + trial)))
		cooldown := 1 + rng.Intn(6)
		grace := rng.Intn(4)
		_, ops := Generate(rand.New(rand.NewSource(int64(9000+trial))), opsPerTrial)

		svc := staffing.New(cooldown, grace)
		seq := 0
		svc.SetLogger(func(l staffing.StepLog) {
			if logf == nil {
				return
			}
			fmt.Fprintf(logf, "trial=%d [%d] %s ok=%v code=%s\n   in : %s\n   out: %s\n   why: %s\n",
				trial, l.Seq, l.Op, l.OK, l.Code, l.Input, l.Output, l.Reason)
		})
		model := NewModel(cooldown, grace)

		for step, op := range ops {
			seq++
			mr := model.Apply(op)
			sr := RunOp(svc, op)
			if logf != nil {
				fmt.Fprintf(logf, "trial=%d step=%d INPUT %s\n   -> model=%+v service=%+v\n",
					trial, step, OpString(op), mr, sr)
			}
			if !resultEqual(mr, sr) {
				t.Fatalf("trial %d step %d result mismatch\nop=%s\nmodel =%+v\nsvc   =%+v",
					trial, step, OpString(op), mr, sr)
			}

			// 每一步后比较完整状态（纯读取，不触发结算）。
			snap, err := svc.Snapshot(model.now)
			if err != nil {
				t.Fatalf("snapshot: %v", err)
			}
			if err := staffing.CheckInvariant(snap); err != nil {
				t.Fatalf("trial %d step %d invariant: %v", trial, step, err)
			}
			sv := ServiceView(snap)
			mv := model.View()
			if !reflect.DeepEqual(sv, mv) {
				t.Fatalf("trial %d step %d STATE mismatch\nop=%s\nmodel=%#v\nsvc=%#v",
					trial, step, OpString(op), mv, sv)
			}
		}
	}
}
