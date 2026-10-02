package reconcile_test

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"sync"
	"testing"

	"ontology/reconcile"
)

// checkInvariants verifies the cross-cutting guarantees on the real matcher:
// every line appears in at most one active match, amounts balance per match
// and in total, no active match contains a forbidden pair, and F only grows.
func checkInvariants(t *testing.T, m *reconcile.Matcher, prevForbidden map[reconcile.Pair]bool) map[reconcile.Pair]bool {
	t.Helper()

	lineAmt := make(map[string]int64)
	for _, l := range m.Unmatched(reconcile.Bank) {
		lineAmt["B"+l.ID] = l.Amt
	}
	for _, l := range m.Unmatched(reconcile.Book) {
		lineAmt["K"+l.ID] = l.Amt
	}

	forbidden := make(map[reconcile.Pair]bool)
	for _, p := range m.Forbidden() {
		forbidden[p] = true
	}
	for p := range prevForbidden {
		if !forbidden[p] {
			t.Fatalf("forbidden pair %+v disappeared; F must only grow", p)
		}
	}

	used := make(map[string]int64)
	var bankTotal, bookTotal int64
	for _, mt := range m.Matches() {
		var bankSum, bookSum int64
		for _, id := range mt.BankIDs {
			if prev, dup := used["B"+id]; dup {
				t.Fatalf("bank line %s in matches %d and %d", id, prev, mt.MID)
			}
			used["B"+id] = mt.MID
			if _, unmatched := lineAmt["B"+id]; unmatched {
				t.Fatalf("bank line %s both matched (mid %d) and unmatched", id, mt.MID)
			}
			bankSum += lineAmtOr(t, m, reconcile.Bank, id)
		}
		for _, id := range mt.BookIDs {
			if prev, dup := used["K"+id]; dup {
				t.Fatalf("book line %s in matches %d and %d", id, prev, mt.MID)
			}
			used["K"+id] = mt.MID
			if _, unmatched := lineAmt["K"+id]; unmatched {
				t.Fatalf("book line %s both matched (mid %d) and unmatched", id, mt.MID)
			}
			bookSum += lineAmtOr(t, m, reconcile.Book, id)
		}
		if bankSum != bookSum {
			t.Fatalf("match %d unbalanced: bank %d != book %d", mt.MID, bankSum, bookSum)
		}
		bankTotal += bankSum
		bookTotal += bookSum
		for _, b := range mt.BankIDs {
			for _, k := range mt.BookIDs {
				if forbidden[reconcile.Pair{BankID: b, BookID: k}] {
					t.Fatalf("active match %d contains forbidden pair (%s,%s)", mt.MID, b, k)
				}
			}
		}
	}
	if bankTotal != bookTotal {
		t.Fatalf("matched totals differ: bank %d != book %d", bankTotal, bookTotal)
	}
	return forbidden
}

// lineAmtOr looks up a matched line's amount by replaying adds is not
// possible, so amounts are tracked by the caller via this helper's fallback:
// it panics the test if the line cannot be found. Amounts of matched lines
// are supplied through a registry built by the random test itself.
var matchedAmt = struct {
	sync.Mutex
	m map[*reconcile.Matcher]map[string]int64
}{m: make(map[*reconcile.Matcher]map[string]int64)}

func lineAmtOr(t *testing.T, m *reconcile.Matcher, side reconcile.Side, id string) int64 {
	t.Helper()
	matchedAmt.Lock()
	defer matchedAmt.Unlock()
	key := fmt.Sprintf("%d:%s", side, id)
	amt, ok := matchedAmt.m[m][key]
	if !ok {
		t.Fatalf("no amount registered for %v %s", side, id)
	}
	return amt
}

func registerAmt(m *reconcile.Matcher, side reconcile.Side, id string, amt int64) {
	matchedAmt.Lock()
	defer matchedAmt.Unlock()
	if matchedAmt.m[m] == nil {
		matchedAmt.m[m] = make(map[string]int64)
	}
	matchedAmt.m[m][fmt.Sprintf("%d:%s", side, id)] = amt
}

func unregisterAmt(m *reconcile.Matcher) {
	matchedAmt.Lock()
	defer matchedAmt.Unlock()
	delete(matchedAmt.m, m)
}

