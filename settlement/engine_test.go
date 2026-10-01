package settlement

import (
	"errors"
	"reflect"
	"sync"
	"testing"
)

func mustTrade(t *testing.T, e *Engine, buyer, seller string, n uint64) uint64 {
	t.Helper()
	seq, err := e.Trade(buyer, seller, n)
	if err != nil {
		t.Fatalf("Trade(%s,%s,%d): %v", buyer, seller, n, err)
	}
	return seq
}

func mustMargin(t *testing.T, e *Engine, acct string, g uint64) {
	t.Helper()
	if err := e.Margin(acct, g); err != nil {
		t.Fatalf("Margin(%s,%d): %v", acct, g, err)
	}
}

func mustSettle(t *testing.T, e *Engine, s uint64) *Settlement {
	t.Helper()
	res, err := e.Settle(s)
	if err != nil {
		t.Fatalf("Settle(%d): %v", s, err)
	}
	return res
}

func sumNet(res *Settlement) int64 {
	var sum int64
	for _, n := range res.NetCash {
		sum += n.Net
	}
	return sum
}

// Spec example 1: Call K=100 Mu=10 T=1, B abstains, partial assignment,
// Y defaults by 15 and A absorbs the whole loss.
func TestSpecExample1(t *testing.T) {
	e, err := NewEngine(Call, 100, 10, 1)
	if err != nil {
		t.Fatal(err)
	}
	mustTrade(t, e, "A", "X", 3)
	mustTrade(t, e, "B", "Y", 5)
	mustTrade(t, e, "A", "Z", 2)
	if err := e.Abstain("B"); err != nil {
		t.Fatal(err)
	}
	mustMargin(t, e, "X", 60)
	mustMargin(t, e, "Y", 25)

	res := mustSettle(t, e, 102)
	if res.Value != 2 || res.Total != 5 {
		t.Fatalf("v=%d Q=%d, want 2, 5", res.Value, res.Total)
	}
	if want := []Exercise{{"A", 5}}; !reflect.DeepEqual(res.Exercises, want) {
		t.Fatalf("exercises=%+v want %+v", res.Exercises, want)
	}
	if want := []Assignment{{1, "X", 3}, {2, "Y", 2}, {3, "Z", 0}}; !reflect.DeepEqual(res.Assignments, want) {
		t.Fatalf("assignments=%+v want %+v", res.Assignments, want)
	}
	if want := []Default{{"Y", 15}}; !reflect.DeepEqual(res.Defaults, want) {
		t.Fatalf("defaults=%+v want %+v", res.Defaults, want)
	}
	if want := []NetCash{{"A", 85}, {"B", 0}, {"X", -60}, {"Y", -25}, {"Z", 0}}; !reflect.DeepEqual(res.NetCash, want) {
		t.Fatalf("net=%+v want %+v", res.NetCash, want)
	}
	if sumNet(res) != 0 {
		t.Fatal("net sum not zero")
	}
}

