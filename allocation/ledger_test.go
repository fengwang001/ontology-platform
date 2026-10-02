package allocation

import (
	"errors"
	"reflect"
	"testing"
)

func mustLedger(t *testing.T, k, p int) *Ledger {
	t.Helper()
	l, err := NewLedger(k, p)
	if err != nil {
		t.Fatalf("NewLedger(%d, %d): %v", k, p, err)
	}
	return l
}

func mustSetUsage(t *testing.T, l *Ledger, s, r int, u int64) {
	t.Helper()
	if err := l.SetUsage(s, r, u); err != nil {
		t.Fatalf("SetUsage(%d, %d, %d): %v", s, r, u, err)
	}
}

func mustClose(t *testing.T, l *Ledger, ds, dp []int64) *CloseResult {
	t.Helper()
	res, err := l.Close(ds, dp)
	if err != nil {
		t.Fatalf("Close(%v, %v): %v", ds, dp, err)
	}
	return res
}

func stepView(res *CloseResult) [][2]int64 {
	out := make([][2]int64, len(res.Steps))
	for i, st := range res.Steps {
		out[i] = [2]int64{int64(st.Service), st.Total}
	}
	return out
}

func sharesView(res *CloseResult) [][]Share {
	out := make([][]Share, len(res.Steps))
	for i, st := range res.Steps {
		out[i] = st.Shares
	}
	return out
}

// The worked example from the specification.
func TestSpecExample(t *testing.T) {
	l := mustLedger(t, 2, 2)
	mustSetUsage(t, l, 0, 1, 1)
	mustSetUsage(t, l, 0, 2, 1)
	mustSetUsage(t, l, 0, 3, 2)
	mustSetUsage(t, l, 1, 0, 1)
	mustSetUsage(t, l, 1, 2, 3)
	mustSetUsage(t, l, 1, 3, 0)

	res := mustClose(t, l, []int64{101, 60}, []int64{10, 20})

	wantSteps := [][2]int64{{0, 101}, {1, 86}}
	if got := stepView(res); !reflect.DeepEqual(got, wantSteps) {
		t.Fatalf("steps = %v, want %v", got, wantSteps)
	}
	wantShares := [][]Share{
		{{Dept: 1, Amount: 26}, {Dept: 2, Amount: 25}, {Dept: 3, Amount: 50}},
		{{Dept: 2, Amount: 86}},
	}
	if got := sharesView(res); !reflect.DeepEqual(got, wantShares) {
		t.Fatalf("shares = %v, want %v", got, wantShares)
	}
	if want := []int64{121, 70}; !reflect.DeepEqual(res.FullCosts, want) {
		t.Fatalf("full costs = %v, want %v", res.FullCosts, want)
	}
	if want := []int64{0, 26, 111, 50}; !reflect.DeepEqual(l.History(), want) {
		t.Fatalf("H = %v, want %v", l.History(), want)
	}
}

// Equal ratios push the smaller department id first.
func TestRatioTiePrefersSmallerID(t *testing.T) {
	l := mustLedger(t, 3, 1)
	// All three service departments have ratio 1/2.
	mustSetUsage(t, l, 0, 1, 1)
	mustSetUsage(t, l, 0, 3, 1)
	mustSetUsage(t, l, 1, 2, 1)
	mustSetUsage(t, l, 1, 3, 1)
	mustSetUsage(t, l, 2, 0, 1)
	mustSetUsage(t, l, 2, 3, 1)

	res := mustClose(t, l, []int64{2, 2, 2}, []int64{0})
	want := [][2]int64{{0, 2}, {1, 3}, {2, 4}}
	if got := stepView(res); !reflect.DeepEqual(got, want) {
		t.Fatalf("push order = %v, want %v", got, want)
	}
}