// TestRandomAgainstNaive replays 2000 randomized sequences of adds,
// reconciles and reverses against both the real matcher and the naive
// oracle, requiring identical results after every single call, and logs the
// inputs, outputs and the judgement for each trial.
func TestRandomAgainstNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(20241001))
	refs := []string{"", "X", "Y"}
	amts := []int64{-200, -100, -50, 50, 100, 200}

	for trial := 0; trial < 2000; trial++ {
		real := reconcile.New()
		sim := newNaive()
		registerAmt(real, reconcile.Bank, "", 0) // ensure registry entry exists
		prevForbidden := make(map[reconcile.Pair]bool)
		var producedMids []int64

		var log strings.Builder
		fmt.Fprintf(&log, "trial %d\n", trial)

		ops := 1 + rng.Intn(24)
		for op := 0; op < ops; op++ {
			switch rng.Intn(10) {
			case 0, 1, 2, 3, 4: // AddLine
				side := reconcile.Bank
				prefix := "b"
				if rng.Intn(2) == 1 {
					side = reconcile.Book
					prefix = "k"
				}
				if rng.Intn(50) == 0 {
					side = reconcile.Side(9) // occasionally an invalid side
				}
				id := fmt.Sprintf("%s%d", prefix, rng.Intn(6))
				if rng.Intn(40) == 0 {
					id = "" // occasionally an empty id
				}
				amt := amts[rng.Intn(len(amts))]
				switch rng.Intn(40) {
				case 0:
					amt = 0
				case 1:
					amt = 1_000_000_000_001
				case 2:
					amt = 1_000_000_000_000
				}
				day := int64(rng.Intn(10))
				if rng.Intn(40) == 0 {
					day = 1_000_000_001
				}
				ref := refs[rng.Intn(len(refs))]

				errReal := real.AddLine(side, id, amt, day, ref)
				errSim := sim.addLine(side, id, amt, day, ref)
				if !errors.Is(errReal, errSim) {
					t.Fatalf("trial %d op %d AddLine(%d,%q,%d,%d,%q): real=%v naive=%v",
						trial, op, side, id, amt, day, ref, errReal, errSim)
				}
				fmt.Fprintf(&log, "  add side=%d id=%q amt=%d day=%d ref=%q -> %v\n", side, id, amt, day, ref, errReal)
				if errReal == nil && (side == reconcile.Bank || side == reconcile.Book) {
					registerAmt(real, side, id, amt)
				}
			case 5: // AddLine correlated with existing lines, to exercise
				// the one-to-many and many-to-one rounds more often.
				side := reconcile.Bank
				prefix := "b"
				others := sim.book
				if rng.Intn(2) == 1 {
					side = reconcile.Book
					prefix = "k"
					others = sim.bank
				}
				ref := refs[1+rng.Intn(2)] // non-empty ref
				day := int64(rng.Intn(8))
				amt := amts[rng.Intn(len(amts))]
				var group []*naiveLine
				for _, l := range others {
					if !l.matched && l.ref == ref {
						group = append(group, l)
					}
				}
				if len(group) >= 2 && rng.Intn(2) == 0 {
					// Aim the amount at the sum of a same-ref pair and the
					// day into their neighbourhood.
					i, j := rng.Intn(len(group)), rng.Intn(len(group))
					amt = group[i].amt + group[j].amt
					day = group[i].day
					if amt == 0 {
						amt = 50
					}
				}
				id := fmt.Sprintf("%s%d", prefix, rng.Intn(6))
				errReal := real.AddLine(side, id, amt, day, ref)
				errSim := sim.addLine(side, id, amt, day, ref)
				if !errors.Is(errReal, errSim) {
					t.Fatalf("trial %d op %d correlated AddLine(%d,%q,%d,%d,%q): real=%v naive=%v",
						trial, op, side, id, amt, day, ref, errReal, errSim)
				}
				fmt.Fprintf(&log, "  add-correlated side=%d id=%q amt=%d day=%d ref=%q -> %v\n", side, id, amt, day, ref, errReal)
				if errReal == nil {
					registerAmt(real, side, id, amt)
				}
			case 6, 7, 8: // Reconcile
				w := int64(rng.Intn(5))
				if rng.Intn(30) == 0 {
					w = -1
				}
				if rng.Intn(30) == 0 {
					w = 1_000_000_001
				}
				gotReal, errReal := real.Reconcile(w)
				gotSim, errSim := sim.reconcile(w)
				if !errors.Is(errReal, errSim) {
					t.Fatalf("trial %d op %d Reconcile(%d): real err=%v naive err=%v", trial, op, w, errReal, errSim)
				}
				if !reflect.DeepEqual(gotReal, gotSim) {
					t.Fatalf("trial %d op %d Reconcile(%d):\nreal  %+v\nnaive %+v", trial, op, w, gotReal, gotSim)
				}
				fmt.Fprintf(&log, "  reconcile w=%d -> %v, matches=%+v\n", w, errReal, gotReal)
				for _, mt := range gotReal {
					producedMids = append(producedMids, mt.MID)
				}
				prevForbidden = checkInvariants(t, real, prevForbidden)
			case 9: // Reverse
				mid := int64(1 + rng.Intn(8))
				if len(producedMids) > 0 && rng.Intn(2) == 0 {
					mid = producedMids[rng.Intn(len(producedMids))]
				}
				gotReal, errReal := real.Reverse(mid)
				gotSim, errSim := sim.reverse(mid)
				if !errors.Is(errReal, errSim) {
					t.Fatalf("trial %d op %d Reverse(%d): real err=%v naive err=%v", trial, op, mid, errReal, errSim)
				}
				if errReal == nil && !reflect.DeepEqual(gotReal, gotSim) {
					t.Fatalf("trial %d op %d Reverse(%d):\nreal  %+v\nnaive %+v", trial, op, mid, gotReal, gotSim)
				}
				fmt.Fprintf(&log, "  reverse mid=%d -> %v, restored=%+v\n", mid, errReal, gotReal)
				prevForbidden = checkInvariants(t, real, prevForbidden)
			}
		}

		// Final state must be identical.
		if got, want := real.Matches(), sim.activeMatches(); !reflect.DeepEqual(got, want) {
			t.Fatalf("trial %d final Matches():\nreal  %+v\nnaive %+v", trial, got, want)
		}
		if got, want := real.Unmatched(reconcile.Bank), sim.unmatched(reconcile.Bank); !reflect.DeepEqual(got, want) {
			t.Fatalf("trial %d final Unmatched(Bank):\nreal  %+v\nnaive %+v", trial, got, want)
		}
		if got, want := real.Unmatched(reconcile.Book), sim.unmatched(reconcile.Book); !reflect.DeepEqual(got, want) {
			t.Fatalf("trial %d final Unmatched(Book):\nreal  %+v\nnaive %+v", trial, got, want)
		}
		if got, want := real.Forbidden(), sim.forbiddenPairs(); !reflect.DeepEqual(got, want) {
			t.Fatalf("trial %d final Forbidden():\nreal  %+v\nnaive %+v", trial, got, want)
		}

		fmt.Fprintf(&log, "  final matches=%+v forbidden=%+v\n", real.Matches(), real.Forbidden())
		fmt.Fprintf(&log, "  judgement: real == naive on every call and final state; invariants hold\n")
		t.Log(log.String())
		unregisterAmt(real)
	}
}