// delta*recv_a overflows 64 bits: big-integer loss sharing is required.
func TestBigNumbers(t *testing.T) {
	e, err := NewEngine(Call, 1, 1000, 1)
	if err != nil {
		t.Fatal(err)
	}
	// Q total 1e9 (the cap), v up to 1e6-1, Mu=1000.
	for i := 0; i < 500; i++ {
		mustTrade(t, e, "A", "X", 1_000_000)
		mustTrade(t, e, "B", "Y", 1_000_000)
	}
	mustMargin(t, e, "X", 1_000_000_000_000) // 1e12, far below owe
	res := mustSettle(t, e, 1_000_000)       // v = 999999
	// recv each = 999999*5e8*1e3 = 499999500000000000000 (~5e20 > 2^64).
	// owe_X = owe_Y = same; pay_X = 1e12, pay_Y = 0.
	// delta = 2*recv - 1e12; delta*recv ~ 2.5e35, needs big.Int.
	// Verify via independent computation.
	v := uint64(999_999)
	recv := v * 500_000_000 * 1000
	delta := 2*recv - 1_000_000_000_000
	// loss_A = floor(delta*recv/(2*recv)) = floor(delta/2)
	// delta is even? delta = 2*recv - 1e12, both even -> loss_A = delta/2,
	// loss_B = delta/2, rho = 0.
	wantLoss := delta / 2
	var gotA, gotB int64
	for _, n := range res.NetCash {
		switch n.Account {
		case "A":
			gotA = n.Net
		case "B":
			gotB = n.Net
		}
	}
	if gotA != int64(recv-wantLoss) || gotB != int64(recv-wantLoss) {
		t.Fatalf("A=%d B=%d, want both %d", gotA, gotB, int64(recv-wantLoss))
	}
	if sumNet(res) != 0 {
		t.Fatal("net sum not zero")
	}
	// loss sums to delta.
	var lossSum uint64
	for _, n := range res.NetCash {
		if n.Account == "A" || n.Account == "B" {
			lossSum += uint64(int64(recv) - n.Net)
		}
	}
	if lossSum != delta {
		t.Fatalf("loss sum %d != delta %d", lossSum, delta)
	}
}

// Rejection reasons are distinguishable and checked in order:
// invalid param, expired, self trade, no long position.
func TestRejections(t *testing.T) {
	// Constructor params.
	for _, args := range [][3]uint64{{0, 1, 1}, {1_000_001, 1, 1}, {1, 0, 1}, {1, 1001, 1}, {1, 1, 0}, {1, 1, 1_000_001}} {
		if _, err := NewEngine(Call, args[0], args[1], args[2]); !errors.Is(err, ErrInvalidParam) {
			t.Fatalf("NewEngine%v: %v", args, err)
		}
	}
	if _, err := NewEngine(OptionType(7), 1, 1, 1); !errors.Is(err, ErrInvalidParam) {
		t.Fatal("bad kind accepted")
	}

	e, err := NewEngine(Call, 100, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	// Trade invalid params.
	for _, c := range []struct {
		b, s string
		n    uint64
	}{{"", "X", 1}, {"A", "", 1}, {"A", "X", 0}, {"A", "X", 1_000_001}} {
		if _, err := e.Trade(c.b, c.s, c.n); !errors.Is(err, ErrInvalidParam) {
			t.Fatalf("Trade(%q,%q,%d): %v", c.b, c.s, c.n, err)
		}
	}
	// Self trade.
	if _, err := e.Trade("A", "A", 1); !errors.Is(err, ErrSelfTrade) {
		t.Fatalf("self trade: %v", err)
	}
	// Abstain without long position.
	if err := e.Abstain("A"); !errors.Is(err, ErrNoLongPosition) {
		t.Fatalf("abstain no long: %v", err)
	}
	if err := e.Abstain(""); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("abstain empty: %v", err)
	}
	// Margin invalid params.
	if err := e.Margin("", 1); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("margin empty acct: %v", err)
	}
	if err := e.Margin("M", 0); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("margin 0: %v", err)
	}
	if err := e.Margin("M", 1_000_000_000_001); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("margin too big: %v", err)
	}
	// Margin cumulative cap 1e15 per account.
	mustMargin(t, e, "M", 1_000_000_000_000)
	if err := e.Margin("M", 999_000_000_000_000); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("margin cumulative: %v", err)
	}
	// Trade cumulative cap 1e9.
	mustTrade(t, e, "A", "X", 1_000_000)
	if _, err := e.Trade("A", "X", 999_000_001); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("trade cumulative: %v", err)
	}
	// Settle invalid S does not expire the series.
	if _, err := e.Settle(0); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("settle 0: %v", err)
	}
	if _, err := e.Settle(1_000_001); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("settle too big: %v", err)
	}
	if e.Settled() {
		t.Fatal("invalid settle must not expire")
	}
	res := mustSettle(t, e, 150)
	if res.Total != 1_000_000 {
		t.Fatalf("Q=%d", res.Total)
	}
	// After expiry everything reports ErrExpired.
	if _, err := e.Trade("A", "X", 1); !errors.Is(err, ErrExpired) {
		t.Fatalf("trade after settle: %v", err)
	}
	if err := e.Margin("A", 1); !errors.Is(err, ErrExpired) {
		t.Fatalf("margin after settle: %v", err)
	}
	if err := e.Abstain("A"); !errors.Is(err, ErrExpired) {
		t.Fatalf("abstain after settle: %v", err)
	}
	if _, err := e.Settle(150); !errors.Is(err, ErrExpired) {
		t.Fatalf("second settle: %v", err)
	}
	// Invalid param still wins over expired.
	if _, err := e.Trade("", "", 0); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("invalid param must be reported first: %v", err)
	}
}