// Ratios are recomputed after every push: department 2 looks larger
// than department 1 in the first step, but once department 0 is pushed
// the order between 1 and 2 reverses.
func TestRatioRecomputedAfterPush(t *testing.T) {
	l := mustLedger(t, 3, 1)
	mustSetUsage(t, l, 0, 1, 1)
	mustSetUsage(t, l, 0, 2, 1)
	mustSetUsage(t, l, 1, 0, 1)
	mustSetUsage(t, l, 1, 2, 100)
	mustSetUsage(t, l, 1, 3, 50)
	mustSetUsage(t, l, 2, 0, 100)
	mustSetUsage(t, l, 2, 1, 1)
	mustSetUsage(t, l, 2, 3, 1)
	// Step-1 ratios: d0 = 2/2, d1 = 101/151, d2 = 101/102 -> push 0.
	// Step-2 ratios: d1 = 100/150, d2 = 1/2 -> push 1, then 2.
	res := mustClose(t, l, []int64{2, 300, 101}, []int64{0})

	wantSteps := [][2]int64{{0, 2}, {1, 301}, {2, 302}}
	if got := stepView(res); !reflect.DeepEqual(got, wantSteps) {
		t.Fatalf("steps = %v, want %v", got, wantSteps)
	}
	wantShares := [][]Share{
		{{Dept: 1, Amount: 1}, {Dept: 2, Amount: 1}},
		{{Dept: 2, Amount: 200}, {Dept: 3, Amount: 101}},
		{{Dept: 3, Amount: 302}},
	}
	if got := sharesView(res); !reflect.DeepEqual(got, wantShares) {
		t.Fatalf("shares = %v, want %v", got, wantShares)
	}
	if want := []int64{403}; !reflect.DeepEqual(res.FullCosts, want) {
		t.Fatalf("full costs = %v, want %v", res.FullCosts, want)
	}
}

// The denominator excludes already pushed departments instead of
// reusing the first step's denominator.
func TestDenominatorExcludesPushed(t *testing.T) {
	l := mustLedger(t, 2, 2)
	mustSetUsage(t, l, 0, 1, 5)
	mustSetUsage(t, l, 0, 2, 1)
	mustSetUsage(t, l, 0, 3, 1)
	mustSetUsage(t, l, 1, 0, 6)
	mustSetUsage(t, l, 1, 2, 1)
	mustSetUsage(t, l, 1, 3, 1)
	// Step 1: d0 ratio 5/7, d1 ratio 6/8 -> push 1 first.
	// Step 2: d0's denominator drops u[0][1]=5 (pushed), so b_0 = 2,
	// not the stale 7; with b_0 = 7 the floor split would differ.
	res := mustClose(t, l, []int64{0, 40}, []int64{0, 0})

	wantSteps := [][2]int64{{1, 40}, {0, 30}}
	if got := stepView(res); !reflect.DeepEqual(got, wantSteps) {
		t.Fatalf("steps = %v, want %v", got, wantSteps)
	}
	wantShares := [][]Share{
		{{Dept: 0, Amount: 30}, {Dept: 2, Amount: 5}, {Dept: 3, Amount: 5}},
		{{Dept: 2, Amount: 15}, {Dept: 3, Amount: 15}},
	}
	if got := sharesView(res); !reflect.DeepEqual(got, wantShares) {
		t.Fatalf("shares = %v, want %v", got, wantShares)
	}
	if want := []int64{20, 20}; !reflect.DeepEqual(res.FullCosts, want) {
		t.Fatalf("full costs = %v, want %v", res.FullCosts, want)
	}
}

// A department with b_s == 0 sorts last, and with T_s == 0 it needs no
// recipient at all.
func TestZeroRatioLastAndZeroTotalNeedsNoRecipient(t *testing.T) {
	l := mustLedger(t, 2, 1)
	mustSetUsage(t, l, 0, 2, 1)
	// Department 1 has no usage at all: b_1 = 0.
	res := mustClose(t, l, []int64{10, 0}, []int64{5})

	wantSteps := [][2]int64{{0, 10}, {1, 0}}
	if got := stepView(res); !reflect.DeepEqual(got, wantSteps) {
		t.Fatalf("steps = %v, want %v", got, wantSteps)
	}
	if res.Steps[1].Shares != nil {
		t.Fatalf("zero-total step has shares %v, want none", res.Steps[1].Shares)
	}
	if want := []int64{15}; !reflect.DeepEqual(res.FullCosts, want) {
		t.Fatalf("full costs = %v, want %v", res.FullCosts, want)
	}
}

