package costalloc

import (
	"errors"
	"reflect"
	"sync"
	"testing"
)

func mustNew(t *testing.T, k, p int) *Ledger {
	t.Helper()
	l, err := New(k, p)
	if err != nil {
		t.Fatalf("New(%d, %d): %v", k, p, err)
	}
	return l
}

func mustUsage(t *testing.T, l *Ledger, s, r int, u int64) {
	t.Helper()
	if err := l.SetUsage(s, r, u); err != nil {
		t.Fatalf("SetUsage(%d, %d, %d): %v", s, r, u, err)
	}
}

func mustClose(t *testing.T, l *Ledger, ds, dp []int64) *Result {
	t.Helper()
	res, err := l.Close(ds, dp)
	if err != nil {
		t.Fatalf("Close(%v, %v): %v", ds, dp, err)
	}
	return res
}

func checkConservation(t *testing.T, res *Result, ds, dp []int64) {
	t.Helper()
	var dsSum, dpSum, fullSum int64
	for _, v := range ds {
		dsSum += v
	}
	for _, v := range dp {
		dpSum += v
	}
	for _, v := range res.FullCosts {
		fullSum += v
	}
	if fullSum != dsSum+dpSum {
		t.Errorf("full cost sum %d != ds+dp sum %d", fullSum, dsSum+dpSum)
	}
	for _, st := range res.Steps {
		var got int64
		for _, sh := range st.Shares {
			got += sh.Amount
		}
		if got != st.Total {
			t.Errorf("step s=%d: shares sum %d != total %d", st.Dept, got, st.Total)
		}
	}
}

// TestSpecExample replays the worked example from the specification.
func TestSpecExample(t *testing.T) {
	l := mustNew(t, 2, 2)
	mustUsage(t, l, 0, 1, 1)
	mustUsage(t, l, 0, 2, 1)
	mustUsage(t, l, 0, 3, 2)
	mustUsage(t, l, 1, 0, 1)
	mustUsage(t, l, 1, 2, 3)

	ds := []int64{101, 60}
	dp := []int64{10, 20}
	res := mustClose(t, l, ds, dp)
	wantSteps := []Step{
		{Dept: 0, Total: 101, Shares: []Share{{Dept: 1, Amount: 26}, {Dept: 2, Amount: 25}, {Dept: 3, Amount: 50}}},
		{Dept: 1, Total: 86, Shares: []Share{{Dept: 2, Amount: 86}}},
	}
	if !reflect.DeepEqual(res.Steps, wantSteps) {
		t.Errorf("steps = %+v, want %+v", res.Steps, wantSteps)
	}
	if want := []int64{121, 70}; !reflect.DeepEqual(res.FullCosts, want) {
		t.Errorf("full costs = %v, want %v", res.FullCosts, want)
	}
	if want := []int64{0, 26, 111, 50}; !reflect.DeepEqual(l.Snapshot(), want) {
		t.Errorf("H = %v, want %v", l.Snapshot(), want)
	}
	checkConservation(t, res, ds, dp)
	if l.Periods() != 1 {
		t.Errorf("periods = %d, want 1", l.Periods())
	}
}

// TestTieBreaksSmallerIndex: equal ratios push the smaller number first.
func TestTieBreaksSmallerIndex(t *testing.T) {
	l := mustNew(t, 2, 1)
	mustUsage(t, l, 0, 1, 1)
	mustUsage(t, l, 0, 2, 1)
	mustUsage(t, l, 1, 0, 1)
	mustUsage(t, l, 1, 2, 1)

	res := mustClose(t, l, []int64{2, 0}, []int64{0})
	if res.Steps[0].Dept != 0 || res.Steps[1].Dept != 1 {
		t.Errorf("push order = [%d %d], want [0 1]", res.Steps[0].Dept, res.Steps[1].Dept)
	}
}