// A rejected operation must not change batches, margins, abstentions or
// expiry state.
func TestRejectedOpsKeepState(t *testing.T) {
	e, err := NewEngine(Call, 100, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	seq := mustTrade(t, e, "A", "X", 3)
	mustMargin(t, e, "X", 10)

	// Failed trades do not consume seq or totals.
	if _, err := e.Trade("A", "A", 5); !errors.Is(err, ErrSelfTrade) {
		t.Fatal(err)
	}
	if _, err := e.Trade("A", "X", 1_000_000_000); !errors.Is(err, ErrInvalidParam) {
		t.Fatal(err)
	}
	if got := mustTrade(t, e, "B", "Y", 2); got != seq+1 {
		t.Fatalf("seq jumped to %d after rejected trades", got)
	}
	if e.TotalTraded() != 5 {
		t.Fatalf("total=%d, want 5", e.TotalTraded())
	}
	// Failed margin does not change balance.
	if err := e.Margin("X", 1_000_000_000_000_000); !errors.Is(err, ErrInvalidParam) {
		t.Fatal(err)
	}
	if e.MarginOf("X") != 10 {
		t.Fatalf("margin=%d, want 10", e.MarginOf("X"))
	}
	// Failed abstain does not register.
	if err := e.Abstain("Z"); !errors.Is(err, ErrNoLongPosition) {
		t.Fatal(err)
	}
	res := mustSettle(t, e, 110)
	if res.Total != 5 {
		t.Fatalf("Q=%d, want 5 (no phantom abstain)", res.Total)
	}
}

// Concurrent use: exactly one Settle wins; invariants hold for the result.
func TestConcurrentSettle(t *testing.T) {
	for iter := 0; iter < 50; iter++ {
		e, err := NewEngine(Call, 100, 10, 1)
		if err != nil {
			t.Fatal(err)
		}
		mustTrade(t, e, "A", "X", 3)
		mustTrade(t, e, "B", "Y", 5)
		mustMargin(t, e, "X", 10)

		const n = 16
		var wg sync.WaitGroup
		errs := make([]error, n)
		ress := make([]*Settlement, n)
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				ress[i], errs[i] = e.Settle(102)
			}(i)
		}
		wg.Wait()
		var ok, expired int
		for i := 0; i < n; i++ {
			switch {
			case errs[i] == nil:
				ok++
			case errors.Is(errs[i], ErrExpired):
				expired++
			default:
				t.Fatalf("unexpected error %v", errs[i])
			}
		}
		if ok != 1 || expired != n-1 {
			t.Fatalf("ok=%d expired=%d", ok, expired)
		}
	}
}

