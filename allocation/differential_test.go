package allocation

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"testing"
)

// op is one recorded operation in a random sequence, kept so the whole
// sequence can be logged and replayed.
type op struct {
	kind   string // "set" or "close"
	s, r   int
	u      int64
	ds, dp []int64
}

func (o op) String() string {
	if o.kind == "set" {
		return fmt.Sprintf("SetUsage(%d, %d, %d)", o.s, o.r, o.u)
	}
	return fmt.Sprintf("Close(%v, %v)", o.ds, o.dp)
}

func randCost(rng *rand.Rand) int64 {
	switch rng.Intn(10) {
	case 0:
		return 0
	case 1, 2: // huge, forces 128-bit products
		return rng.Int63n(maxCost + 1)
	default: // small, exercises remainders
		return rng.Int63n(60)
	}
}

func randUsage(rng *rand.Rand) int64 {
	switch rng.Intn(10) {
	case 0, 1:
		return 0
	case 2:
		return rng.Int63n(maxUsage + 1)
	default:
		return rng.Int63n(9)
	}
}

// TestDifferentialRandom replays 2000 random operation sequences
// against both the ledger and the naive big.Int simulation, comparing
// every result, and checks the conservation invariants on the way.
func TestDifferentialRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(20261003))
	const sequences = 2000
	var totalAccepted, totalRem, multiCloseSeqs int

	for seq := 0; seq < sequences; seq++ {
		k := 1 + rng.Intn(4)
		p := 1 + rng.Intn(4)
		if seq%97 == 0 { // occasionally exercise the maximum size
			k, p = 8, 8
		}
		n := k + p
		l := mustLedger(t, k, p)
		nl := newNaive(k, p)

		ops := 5 + rng.Intn(12)
		history := make([]op, 0, ops)

		for i := 0; i < ops; i++ {
			if rng.Intn(5) < 3 {
				var s, r int
				if rng.Intn(10) < 9 { // mostly valid coordinates
					s = rng.Intn(k)
					r = rng.Intn(n - 1)
					if r >= s {
						r++
					}
				} else { // occasionally out of range on purpose
					s, r = rng.Intn(n+2)-1, rng.Intn(n+2)-1
				}
				u := randUsage(rng)
				if rng.Intn(20) == 0 {
					u = -1 // invalid on purpose
				}
				o := op{kind: "set", s: s, r: r, u: u}
				history = append(history, o)

				errReal := l.SetUsage(s, r, u)
				errNaive := nl.setUsage(s, r, u)
				if (errReal == nil) != (errNaive == nil) {
					t.Fatalf("seq %d op %q: SetUsage error mismatch: real=%v naive=%v",
						seq, o, errReal, errNaive)
				}
				continue
			}

			ds := make([]int64, k)
			dp := make([]int64, p)
			for j := range ds {
				ds[j] = randCost(rng)
			}
			for j := range dp {
				dp[j] = randCost(rng)
			}
			if rng.Intn(25) == 0 && len(ds) > 0 {
				ds[rng.Intn(len(ds))] = maxCost + 1 // invalid on purpose
			}
			o := op{kind: "close", ds: ds, dp: dp}
			history = append(history, o)

			resReal, errReal := l.Close(ds, dp)
			stepsNaive, fullNaive, errNaive := nl.close(ds, dp)

			if (errReal == nil) != (errNaive == nil) {
				t.Fatalf("seq %d op %q: error mismatch: real=%v naive=%v\nops: %v",
					seq, o, errReal, errNaive, history)
			}
			if errReal != nil {
				if errors.Is(errReal, ErrInvalidArgument) != (errNaive == ErrInvalidArgument) ||
					errors.Is(errReal, ErrNoRecipient) != (errNaive == ErrNoRecipient) {
					t.Fatalf("seq %d op %q: error kind mismatch: real=%v naive=%v",
						seq, o, errReal, errNaive)
				}
				continue
			}

			// Compare the full push-down transcript.
			if len(resReal.Steps) != len(stepsNaive) {
				t.Fatalf("seq %d op %q: step count %d != %d", seq, o, len(resReal.Steps), len(stepsNaive))
			}
			var sumDS, sumDP int64
			for _, c := range ds {
				sumDS += c
			}
			for _, c := range dp {
				sumDP += c
			}
			var sumFull int64
			for j, st := range resReal.Steps {
				ns := stepsNaive[j]
				if st.Service != ns.service || st.Total != ns.total ||
					!reflect.DeepEqual(st.Shares, nilIfEmpty(ns.shares)) {
					t.Fatalf("seq %d op %q step %d: real=%+v naive=%+v",
						seq, o, j, st, ns)
				}
				var sumShares int64
				for _, sh := range st.Shares {
					sumShares += sh.Amount
				}
				if st.Total > 0 && sumShares != st.Total {
					t.Fatalf("seq %d op %q step %d: shares sum %d != total %d",
						seq, o, j, sumShares, st.Total)
				}
			}
			for _, c := range resReal.FullCosts {
				sumFull += c
			}
			if !reflect.DeepEqual(resReal.FullCosts, fullNaive) {
				t.Fatalf("seq %d op %q: full costs %v != %v", seq, o, resReal.FullCosts, fullNaive)
			}
			if sumFull != sumDS+sumDP {
				t.Fatalf("seq %d op %q: conservation violated: full=%d ds+dp=%d",
					seq, o, sumFull, sumDS+sumDP)
			}
			if !reflect.DeepEqual(l.History(), nl.h) {
				t.Fatalf("seq %d op %q: H %v != %v", seq, o, l.History(), nl.h)
			}
			for _, hv := range l.History() {
				if hv < 0 {
					t.Fatalf("seq %d op %q: negative H %v", seq, o, l.History())
				}
			}
		}
		t.Logf("seq %d: k=%d p=%d ops=%v -> H=%v (matched naive big.Int simulation)",
			seq, k, p, history, l.History())
		totalAccepted += nl.acceptedCls
		totalRem += nl.remEvents
		if nl.acceptedCls >= 2 {
			multiCloseSeqs++
		}
	}
	t.Logf("accepted closes=%d, remainder events=%d, multi-close sequences=%d",
		totalAccepted, totalRem, multiCloseSeqs)
	if totalAccepted < 2000 || totalRem < 500 || multiCloseSeqs < 500 {
		t.Fatalf("random coverage too thin: accepted=%d remainders=%d multi=%d",
			totalAccepted, totalRem, multiCloseSeqs)
	}
}