// TestRatioRecomputedReversal builds three service departments so that the
// ratio order between departments 1 and 2 flips once department 0 is pushed:
// step 1 ratios are 0:2/3, 1:101/201, 2:50/100 (1 ahead of 2), but after 0
// is pushed department 1 recomputes to 1/101 while 2 stays 50/100, so 2 goes
// before 1. It also proves the denominator excludes pushed departments
// (department 1's b drops from 201 to 101, then to 100).
func TestRatioRecomputedReversal(t *testing.T) {
	l := mustNew(t, 3, 1)
	mustUsage(t, l, 0, 1, 2)
	mustUsage(t, l, 0, 3, 1)
	mustUsage(t, l, 1, 0, 100)
	mustUsage(t, l, 1, 2, 1)
	mustUsage(t, l, 1, 3, 100)
	mustUsage(t, l, 2, 1, 50)
	mustUsage(t, l, 2, 3, 50)

	ds := []int64{3, 0, 0}
	dp := []int64{0}
	res := mustClose(t, l, ds, dp)
	wantSteps := []Step{
		{Dept: 0, Total: 3, Shares: []Share{{Dept: 1, Amount: 2}, {Dept: 3, Amount: 1}}},
		{Dept: 2, Total: 0},
		{Dept: 1, Total: 2, Shares: []Share{{Dept: 3, Amount: 2}}},
	}
	if !reflect.DeepEqual(res.Steps, wantSteps) {
		t.Errorf("steps = %+v, want %+v", res.Steps, wantSteps)
	}
	if want := []int64{3}; !reflect.DeepEqual(res.FullCosts, want) {
		t.Errorf("full costs = %v, want %v", res.FullCosts, want)
	}
	checkConservation(t, res, ds, dp)
}

// TestDenominatorExcludesPushed: department 1's denominator in step 2 must
// exclude the already pushed department 0 (b = 3, not 4), so department 2
// receives floor(86*3/3) = 86 instead of floor(86*3/4) = 64.
func TestDenominatorExcludesPushed(t *testing.T) {
	l := mustNew(t, 2, 2)
	mustUsage(t, l, 0, 1, 1)
	mustUsage(t, l, 0, 2, 1)
	mustUsage(t, l, 0, 3, 2)
	mustUsage(t, l, 1, 0, 1)
	mustUsage(t, l, 1, 2, 3)

	res := mustClose(t, l, []int64{101, 60}, []int64{10, 20})
	got := res.Steps[1].Shares[0].Amount
	if got != 86 {
		t.Errorf("step 2 share = %d, want 86 (denominator must exclude pushed dept 0)", got)
	}
}

// TestZeroRatioLastAndZeroTotalNeedsNoReceiver: a department with b = 0 is
// pushed last, and with T = 0 it needs no receivers and Close succeeds.
func TestZeroRatioLastAndZeroTotalNeedsNoReceiver(t *testing.T) {
	l := mustNew(t, 2, 1)
	mustUsage(t, l, 0, 2, 1)

	ds := []int64{5, 0}
	dp := []int64{7}
	res := mustClose(t, l, ds, dp)
	wantSteps := []Step{
		{Dept: 0, Total: 5, Shares: []Share{{Dept: 2, Amount: 5}}},
		{Dept: 1, Total: 0},
	}
	if !reflect.DeepEqual(res.Steps, wantSteps) {
		t.Errorf("steps = %+v, want %+v", res.Steps, wantSteps)
	}
	if want := []int64{12}; !reflect.DeepEqual(res.FullCosts, want) {
		t.Errorf("full costs = %v, want %v", res.FullCosts, want)
	}
	checkConservation(t, res, ds, dp)
}

// TestCrossPeriodRemainder: two identical periods; the first remainder goes
// to the smaller number (H tied), the second to the department with the
// smaller cumulative H even though its number is larger.
func TestCrossPeriodRemainder(t *testing.T) {
	l := mustNew(t, 1, 2)
	mustUsage(t, l, 0, 1, 1)
	mustUsage(t, l, 0, 2, 1)

	res1 := mustClose(t, l, []int64{3}, []int64{0, 0})
	want1 := []Share{{Dept: 1, Amount: 2}, {Dept: 2, Amount: 1}}
	if !reflect.DeepEqual(res1.Steps[0].Shares, want1) {
		t.Errorf("period 1 shares = %+v, want %+v", res1.Steps[0].Shares, want1)
	}

	res2 := mustClose(t, l, []int64{3}, []int64{0, 0})
	want2 := []Share{{Dept: 1, Amount: 1}, {Dept: 2, Amount: 2}}
	if !reflect.DeepEqual(res2.Steps[0].Shares, want2) {
		t.Errorf("period 2 shares = %+v, want %+v (remainder must follow smaller H, not smaller number)", res2.Steps[0].Shares, want2)
	}
	if want := []int64{0, 3, 3}; !reflect.DeepEqual(l.Snapshot(), want) {
		t.Errorf("H = %v, want %v", l.Snapshot(), want)
	}
}

