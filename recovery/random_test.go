package recovery_test

import (
	"fmt"
	"math/rand"
	"testing"

	"ontology/recovery"
)

type op struct {
	name string
	a, b int
	d    int64
}

func (o op) String() string {
	switch o.name {
	case "Update":
		return fmt.Sprintf("Update(%d,%d,%d)", o.a, o.b, o.d)
	case "Rollback":
		return fmt.Sprintf("Rollback(%d,%d)", o.a, o.b)
	case "RestartStep":
		return fmt.Sprintf("RestartStep(%d)", o.a)
	default:
		return fmt.Sprintf("%s(%d)", o.name, o.a)
	}
}

func applyEngine(e *recovery.Engine, o op) (int, error) {
	switch o.name {
	case "Begin":
		return 0, e.Begin(o.a)
	case "Update":
		return e.Update(o.a, o.b, o.d)
	case "Save":
		return e.Save(o.a)
	case "Commit":
		return 0, e.Commit(o.a)
	case "Rollback":
		return e.Rollback(o.a, o.b)
	case "Abort":
		return e.Abort(o.a)
	case "Crash":
		return 0, e.Crash()
	case "Restart":
		return e.Restart()
	default:
		return e.RestartStep(o.a)
	}
}

func applyNaive(m *naive, o op) (int, error) {
	switch o.name {
	case "Begin":
		return 0, m.Begin(o.a)
	case "Update":
		return m.Update(o.a, o.b, o.d)
	case "Save":
		return m.Save(o.a)
	case "Commit":
		return 0, m.Commit(o.a)
	case "Rollback":
		return m.Rollback(o.a, o.b)
	case "Abort":
		return m.Abort(o.a)
	case "Crash":
		return 0, m.Crash()
	case "Restart":
		return m.Restart()
	default:
		return m.RestartStep(o.a)
	}
}

func genTxn(rng *rand.Rand) int {
	if rng.Intn(100) < 12 {
		bad := []int{0, -1, 5, 6, 7, recovery.MaxTxnID, recovery.MaxTxnID + 1}
		return bad[rng.Intn(len(bad))]
	}
	return 1 + rng.Intn(4)
}

func genPage(rng *rand.Rand) int {
	if rng.Intn(100) < 5 {
		bad := []int{-1, recovery.MaxPageID + 1}
		return bad[rng.Intn(len(bad))]
	}
	return rng.Intn(4)
}

func genDelta(rng *rand.Rand) int64 {
	if rng.Intn(100) < 5 {
		bad := []int64{0, recovery.MaxDelta + 1, -recovery.MaxDelta - 1}
		return bad[rng.Intn(len(bad))]
	}
	d := int64(1 + rng.Intn(recovery.MaxDelta))
	if rng.Intn(2) == 0 {
		return -d
	}
	return d
}

func genOp(rng *rand.Rand, saves map[int][]int, logLen int) op {
	switch w := rng.Intn(100); {
	case w < 14:
		return op{name: "Begin", a: genTxn(rng)}
	case w < 48:
		return op{name: "Update", a: genTxn(rng), b: genPage(rng), d: genDelta(rng)}
	case w < 56:
		return op{name: "Save", a: genTxn(rng)}
	case w < 63:
		return op{name: "Commit", a: genTxn(rng)}
	case w < 70:
		return op{name: "Abort", a: genTxn(rng)}
	case w < 84:
		id := genTxn(rng)
		var s int
		switch rng.Intn(10) {
		case 0, 1, 2:
			s = 0
		case 3, 4, 5:
			if ss := saves[id]; len(ss) > 0 {
				s = ss[rng.Intn(len(ss))]
			}
		case 6:
			s = -1 - rng.Intn(3)
		default:
			s = rng.Intn(logLen + 3)
		}
		return op{name: "Rollback", a: id, b: s}
	case w < 89:
		return op{name: "Crash"}
	case w < 94:
		return op{name: "Restart"}
	default:
		n := 1 + rng.Intn(5)
		if rng.Intn(100) < 8 {
			n = -1 + rng.Intn(2) // 0 or -1: invalid
		}
		return op{name: "RestartStep", a: n}
	}
}