func nilIfEmpty(s []Share) []Share {
	if len(s) == 0 {
		return nil
	}
	return s
}

// TestConcurrent hammers one ledger from many goroutines; accepted
// closes must satisfy the conservation invariants and H must equal the
// sum of every share ever handed out.
func TestConcurrent(t *testing.T) {
	l := mustLedger(t, 3, 3)
	const workers = 8
	const rounds = 200

	type accepted struct {
		res *CloseResult
		ds  []int64
		dp  []int64
	}
	var mu sync.Mutex
	var got []accepted

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for i := 0; i < rounds; i++ {
				s := rng.Intn(3)
				r := rng.Intn(6)
				if r != s {
					_ = l.SetUsage(s, r, rng.Int63n(5))
				}
				ds := []int64{rng.Int63n(50), rng.Int63n(50), rng.Int63n(50)}
				dp := []int64{rng.Int63n(50), rng.Int63n(50), rng.Int63n(50)}
				res, err := l.Close(ds, dp)
				if err != nil {
					continue // rejected closes change nothing
				}
				mu.Lock()
				got = append(got, accepted{res, ds, dp})
				mu.Unlock()
			}
		}(int64(w) + 1)
	}
	wg.Wait()

	var totalShares int64
	for _, a := range got {
		var sumDS, sumDP, sumFull int64
		for _, c := range a.ds {
			sumDS += c
		}
		for _, c := range a.dp {
			sumDP += c
		}
		for _, c := range a.res.FullCosts {
			sumFull += c
		}
		if sumFull != sumDS+sumDP {
			t.Fatalf("conservation violated: full=%d ds+dp=%d", sumFull, sumDS+sumDP)
		}
		for _, st := range a.res.Steps {
			var sum int64
			for _, sh := range st.Shares {
				sum += sh.Amount
			}
			if st.Total > 0 && sum != st.Total {
				t.Fatalf("step %+v: shares sum %d", st, sum)
			}
			totalShares += sum
		}
	}
	var sumH int64
	for _, hv := range l.History() {
		if hv < 0 {
			t.Fatalf("negative H: %v", l.History())
		}
		sumH += hv
	}
	if sumH != totalShares {
		t.Fatalf("sum(H)=%d != total shares handed out %d", sumH, totalShares)
	}
	t.Logf("accepted %d closes, sum(H)=%d", len(got), sumH)
}