// The remainder goes to the smallest cumulative H, not the smallest id.
func TestRemainderByHAscendingNotID(t *testing.T) {
	l := mustLedger(t, 1, 2)
	mustSetUsage(t, l, 0, 1, 1)
	mustSetUsage(t, l, 0, 2, 1)

	// Period 1: H is all zero, tie breaks to the smaller id (dept 1).
	res := mustClose(t, l, []int64{3}, []int64{0, 0})
	wantShares := [][]Share{{{Dept: 1, Amount: 2}, {Dept: 2, Amount: 1}}}
	if got := sharesView(res); !reflect.DeepEqual(got, wantShares) {
		t.Fatalf("period 1 shares = %v, want %v", got, wantShares)
	}

	// Period 2: identical input, but H now differs (2 vs 1), so the
	// remainder cent goes to dept 2 despite its larger id.
	res = mustClose(t, l, []int64{3}, []int64{0, 0})
	wantShares = [][]Share{{{Dept: 1, Amount: 1}, {Dept: 2, Amount: 2}}}
	if got := sharesView(res); !reflect.DeepEqual(got, wantShares) {
		t.Fatalf("period 2 shares = %v, want %v", got, wantShares)
	}
	if want := []int64{0, 3, 3}; !reflect.DeepEqual(l.History(), want) {
		t.Fatalf("H = %v, want %v", l.History(), want)
	}
}

// H used by a step's remainder includes shares handed out earlier in
// the same period.
func TestRemainderUsesHUpdatedWithinPeriod(t *testing.T) {
	l := mustLedger(t, 2, 2)
	mustSetUsage(t, l, 0, 2, 1)
	mustSetUsage(t, l, 0, 3, 1)
	mustSetUsage(t, l, 1, 2, 1)
	mustSetUsage(t, l, 1, 3, 1)
	// All ratios are 0, dept 0 pushed first by id.
	// Step 1: T=3, shares 1/1, remainder 1 -> dept 2 (H tie, smaller id).
	// Step 2: T=3, shares 1/1, remainder 1 -> dept 3, because its H is
	// now smaller (1 vs 2) after step 1.
	res := mustClose(t, l, []int64{3, 3}, []int64{0, 0})

	wantShares := [][]Share{
		{{Dept: 2, Amount: 2}, {Dept: 3, Amount: 1}},
		{{Dept: 2, Amount: 1}, {Dept: 3, Amount: 2}},
	}
	if got := sharesView(res); !reflect.DeepEqual(got, wantShares) {
		t.Fatalf("shares = %v, want %v", got, wantShares)
	}
	if want := []int64{0, 0, 3, 3}; !reflect.DeepEqual(l.History(), want) {
		t.Fatalf("H = %v, want %v", l.History(), want)
	}
}

// T_s * u[s][r] can exceed int64 (here 1e19); 128-bit arithmetic is
// required for the floor division.
func Test128BitProduct(t *testing.T) {
	l := mustLedger(t, 1, 2)
	mustSetUsage(t, l, 0, 1, 1_000_000)
	mustSetUsage(t, l, 0, 2, 1_000_000)

	res := mustClose(t, l, []int64{10_000_000_000_000}, []int64{0, 0})
	wantShares := [][]Share{
		{{Dept: 1, Amount: 5_000_000_000_000}, {Dept: 2, Amount: 5_000_000_000_000}},
	}
	if got := sharesView(res); !reflect.DeepEqual(got, wantShares) {
		t.Fatalf("shares = %v, want %v", got, wantShares)
	}
	if want := []int64{5_000_000_000_000, 5_000_000_000_000}; !reflect.DeepEqual(res.FullCosts, want) {
		t.Fatalf("full costs = %v, want %v", res.FullCosts, want)
	}
}

// A positive total with no recipient rejects the Close and leaves H
// and usage untouched; the rejected close does not count as a period.
func TestNoRecipientRejectedAndStateUnchanged(t *testing.T) {
	l := mustLedger(t, 1, 1)
	// No usage registered: dept 0 has T = 5 > 0 but no recipient.
	if _, err := l.Close([]int64{5}, []int64{7}); !errors.Is(err, ErrNoRecipient) {
		t.Fatalf("err = %v, want ErrNoRecipient", err)
	}
	if want := []int64{0, 0}; !reflect.DeepEqual(l.History(), want) {
		t.Fatalf("H after rejection = %v, want %v", l.History(), want)
	}

	// A later valid Close behaves as the first successful period.
	mustSetUsage(t, l, 0, 1, 1)
	res := mustClose(t, l, []int64{5}, []int64{7})
	if want := []int64{12}; !reflect.DeepEqual(res.FullCosts, want) {
		t.Fatalf("full costs = %v, want %v", res.FullCosts, want)
	}
	if want := []int64{0, 5}; !reflect.DeepEqual(l.History(), want) {
		t.Fatalf("H = %v, want %v", l.History(), want)
	}
}

