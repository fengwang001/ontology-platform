package replay

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"

	"ontology/code"
	"ontology/history"
)

// naiveResult 是按题目规则逐行翻译的朴素模拟结果（独立于实现）。
type naiveResult struct {
	consumed int
	cont     []history.Event
	err      error // 三类非确定性错误之一
}

// naiveRun 完全按规格逐步模拟：c 为游标，P 为本次补丁 pid 集合。
func naiveRun(program code.Code, hist []history.Event) naiveResult {
	c := 0
	L := len(hist)
	P := map[string]bool{}
	cont := []history.Event{}

	var runSteps func(items []code.Item) error
	runSteps = func(items []code.Item) error {
		for _, it := range items {
			if c < L {
				e := hist[c]
				if e.IsMarker() {
					return &IndexError{kind: ErrUnexpectedMarker, index: c}
				}
				if string(e.Name) != string(it.Name) {
					return &IndexError{kind: ErrMismatch, index: c}
				}
				c++
			} else {
				cont = append(cont, history.Step(it.Name))
			}
		}
		return nil
	}

	for _, it := range program {
		var err error
		if it.IsStep() {
			err = runSteps([]code.Item{it})
		} else {
			pid := string(it.Pid)
			switch {
			case P[pid]:
				err = runSteps(it.New)
			case c < L && hist[c].IsMarker() && string(hist[c].Pid) == pid:
				c++
				P[pid] = true
				err = runSteps(it.New)
			case c < L && hist[c].IsMarker():
				err = &IndexError{kind: ErrUnexpectedMarker, index: c}
			case c < L:
				err = runSteps(it.Old)
			default:
				cont = append(cont, history.Marker(it.Pid))
				P[pid] = true
				err = runSteps(it.New)
			}
		}
		if err != nil {
			return naiveResult{err: err}
		}
	}
	if c < L {
		return naiveResult{err: &IndexError{kind: ErrHistoryExtra, index: c}}
	}
	return naiveResult{consumed: c, cont: cont}
}

// genCode 生成结构合法的随机代码：Step 与 Branch（N/O 只含 Step，可空）。
func genCode(rng *rand.Rand, alphabet []string) code.Code {
	n := 1 + rng.Intn(6)
	prog := make(code.Code, n)
	usedPid := map[string]bool{}
	for i := range prog {
		if rng.Intn(2) == 0 {
			prog[i] = code.Step([]byte(alphabet[rng.Intn(len(alphabet))]))
			continue
		}
		pid := fmt.Sprintf("p%d", rng.Intn(3)) // 刻意制造重复 pid，覆盖 P 集合
		usedPid[pid] = true
		mkSteps := func() []code.Item {
			m := rng.Intn(3)
			out := make([]code.Item, m)
			for j := range out {
				out[j] = code.Step([]byte(alphabet[rng.Intn(len(alphabet))]))
			}
			return out
		}
		prog[i] = code.Branch([]byte(pid), mkSteps(), mkSteps())
	}
	_ = usedPid
	return prog
}

// genHistory 生成随机历史，其中一部分刻意做成“非法/不匹配”历史
// （乱入的标记、不同 name 的 Step），以覆盖三类错误。
func genHistory(rng *rand.Rand, alphabet []string) []history.Event {
	n := rng.Intn(8)
	out := make([]history.Event, n)
	for i := range out {
		switch rng.Intn(3) {
		case 0:
			out[i] = history.Marker([]byte(fmt.Sprintf("p%d", rng.Intn(4))))
		case 1:
			out[i] = history.Step([]byte("junk" + alphabet[rng.Intn(len(alphabet))]))
		default:
			out[i] = history.Step([]byte(alphabet[rng.Intn(len(alphabet))]))
		}
	}
	return out
}

func TestNaiveDifferential(t *testing.T) {
	const iterations = 4000
	rng := rand.New(rand.NewSource(20261003))
	alphabet := []string{"a", "b", "c", "d"}

	for iter := 0; iter < iterations; iter++ {
		prog := genCode(rng, alphabet)
		if err := prog.Validate(); err != nil {
			t.Fatalf("generated invalid code: %v", err)
		}
		hist := genHistory(rng, alphabet)
		want := naiveRun(prog, hist)

		store, wf := seed(t, "diff", hist)
		r := NewRunner(store)
		consumed, cont, err := r.Run(wf, prog)

		switch {
		case want.err != nil:
			if !sameErrorKind(want.err, err) {
				t.Fatalf("iter=%d code=%s hist=%s: naive err=%v impl err=%v",
					iter, codeString(prog), evsString(hist), want.err, err)
			}
			if wi, ii := mustIndex(want.err), mustIndex(err); wi != ii {
				t.Fatalf("iter=%d: naive idx=%d impl idx=%d", iter, wi, ii)
			}
			if got := store.Snapshot(wf); len(got) != len(hist) {
				t.Fatalf("iter=%d: failed run changed history", iter)
			}
			t.Logf("iter=%d REJECT code=%s hist=%s -> %v (index %d)",
				iter, codeString(prog), evsString(hist), want.err, mustIndex(err))
		case err != nil:
			t.Fatalf("iter=%d code=%s hist=%s: impl unexpected err=%v",
				iter, codeString(prog), evsString(hist), err)
		default:
			if consumed != want.consumed || cont != len(want.cont) {
				t.Fatalf("iter=%d: counts (%d,%d), want (%d,%d)",
					iter, consumed, cont, want.consumed, len(want.cont))
			}
			final := store.Snapshot(wf)
			mustEqualEvents(t, final, append(append([]history.Event{}, hist...), want.cont...), "diff final")
			t.Logf("iter=%d OK     code=%s hist=%s -> consumed=%d cont=%s",
				iter, codeString(prog), evsString(hist), consumed, evsString(want.cont))
		}
	}
}

func sameErrorKind(a, b error) bool {
	for _, target := range []error{ErrMismatch, ErrUnexpectedMarker, ErrHistoryExtra} {
		if errors.Is(a, target) != errors.Is(b, target) {
			return false
		}
	}
	return true
}

func mustIndex(err error) int {
	i, ok := IndexFrom(err)
	if !ok {
		panic("expected indexed error")
	}
	return i
}