// Mixed concurrent operations behave like some serial order: no race,
// and post-expiry operations all fail with ErrExpired.
func TestConcurrentMixed(t *testing.T) {
	e, err := NewEngine(Call, 100, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				switch j % 4 {
				case 0:
					_, _ = e.Trade("A", "X", 1)
				case 1:
					_ = e.Margin("X", 1)
				case 2:
					_ = e.Abstain("A")
				case 3:
					_, _ = e.Settle(150)
				}
			}
		}(i)
	}
	wg.Wait()
	if !e.Settled() {
		t.Fatal("expected settled")
	}
	// Everything now fails with ErrExpired (or invalid param first).
	if _, err := e.Trade("A", "X", 1); !errors.Is(err, ErrExpired) {
		t.Fatalf("trade after settle: %v", err)
	}
}

// v exactly equal to T exercises; v one below T does not.
func TestThresholdBoundary(t *testing.T) {
	build := func() *Engine {
		e, err := NewEngine(Call, 100, 10, 1)
		if err != nil {
			t.Fatal(err)
		}
		mustTrade(t, e, "A", "X", 3)
		mustTrade(t, e, "B", "Y", 5)
		mustTrade(t, e, "A", "Z", 2)
		if err := e.Abstain("B"); err != nil {
			t.Fatal(err)
		}
		mustMargin(t, e, "X", 60)
		mustMargin(t, e, "Y", 25)
		return e
	}

	// S=101: v=1 == T, still exercises.
	res := mustSettle(t, build(), 101)
	if res.Value != 1 || res.Total != 5 {
		t.Fatalf("v=%d Q=%d, want 1, 5", res.Value, res.Total)
	}

	// S=100: v=0 < T, nobody exercises.
	res = mustSettle(t, build(), 100)
	if res.Value != 0 || res.Total != 0 {
		t.Fatalf("v=%d Q=%d, want 0, 0", res.Value, res.Total)
	}
	if len(res.Exercises) != 0 || len(res.Defaults) != 0 {
		t.Fatalf("unexpected exercises/defaults: %+v %+v", res.Exercises, res.Defaults)
	}
	for _, a := range res.Assignments {
		if a.Quantity != 0 {
			t.Fatalf("assignment %+v should be 0", a)
		}
	}
	if sumNet(res) != 0 {
		t.Fatal("net sum not zero")
	}

	// v = T-1 with T > 1: no exercise.
	e, err := NewEngine(Call, 100, 1, 5)
	if err != nil {
		t.Fatal(err)
	}
	mustTrade(t, e, "A", "X", 7)
	res = mustSettle(t, e, 104) // v=4 = T-1
	if res.Total != 0 || len(res.Exercises) != 0 {
		t.Fatalf("v=T-1 must not exercise: %+v", res.Exercises)
	}
	res2 := mustSettleOK(t, 105, func() *Engine {
		e2, err := NewEngine(Call, 100, 1, 5)
		if err != nil {
			t.Fatal(err)
		}
		mustTrade(t, e2, "A", "X", 7)
		return e2
	})
	if res2.Total != 7 {
		t.Fatalf("v=T must exercise, Q=%d", res2.Total)
	}
}

func mustSettleOK(t *testing.T, s uint64, makeEngine func() *Engine) *Settlement {
	t.Helper()
	return mustSettle(t, makeEngine(), s)
}

// Put series: v = max(K-S, 0).
func TestPutExercise(t *testing.T) {
	e, err := NewEngine(Put, 100, 10, 2)
	if err != nil {
		t.Fatal(err)
	}
	mustTrade(t, e, "A", "X", 4)
	mustMargin(t, e, "X", 1000)

	res := mustSettle(t, e, 97) // v=3 >= T=2
	if res.Value != 3 || res.Total != 4 {
		t.Fatalf("v=%d Q=%d, want 3, 4", res.Value, res.Total)
	}
	if want := []NetCash{{"A", 120}, {"X", -120}}; !reflect.DeepEqual(res.NetCash, want) {
		t.Fatalf("net=%+v want %+v", res.NetCash, want)
	}

	e2, err := NewEngine(Put, 100, 10, 2)
	if err != nil {
		t.Fatal(err)
	}
	mustTrade(t, e2, "A", "X", 4)
	res = mustSettle(t, e2, 99) // v=1 < T=2
	if res.Total != 0 {
		t.Fatalf("put v<T must not exercise, Q=%d", res.Total)
	}
}