// Invalid parameters are rejected as a whole and change nothing.
func TestInvalidArgumentsRejected(t *testing.T) {
	if _, err := NewLedger(0, 1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("NewLedger(0,1) err = %v", err)
	}
	if _, err := NewLedger(1, 9); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("NewLedger(1,9) err = %v", err)
	}

	l := mustLedger(t, 1, 1)
	mustSetUsage(t, l, 0, 1, 4)
	bad := []struct {
		s, r int
		u    int64
	}{
		{1, 0, 1},         // s not a service department
		{-1, 0, 1},        // s negative
		{0, 2, 1},         // r out of range
		{0, 0, 1},         // r == s
		{0, 1, -1},        // u negative
		{0, 1, 1_000_001}, // u too large
	}
	for _, c := range bad {
		if err := l.SetUsage(c.s, c.r, c.u); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("SetUsage(%d,%d,%d) err = %v, want ErrInvalidArgument", c.s, c.r, c.u, err)
		}
	}

	if _, err := l.Close([]int64{1, 2}, []int64{0}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Close wrong ds length err = %v", err)
	}
	if _, err := l.Close([]int64{1}, nil); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Close wrong dp length err = %v", err)
	}
	if _, err := l.Close([]int64{-1}, []int64{0}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Close negative cost err = %v", err)
	}
	if _, err := l.Close([]int64{0}, []int64{10_000_000_000_001}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Close cost too large err = %v", err)
	}

	// Usage and H are untouched by every rejection above.
	res := mustClose(t, l, []int64{8}, []int64{0})
	if want := []int64{8}; !reflect.DeepEqual(res.FullCosts, want) {
		t.Fatalf("full costs = %v, want %v (usage must be unchanged)", res.FullCosts, want)
	}
	if want := []int64{0, 8}; !reflect.DeepEqual(l.History(), want) {
		t.Fatalf("H = %v, want %v", l.History(), want)
	}
}

// Returned slices never alias internal state.
func TestNoAliasing(t *testing.T) {
	l := mustLedger(t, 1, 1)
	mustSetUsage(t, l, 0, 1, 1)
	res := mustClose(t, l, []int64{4}, []int64{1})
	res.FullCosts[0] = -99
	res.Steps[0].Shares[0].Amount = -99
	h := l.History()
	h[1] = -99

	res2 := mustClose(t, l, []int64{4}, []int64{1})
	if want := []int64{5}; !reflect.DeepEqual(res2.FullCosts, want) {
		t.Fatalf("full costs = %v, want %v", res2.FullCosts, want)
	}
	if want := []int64{0, 8}; !reflect.DeepEqual(l.History(), want) {
		t.Fatalf("H = %v, want %v", l.History(), want)
	}
}

// Replaying the same operation sequence reproduces identical results.
func TestDeterministicReplay(t *testing.T) {
	run := func() ([]*CloseResult, []int64) {
		l := mustLedger(t, 2, 2)
		mustSetUsage(t, l, 0, 1, 3)
		mustSetUsage(t, l, 0, 2, 1)
		mustSetUsage(t, l, 0, 3, 2)
		mustSetUsage(t, l, 1, 0, 1)
		mustSetUsage(t, l, 1, 3, 4)
		var out []*CloseResult
		out = append(out, mustClose(t, l, []int64{101, 60}, []int64{10, 20}))
		mustSetUsage(t, l, 0, 3, 5) // overwrite
		out = append(out, mustClose(t, l, []int64{7, 7}, []int64{1, 1}))
		return out, l.History()
	}
	r1, h1 := run()
	r2, h2 := run()
	if !reflect.DeepEqual(r1, r2) || !reflect.DeepEqual(h1, h2) {
		t.Fatalf("replay diverged: %v/%v vs %v/%v", r1, h1, r2, h2)
	}
}