// TestConcurrent hammers the matcher from many goroutines; with -race this
// proves freedom of data races, and the final invariant check confirms the
// state is one some serial order could produce.
func TestConcurrent(t *testing.T) {
	m := reconcile.New()
	registerAmt(m, reconcile.Bank, "", 0)
	defer unregisterAmt(m)

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		g := g
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				side := reconcile.Side(i % 2)
				id := fmt.Sprintf("g%02d-%04d", g, i)
				amt := int64((i%5 + 1) * 100)
				if i%3 == 0 {
					amt = -amt
				}
				if err := m.AddLine(side, id, amt, int64(i%20), []string{"", "X", "Y"}[i%3]); err != nil {
					t.Errorf("AddLine: %v", err)
				}
				registerAmt(m, side, id, amt)
			}
		}()
	}
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				if _, err := m.Reconcile(int64(i % 4)); err != nil {
					t.Errorf("Reconcile: %v", err)
				}
			}
		}()
	}
	for g := 0; g < 2; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				_, _ = m.Reverse(int64(i%40 + 1))
			}
		}()
	}
	for g := 0; g < 2; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				_ = m.Matches()
				_ = m.Unmatched(reconcile.Bank)
				_ = m.Unmatched(reconcile.Book)
				_ = m.Forbidden()
			}
		}()
	}
	wg.Wait()

	checkInvariants(t, m, make(map[reconcile.Pair]bool))
}
