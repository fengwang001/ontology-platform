package bracket

import (
	"fmt"
	"math/rand"
	"testing"
)

func refSnapshot(t *refTournament) [][]Match {
	out := make([][]Match, t.rounds)
	for r := 0; r < t.rounds; r++ {
		out[r] = make([]Match, len(t.matches[r]))
		for i, m := range t.matches[r] {
			tp := Unresolved
			switch m.typ {
			case refManual:
				tp = Manual
			case refTechnical:
				tp = Technical
			case refBye:
				tp = Bye
			}
			out[r][i] = Match{Round: r + 1, Index: i + 1, Left: m.left, Right: m.right, Winner: m.winner, Type: tp}
		}
	}
	return out
}

// TestExhaustiveInitialBracket compares the production bracket for every N in
// [2, 64] against the naive order(B) implementation, match by match.
func TestExhaustiveInitialBracket(t *testing.T) {
	for n := 2; n <= 64; n++ {
		prod, err := New(n)
		if err != nil {
			t.Fatalf("N=%d: %v", n, err)
		}
		got := prod.Bracket()
		want := refSnapshot(newRef(n))
		if !bracketsEqual(got, want) {
			t.Fatalf("N=%d bracket mismatch:\n got=%+v\nwant=%+v", n, got, want)
		}
		// Seed 1 and seed 2 can only meet in the final: they must occupy the
		// left and right halves of the final, never the same earlier match.
		final := got[len(got)-1][0]
		// Slots may still be empty for large N; verify via folded order halves.
		b := 1
		for b < n {
			b <<= 1
		}
		order := foldedOrder(b)
		var pos1, pos2 int
		for idx, s := range order {
			if s == 1 {
				pos1 = idx
			}
			if s == 2 {
				pos2 = idx
			}
		}
		if pos1/(b/2) == pos2/(b/2) {
			t.Fatalf("N=%d: seeds 1 and 2 in the same half", n)
		}
		_ = final
	}
}

type opKind int

const (
	opReport opKind = iota
	opCorrect
	opWithdraw
)

type op struct {
	kind    opKind
	r, i, w int
}

func (o op) String() string {
	switch o.kind {
	case opReport:
		return fmt.Sprintf("Report(r=%d,i=%d,w=%d)", o.r, o.i, o.w)
	case opCorrect:
		return fmt.Sprintf("Correct(r=%d,i=%d,w=%d)", o.r, o.i, o.w)
	case opWithdraw:
		return fmt.Sprintf("Withdraw(s=%d)", o.w)
	}
	return "?"
}

// apply runs the op against both implementations and returns both errors.
func applyOp(prod *Tournament, ref *refTournament, o op) (error, error) {
	switch o.kind {
	case opReport:
		return prod.Report(o.r, o.i, o.w), ref.report(o.r, o.i, o.w)
	case opCorrect:
		return prod.Correct(o.r, o.i, o.w), ref.correct(o.r, o.i, o.w)
	default:
		return prod.Withdraw(o.w), ref.withdraw(o.w)
	}
}

func randomOp(rng rngSource, n, rounds int) op {
	r := 1 + rng.Intn(rounds)
	size := 1 << uint(rounds-r)
	i := 1 + rng.Intn(size)
	w := 1 + rng.Intn(n)
	switch rng.Intn(3) {
	case 0:
		return op{opReport, r, i, w}
	case 1:
		return op{opCorrect, r, i, w}
	default:
		return op{opWithdraw, 0, 0, w}
	}
}

type rngSource interface {
	Intn(n int) int
}

// TestRandomDifferential replays 2000 random operation sequences against both
// implementations and compares errors and the full bracket tree after every
// op. Each op's input, output and decision basis is logged.
func TestRandomDifferential(t *testing.T) {
	rng := rand.New(rand.NewSource(20261001))
	const trials = 2000
	for trial := 0; trial < trials; trial++ {
		n := 2 + rng.Intn(63)
		b, rounds := 1, 0
		for b < n {
			b <<= 1
			rounds++
		}
		prod, _ := New(n)
		ref := newRef(n)
		steps := rng.Intn(4 * rounds * (b / 2))
		t.Logf("[trial %d] N=%d steps=%d", trial, n, steps)
		for step := 0; step < steps; step++ {
			o := randomOp(rng, n, rounds)
			gotErr, wantErr := applyOp(prod, ref, o)
			gotBracket := prod.Bracket()
			wantBracket := refSnapshot(ref)
			basis := "rejected"
			if gotErr == nil {
				switch o.kind {
				case opReport:
					basis = "manual report; cascade then auto-decided ready matches with withdrawn contestants"
				case opCorrect:
					basis = "manual correction; next-round slot replaced; cascade re-run"
				case opWithdraw:
					basis = "withdrawal recorded; cascade: one withdrawn -> other advances, both -> smaller seed"
				}
			}
			t.Logf("  step %d: %s -> got=%q want=%q basis=%s", step, o, refString(gotErr), refString(wantErr), basis)
			if refString(gotErr) != refString(wantErr) {
				t.Fatalf("[trial %d N=%d] step %d %s error mismatch:\n got=%v\nwant=%v", trial, n, step, o, gotErr, wantErr)
			}
			if !bracketsEqual(gotBracket, wantBracket) {
				t.Fatalf("[trial %d N=%d] step %d %s bracket mismatch:\n got=%+v\nwant=%+v", trial, n, step, o, gotBracket, wantBracket)
			}
			gc, gok := prod.Champion()
			rc, rok := ref.championForTest()
			if gok != rok || gc != rc {
				t.Fatalf("[trial %d] champion mismatch (%d,%v) vs (%d,%v)", trial, gc, gok, rc, rok)
			}
		}
	}
}