// TestRemainderUsesCurrentPeriodH: the remainder ordering must use H values
// that already include shares granted earlier in the same period.
func TestRemainderUsesCurrentPeriodH(t *testing.T) {
	l := mustNew(t, 2, 2)
	mustUsage(t, l, 0, 2, 1)
	mustUsage(t, l, 0, 3, 1)
	mustUsage(t, l, 1, 2, 1)
	mustUsage(t, l, 1, 3, 1)

	res := mustClose(t, l, []int64{3, 3}, []int64{0, 0})
	// Step 1 (s=0): 1 each, remainder 1; H tied so dept 2 gets the cent.
	// H becomes H2=2, H3=1.
	wantStep1 := []Share{{Dept: 2, Amount: 2}, {Dept: 3, Amount: 1}}
	if !reflect.DeepEqual(res.Steps[0].Shares, wantStep1) {
		t.Fatalf("step 1 shares = %+v, want %+v", res.Steps[0].Shares, wantStep1)
	}
	// Step 2 (s=1): 1 each, remainder 1; H2=2 > H3=1 from this very period,
	// so dept 3 gets the cent. Without current-period H this would tie and
	// go to dept 2.
	wantStep2 := []Share{{Dept: 2, Amount: 1}, {Dept: 3, Amount: 2}}
	if !reflect.DeepEqual(res.Steps[1].Shares, wantStep2) {
		t.Errorf("step 2 shares = %+v, want %+v (H must include current-period shares)", res.Steps[1].Shares, wantStep2)
	}
}

// Test128BitProduct: T*u reaches 9.99999e18, beyond int64, so the share
// computation must use 128-bit intermediate arithmetic.
func Test128BitProduct(t *testing.T) {
	l := mustNew(t, 1, 2)
	mustUsage(t, l, 0, 1, 999999)
	mustUsage(t, l, 0, 2, 1)

	ds := []int64{10_000_000_000_000}
	dp := []int64{0, 0}
	res := mustClose(t, l, ds, dp)
	want := []Share{
		{Dept: 1, Amount: 9_999_990_000_000},
		{Dept: 2, Amount: 10_000_000},
	}
	if !reflect.DeepEqual(res.Steps[0].Shares, want) {
		t.Errorf("shares = %+v, want %+v", res.Steps[0].Shares, want)
	}
	checkConservation(t, res, ds, dp)
}

// TestNoReceiverRejected: a positive total with b = 0 rejects the whole
// Close without touching H or the period counter.
func TestNoReceiverRejected(t *testing.T) {
	l := mustNew(t, 2, 1)
	mustUsage(t, l, 0, 2, 1)

	mustClose(t, l, []int64{5, 0}, []int64{7})
	before := l.Snapshot()
	periods := l.Periods()

	// Department 1 has no outgoing usage but a positive direct cost.
	_, err := l.Close([]int64{0, 5}, []int64{7})
	if !errors.Is(err, ErrNoReceiver) {
		t.Fatalf("err = %v, want ErrNoReceiver", err)
	}
	if errors.Is(err, ErrInvalidParam) {
		t.Fatalf("err = %v must be distinguishable from ErrInvalidParam", err)
	}
	if !reflect.DeepEqual(l.Snapshot(), before) {
		t.Errorf("H changed after rejection: %v -> %v", before, l.Snapshot())
	}
	if l.Periods() != periods {
		t.Errorf("periods changed after rejection: %d -> %d", periods, l.Periods())
	}

	// A positive total received from another department also rejects.
	l2 := mustNew(t, 2, 1)
	mustUsage(t, l2, 0, 1, 1)
	mustUsage(t, l2, 0, 2, 1)
	if _, err := l2.Close([]int64{3, 0}, []int64{0}); !errors.Is(err, ErrNoReceiver) {
		t.Fatalf("err = %v, want ErrNoReceiver (dept 1 inherits a positive total)", err)
	}
	if l2.Periods() != 0 {
		t.Errorf("periods = %d, want 0", l2.Periods())
	}
}