// Spec example 2: no abstain, floor sharing with remainder to the
// byte-smallest exercising account.
func TestSpecExample2(t *testing.T) {
	e, err := NewEngine(Call, 100, 10, 1)
	if err != nil {
		t.Fatal(err)
	}
	mustTrade(t, e, "A", "X", 3)
	mustTrade(t, e, "B", "Y", 5)
	mustTrade(t, e, "A", "Z", 2)
	mustMargin(t, e, "X", 60)
	mustMargin(t, e, "Y", 71)

	res := mustSettle(t, e, 102)
	if res.Total != 10 {
		t.Fatalf("Q=%d, want 10", res.Total)
	}
	if want := []Exercise{{"A", 5}, {"B", 5}}; !reflect.DeepEqual(res.Exercises, want) {
		t.Fatalf("exercises=%+v want %+v", res.Exercises, want)
	}
	if want := []Assignment{{1, "X", 3}, {2, "Y", 5}, {3, "Z", 2}}; !reflect.DeepEqual(res.Assignments, want) {
		t.Fatalf("assignments=%+v want %+v", res.Assignments, want)
	}
	if want := []Default{{"Y", 29}, {"Z", 40}}; !reflect.DeepEqual(res.Defaults, want) {
		t.Fatalf("defaults=%+v want %+v", res.Defaults, want)
	}
	// loss_A=35 (remainder), loss_B=34.
	if want := []NetCash{{"A", 65}, {"B", 66}, {"X", -60}, {"Y", -71}, {"Z", 0}}; !reflect.DeepEqual(res.NetCash, want) {
		t.Fatalf("net=%+v want %+v", res.NetCash, want)
	}
	if sumNet(res) != 0 {
		t.Fatal("net sum not zero")
	}
}