func sameErr(a, b error) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Error() == b.Error()
}

func checkInvariants(t *testing.T, e *recovery.Engine, log []recovery.Record) {
	t.Helper()
	sums := map[int]int64{}
	for _, r := range log {
		if r.Type == recovery.RecCompensation && r.UndoNext >= r.LSN {
			t.Fatalf("invariant violated: C at LSN %d has undoNext %d", r.LSN, r.UndoNext)
		}
		if r.Type == recovery.RecUpdate || r.Type == recovery.RecCompensation {
			sums[r.Page] += r.Delta
		}
	}
	for p, want := range sums {
		if got := e.Page(p); got != want {
			t.Fatalf("invariant violated: page %d = %d, log deltas sum to %d", p, got, want)
		}
	}
}

// TestRandomSequencesAgainstNaiveModel replays 2000 random operation
// sequences against both the engine and the naive step-by-step simulation
// of the spec, comparing every return value and error, the final log, the
// page values, and a determinism replay of the same sequence.
func TestRandomSequencesAgainstNaiveModel(t *testing.T) {
	const sequences = 2000
	for seq := 0; seq < sequences; seq++ {
		rng := rand.New(rand.NewSource(int64(seq)*7919 + 13))
		lmax := 1 + rng.Intn(40)
		e, err := recovery.NewEngine(lmax)
		if err != nil {
			t.Fatalf("NewEngine(%d): %v", lmax, err)
		}
		replay, err := recovery.NewEngine(lmax)
		if err != nil {
			t.Fatalf("NewEngine(%d): %v", lmax, err)
		}
		m := newNaive(lmax)
		saves := map[int][]int{}
		ops := 30 + rng.Intn(90)
		t.Logf("sequence %d: lmax=%d ops=%d", seq, lmax, ops)
		for i := 0; i < ops; i++ {
			o := genOp(rng, saves, e.LogLen())
			v1, err1 := applyEngine(e, o)
			v2, err2 := applyNaive(m, o)
			applyEngine(replay, o)
			t.Logf("  op %d: %s -> engine=(%d,%v) naive=(%d,%v)", i, o, v1, err1, v2, err2)
			if v1 != v2 || !sameErr(err1, err2) {
				t.Fatalf("sequence %d op %d %s: engine=(%d,%v) naive=(%d,%v)",
					seq, i, o, v1, err1, v2, err2)
			}
			if o.name == "Save" && err1 == nil {
				saves[o.a] = append(saves[o.a], v1)
			}
		}
		engLog := e.Log()
		if len(engLog) != len(m.log) {
			t.Fatalf("sequence %d: engine log %d records, naive log %d records\nengine: %v\nnaive:  %v",
				seq, len(engLog), len(m.log), engLog, m.log)
		}
		for i := range engLog {
			if engLog[i] != m.log[i] {
				t.Fatalf("sequence %d: log record %d differs\nengine: %+v\nnaive:  %+v",
					seq, i+1, engLog[i], m.log[i])
			}
		}
		replayLog := replay.Log()
		if len(replayLog) != len(engLog) {
			t.Fatalf("sequence %d: replay diverged (%d vs %d records)", seq, len(replayLog), len(engLog))
		}
		for i := range engLog {
			if replayLog[i] != engLog[i] {
				t.Fatalf("sequence %d: replay record %d = %+v, want %+v", seq, i+1, replayLog[i], engLog[i])
			}
		}
		for p, want := range m.pages {
			if got := e.Page(p); got != want {
				t.Fatalf("sequence %d: page %d = %d (engine), naive computes %d", seq, p, got, want)
			}
		}
		checkInvariants(t, e, engLog)
		t.Logf("sequence %d OK: %d ops, %d log records, logs identical, pages identical, replay identical",
			seq, ops, len(engLog))
	}
}