// TestInvalidParams: every out-of-range argument is rejected with
// ErrInvalidParam and leaves the ledger untouched.
func TestInvalidParams(t *testing.T) {
	for _, kp := range [][2]int{{0, 1}, {9, 1}, {1, 0}, {1, 9}, {-1, 1}, {1, -1}} {
		if _, err := New(kp[0], kp[1]); !errors.Is(err, ErrInvalidParam) {
			t.Errorf("New(%d, %d) err = %v, want ErrInvalidParam", kp[0], kp[1], err)
		}
	}

	l := mustNew(t, 2, 2) // n = 4
	badUsage := []struct {
		s, r int
		u    int64
	}{
		{-1, 1, 1}, {2, 1, 1}, {3, 1, 1}, // s not a service department
		{0, -1, 1}, {0, 4, 1}, // r out of range
		{0, 0, 1}, {1, 1, 1}, // r == s
		{0, 1, -1}, {0, 1, 1_000_001}, // u out of range
	}
	for _, c := range badUsage {
		if err := l.SetUsage(c.s, c.r, c.u); !errors.Is(err, ErrInvalidParam) {
			t.Errorf("SetUsage(%d, %d, %d) err = %v, want ErrInvalidParam", c.s, c.r, c.u, err)
		}
	}
	if err := l.SetUsage(0, 2, 0); err != nil {
		t.Errorf("SetUsage boundary u=0: %v", err)
	}
	if err := l.SetUsage(0, 2, 1_000_000); err != nil {
		t.Errorf("SetUsage boundary u=1e6: %v", err)
	}

	if _, err := l.Close([]int64{1}, []int64{0, 0}); !errors.Is(err, ErrInvalidParam) {
		t.Errorf("Close short ds err = %v, want ErrInvalidParam", err)
	}
	if _, err := l.Close([]int64{0, 0}, []int64{0}); !errors.Is(err, ErrInvalidParam) {
		t.Errorf("Close short dp err = %v, want ErrInvalidParam", err)
	}
	if _, err := l.Close([]int64{-1, 0}, []int64{0, 0}); !errors.Is(err, ErrInvalidParam) {
		t.Errorf("Close negative ds err = %v, want ErrInvalidParam", err)
	}
	if _, err := l.Close([]int64{0, 0}, []int64{0, 10_000_000_000_001}); !errors.Is(err, ErrInvalidParam) {
		t.Errorf("Close dp too large err = %v, want ErrInvalidParam", err)
	}
	if _, err := l.Close([]int64{10_000_000_000_000, 0}, []int64{0, 0}); err != nil {
		t.Errorf("Close boundary cost 1e13: %v", err)
	}

	// All rejections above must not have changed anything: usage set before
	// the invalid closes persists, exactly one period was closed.
	if l.Periods() != 1 {
		t.Errorf("periods = %d, want 1", l.Periods())
	}
	res := mustClose(t, l, []int64{1_000_000, 0}, []int64{0, 0})
	if got := res.Steps[0].Shares[0].Amount; got != 1_000_000 {
		t.Errorf("share = %d, want 1000000 (usage must persist across rejections)", got)
	}
	if got := res.Steps[0].Shares[0].Dept; got != 2 {
		t.Errorf("share dept = %d, want 2", got)
	}
}

// TestInvalidParamPrecedesNoReceiver: parameter errors are reported before
// the no-receiver condition.
func TestInvalidParamPrecedesNoReceiver(t *testing.T) {
	l := mustNew(t, 1, 1) // no usage at all: any positive cost has no receiver
	if _, err := l.Close([]int64{10_000_000_000_001}, []int64{0}); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("err = %v, want ErrInvalidParam to take precedence", err)
	}
	if _, err := l.Close([]int64{5}, []int64{0}); !errors.Is(err, ErrNoReceiver) {
		t.Fatalf("err = %v, want ErrNoReceiver", err)
	}
}