// Abstain is account-level: both long batches of the account are skipped,
// including batches opened after the Abstain call.
func TestAbstainAccountWide(t *testing.T) {
	e, err := NewEngine(Call, 100, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	mustTrade(t, e, "A", "X", 3)
	if err := e.Abstain("A"); err != nil {
		t.Fatal(err)
	}
	mustTrade(t, e, "A", "Y", 4) // opened after Abstain, still skipped
	mustTrade(t, e, "B", "Z", 2)
	mustMargin(t, e, "Z", 1000)

	res := mustSettle(t, e, 110)
	if res.Total != 2 {
		t.Fatalf("Q=%d, want 2 (only B)", res.Total)
	}
	if want := []Exercise{{"B", 2}}; !reflect.DeepEqual(res.Exercises, want) {
		t.Fatalf("exercises=%+v want %+v", res.Exercises, want)
	}
	// Repeated abstain succeeds with no side effect.
	e2, err := NewEngine(Call, 100, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	mustTrade(t, e2, "A", "X", 1)
	if err := e2.Abstain("A"); err != nil {
		t.Fatal(err)
	}
	if err := e2.Abstain("A"); err != nil {
		t.Fatalf("repeated abstain: %v", err)
	}
}

// All long holders abstain: Q=0, no assignment, no default.
func TestAllAbstain(t *testing.T) {
	e, err := NewEngine(Call, 100, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	mustTrade(t, e, "A", "X", 3)
	mustTrade(t, e, "B", "Y", 5)
	if err := e.Abstain("A"); err != nil {
		t.Fatal(err)
	}
	if err := e.Abstain("B"); err != nil {
		t.Fatal(err)
	}
	res := mustSettle(t, e, 200)
	if res.Total != 0 || len(res.Exercises) != 0 || len(res.Defaults) != 0 {
		t.Fatalf("all abstain: %+v", res)
	}
	for _, a := range res.Assignments {
		if a.Quantity != 0 {
			t.Fatalf("assignment %+v should be 0", a)
		}
	}
	if sumNet(res) != 0 {
		t.Fatal("net sum not zero")
	}
}

// Same account holds long and short batches; its recv does not offset owe.
func TestLongAndShortSameAccount(t *testing.T) {
	e, err := NewEngine(Call, 100, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	mustTrade(t, e, "A", "B", 5) // A long 5, B short 5
	mustTrade(t, e, "B", "C", 3) // B long 3, C short 3
	mustMargin(t, e, "B", 10)    // B owes 50, pays only 10
	mustMargin(t, e, "C", 100)

	res := mustSettle(t, e, 110) // v=10
	// Q=8: seq1 B assigned 5, seq2 C assigned 3.
	if want := []Assignment{{1, "B", 5}, {2, "C", 3}}; !reflect.DeepEqual(res.Assignments, want) {
		t.Fatalf("assignments=%+v want %+v", res.Assignments, want)
	}
	// owe_B=50, pay_B=10, D_B=40; owe_C=30, pay_C=30.
	// recv_A=50, recv_B=30, sum=80, delta=40.
	// loss_A=floor(40*50/80)=25, loss_B=floor(40*30/80)=15.
	if want := []Default{{"B", 40}}; !reflect.DeepEqual(res.Defaults, want) {
		t.Fatalf("defaults=%+v want %+v", res.Defaults, want)
	}
	if want := []NetCash{{"A", 25}, {"B", 30 - 15 - 10}, {"C", -30}}; !reflect.DeepEqual(res.NetCash, want) {
		t.Fatalf("net=%+v want %+v", res.NetCash, want)
	}
	if sumNet(res) != 0 {
		t.Fatal("net sum not zero")
	}
}

// Margin exactly equal to owe leaves no shortfall; one less leaves D=1.
func TestMarginExactAndOneShort(t *testing.T) {
	e, err := NewEngine(Call, 100, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	mustTrade(t, e, "A", "X", 5)
	mustMargin(t, e, "X", 50) // owe = 10*5 = 50, exact
	res := mustSettle(t, e, 110)
	if len(res.Defaults) != 0 {
		t.Fatalf("exact margin must not default: %+v", res.Defaults)
	}
	if want := []NetCash{{"A", 50}, {"X", -50}}; !reflect.DeepEqual(res.NetCash, want) {
		t.Fatalf("net=%+v want %+v", res.NetCash, want)
	}

	e2, err := NewEngine(Call, 100, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	mustTrade(t, e2, "A", "X", 5)
	mustMargin(t, e2, "X", 49) // one short
	res = mustSettle(t, e2, 110)
	if want := []Default{{"X", 1}}; !reflect.DeepEqual(res.Defaults, want) {
		t.Fatalf("defaults=%+v want %+v", res.Defaults, want)
	}
	if want := []NetCash{{"A", 49}, {"X", -49}}; !reflect.DeepEqual(res.NetCash, want) {
		t.Fatalf("net=%+v want %+v", res.NetCash, want)
	}
}

// When delta equals total recv, every exerciser loses exactly its recv.
func TestTotalDefault(t *testing.T) {
	e, err := NewEngine(Call, 100, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	mustTrade(t, e, "A", "X", 3)
	mustTrade(t, e, "B", "Y", 7)
	// No margin at all: delta == sum recv.
	res := mustSettle(t, e, 110) // v=10, recv_A=30, recv_B=70
	if want := []Default{{"X", 30}, {"Y", 70}}; !reflect.DeepEqual(res.Defaults, want) {
		t.Fatalf("defaults=%+v want %+v", res.Defaults, want)
	}
	for _, n := range res.NetCash {
		if n.Account == "A" || n.Account == "B" {
			if n.Net != 0 {
				t.Fatalf("%s net=%d, want 0 (loss == recv)", n.Account, n.Net)
			}
		}
	}
	if sumNet(res) != 0 {
		t.Fatal("net sum not zero")
	}
}
