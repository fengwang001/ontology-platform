package cursor

import (
	"fmt"
	"math"
	"math/rand"
	"strings"
	"sync"
	"testing"
)

var diffOps = []Op{
	OpNext, OpPrior, OpFirst, OpLast, OpAbsolute, OpRelative, OpForward, OpBackward,
}

// randomK chooses an exact-integer argument, mostly small values with rare
// int64 extremes (multi-row extremes are only injected on tiny sets so the
// test does not allocate enormous slices).
func randomK(rng *rand.Rand, op Op, n int64) int64 {
	if rng.Intn(10) == 0 {
		switch rng.Intn(4) {
		case 0:
			return math.MaxInt64
		case 1:
			return math.MinInt64
		case 2:
			return 1
		default:
			return -1
		}
	}
	bound := int64(n) + 4
	if bound < 8 {
		bound = 8
	}
	v := rng.Int63n(bound*2+1) - bound
	if (op == OpForward || op == OpBackward) && v <= 0 && rng.Intn(2) == 0 {
		v = rng.Int63n(4) + 1
	}
	return v
}

func runSequence(t *testing.T, r *Registry, name string, n int64, scroll bool, rng *rand.Rand, steps int) (*oracle, *strings.Builder) {
	t.Helper()
	if err := r.Open(name, n, scroll); err != nil {
		t.Fatalf("open: %v", err)
	}
	m := newOracle(n, scroll)
	var log strings.Builder
	fmt.Fprintf(&log, "== cursor %q n=%d scroll=%v steps=%d ==\n", name, n, scroll, steps)
	for i := 0; i < steps; i++ {
		var op Op
		if rng.Intn(12) == 0 {
			op = Op("SIDEWAYS")
		} else {
			op = diffOps[rng.Intn(len(diffOps))]
		}
		k := randomK(rng, op, n)
		mustFetch(t, r, name, m, op, k, &log)
	}
	return m, &log
}

func TestDifferentialRandom(t *testing.T) {
	const groups = 2000
	rng := rand.New(rand.NewSource(20261001))
	var all strings.Builder
	for g := 0; g < groups; g++ {
		r := NewRegistry()
		n := rng.Int63n(16) // 0..15
		scroll := rng.Intn(2) == 0
		steps := 1 + rng.Intn(25)
		name := fmt.Sprintf("c%d", g)
		_, trace := runSequence(t, r, name, n, scroll, rng, steps)
		if err := r.Close(name); err != nil {
			t.Fatalf("close: %v", err)
		}
		all.WriteString(trace.String())
		all.WriteString("CLOSE ok\n")
	}
	t.Logf("differential trace for %d random sequences:\n%s", groups, all.String())
}

// recordedOp is one replayed fetch request.
type recordedOp struct {
	op Op
	k  int64
}

func TestReplayDeterminism(t *testing.T) {
	rng := rand.New(rand.NewSource(424242))
	n := rng.Int63n(12)
	var script []recordedOp
	for i := 0; i < 60; i++ {
		op := diffOps[rng.Intn(len(diffOps))]
		script = append(script, recordedOp{op, randomK(rng, op, n)})
	}

	play := func() ([][]int64, []string, []int64) {
		r := NewRegistry()
		if err := r.Open("replay", n, true); err != nil {
			t.Fatal(err)
		}
		var rows [][]int64
		var errs []string
		var positions []int64
		for _, s := range script {
			got, err := r.Fetch("replay", s.op, s.k)
			rows = append(rows, got)
			errs = append(errs, errKind(err))
			p, _ := r.Position("replay")
			positions = append(positions, p)
		}
		return rows, errs, positions
	}

	rows1, errs1, pos1 := play()
	rows2, errs2, pos2 := play()
	for i := range script {
		if !sameRows(rows1[i], rows2[i]) || errs1[i] != errs2[i] || pos1[i] != pos2[i] {
			t.Fatalf("replay diverged at step %d %s k=%d: %v/%q/%d vs %v/%q/%d",
				i, script[i].op, script[i].k, rows1[i], errs1[i], pos1[i], rows2[i], errs2[i], pos2[i])
		}
	}
	t.Logf("replay of %d operations produced identical rows, errors and positions", len(script))
}

func TestConcurrentCursors(t *testing.T) {
	r := NewRegistry()
	const workers = 16
	rng := rand.New(rand.NewSource(777))
	var wg sync.WaitGroup
	var traces []string
	var traceMu sync.Mutex

	// Each worker owns a distinct cursor; the result must match its own
	// serial oracle, exercising independent parallel operations.
	for w := 0; w < workers; w++ {
		name := fmt.Sprintf("w%d", w)
		n := rng.Int63n(12)
		steps := 50
		script := make([]recordedOp, steps)
		for i := range script {
			op := diffOps[rng.Intn(len(diffOps))]
			script[i] = recordedOp{op, randomK(rng, op, n)}
		}
		if err := r.Open(name, n, true); err != nil {
			t.Fatal(err)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			m := newOracle(n, true)
			var log strings.Builder
			for _, s := range script {
				mustFetch(t, r, name, m, s.op, s.k, &log)
			}
			traceMu.Lock()
			traces = append(traces, log.String())
			traceMu.Unlock()
		}()
	}
	wg.Wait()

	// Shared-cursor stress: interleaved fetches must always leave the
	// position inside 0..n+1 even though per-call ordering is arbitrary.
	const n = int64(10)
	if err := r.Open("shared", n, true); err != nil {
		t.Fatal(err)
	}
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			lrng := rand.New(rand.NewSource(seed))
			for i := 0; i < 200; i++ {
				op := diffOps[lrng.Intn(len(diffOps))]
				if _, err := r.Fetch("shared", op, randomK(lrng, op, n)); err != nil && err != errNonPositiveCount {
					t.Errorf("unexpected shared fetch error: %v", err)
					return
				}
				p, err := r.Position("shared")
				if err != nil || p < 0 || p > n+1 {
					t.Errorf("position out of range: %d err=%v", p, err)
					return
				}
			}
		}(int64(w + 1))
	}
	wg.Wait()

	var all strings.Builder
	for _, tr := range traces {
		all.WriteString(tr)
	}
	t.Logf("concurrent cursor traces:\n%s", all.String())
}