// TestNoAlias: results and snapshots must not alias caller slices or
// internal state.
func TestNoAlias(t *testing.T) {
	l := mustNew(t, 1, 2)
	mustUsage(t, l, 0, 1, 1)
	mustUsage(t, l, 0, 2, 1)

	ds := []int64{3}
	dp := []int64{10, 20}
	res := mustClose(t, l, ds, dp)
	full := append([]int64(nil), res.FullCosts...)
	snap := l.Snapshot()

	// Mutating caller-owned inputs afterwards must not matter.
	ds[0] = 999
	dp[0] = 999
	// Mutating returned structures must not leak into the ledger.
	res.Steps[0].Shares[0].Amount = -1
	res.FullCosts[0] = -1
	snap[1] = -1

	res2 := mustClose(t, l, []int64{3}, []int64{10, 20})
	if !reflect.DeepEqual(res2.FullCosts, []int64{11, 22}) {
		t.Errorf("second close full costs = %v, want [11 22]", res2.FullCosts)
	}
	_ = full
	if want := []int64{0, 3, 3}; !reflect.DeepEqual(l.Snapshot(), want) {
		t.Errorf("H = %v, want %v (returned slices must not alias state)", l.Snapshot(), want)
	}
}

// TestReplayDeterminism: the same operation sequence replays to identical
// results, full costs and H.
func TestReplayDeterminism(t *testing.T) {
	run := func() ([]*Result, []int64) {
		l := mustNew(t, 3, 2)
		mustUsage(t, l, 0, 1, 5)
		mustUsage(t, l, 0, 3, 7)
		mustUsage(t, l, 1, 2, 3)
		mustUsage(t, l, 1, 4, 11)
		mustUsage(t, l, 2, 0, 2)
		mustUsage(t, l, 2, 4, 13)
		var out []*Result
		out = append(out, mustClose(t, l, []int64{101, 202, 303}, []int64{11, 22}))
		mustUsage(t, l, 2, 4, 1) // usage persists and can be overwritten
		out = append(out, mustClose(t, l, []int64{7, 8, 9}, []int64{1, 2}))
		return out, l.Snapshot()
	}
	r1, h1 := run()
	r2, h2 := run()
	if !reflect.DeepEqual(r1, r2) {
		t.Errorf("replayed results differ:\n%+v\n%+v", r1, r2)
	}
	if !reflect.DeepEqual(h1, h2) {
		t.Errorf("replayed H differs: %v vs %v", h1, h2)
	}
}

// TestConcurrency hammers the ledger from many goroutines; the outcome must
// equal some serial execution: every close conserves, and final H equals the
// sum of shares of all successful closes.
func TestConcurrency(t *testing.T) {
	l := mustNew(t, 2, 2)
	mustUsage(t, l, 0, 1, 1)
	mustUsage(t, l, 0, 2, 1)
	mustUsage(t, l, 0, 3, 2)
	mustUsage(t, l, 1, 0, 1)
	mustUsage(t, l, 1, 2, 3)

	ds := []int64{101, 60}
	dp := []int64{10, 20}
	const workers = 8
	const closesPerWorker = 25

	var wg sync.WaitGroup
	results := make([][]*Result, workers)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < closesPerWorker; i++ {
				res, err := l.Close(ds, dp)
				if err != nil {
					t.Errorf("Close: %v", err)
					return
				}
				results[w] = append(results[w], res)
				_ = l.Snapshot()
				_ = l.Periods()
				if err := l.SetUsage(0, 2, int64(1+i%3)); err != nil {
					t.Errorf("SetUsage: %v", err)
					return
				}
			}
		}(w)
	}
	wg.Wait()

	if want := workers * closesPerWorker; l.Periods() != want {
		t.Fatalf("periods = %d, want %d", l.Periods(), want)
	}
	hSum := make([]int64, 4)
	for w := 0; w < workers; w++ {
		for _, res := range results[w] {
			checkConservation(t, res, ds, dp)
			for _, st := range res.Steps {
				for _, sh := range st.Shares {
					hSum[sh.Dept] += sh.Amount
				}
			}
		}
	}
	if got := l.Snapshot(); !reflect.DeepEqual(got, hSum) {
		t.Errorf("H = %v, want sum of all shares %v", got, hSum)
	}
	for i, v := range l.Snapshot() {
		if v < 0 {
			t.Errorf("H[%d] = %d < 0", i, v)
		}
	}
}
