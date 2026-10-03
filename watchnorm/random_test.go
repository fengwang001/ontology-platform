package watchnorm

import (
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"testing"
)

var (
	randSegs      = []string{"a", "b", "bc"}
	randBadPaths  = []string{"", "/a", "a/", "a//b", "a/./b", "a/../b", ".", ".."}
	randOpWeights = []struct {
		kind   string
		weight int
	}{
		{"C", 25}, {"D", 15}, {"MF", 20}, {"MT", 25}, {"O", 5}, {"T", 10},
	}
)

func randPath(r *rand.Rand) string {
	if r.Intn(100) < 8 {
		return randBadPaths[r.Intn(len(randBadPaths))]
	}
	depth := 1
	if r.Intn(100) < 40 {
		depth = 2
	}
	segs := make([]string, depth)
	for i := range segs {
		segs[i] = randSegs[r.Intn(len(randSegs))]
	}
	return strings.Join(segs, "/")
}

func randIsDir(r *rand.Rand) bool { return r.Intn(100) < 70 }

func randOps(r *rand.Rand, n int) []op {
	total := 0
	for _, w := range randOpWeights {
		total += w.weight
	}
	ops := make([]op, 0, n)
	var now int64
	for i := 0; i < n; i++ {
		now += r.Int63n(6)
		if r.Intn(100) < 5 {
			now -= 1 + r.Int63n(3) // sometimes go backwards (ErrClock)
		}
		if r.Intn(100) < 8 {
			// Inject a scripted move-out/move-in pair with a unique
			// cookie to exercise the rename pairing path.
			cookie := 100 + r.Int63n(50)
			dir := randIsDir(r)
			ops = append(ops, op{kind: "MF", now: now, path: randPath(r), cookie: cookie, isDir: dir})
			now += r.Int63n(4)
			ops = append(ops, op{kind: "MT", now: now, path: randPath(r), cookie: cookie, isDir: dir})
			continue
		}
		pick := r.Intn(total)
		kind := ""
		for _, w := range randOpWeights {
			if pick < w.weight {
				kind = w.kind
				break
			}
			pick -= w.weight
		}
		o := op{kind: kind, now: now}
		switch kind {
		case "C":
			o.path, o.isDir = randPath(r), randIsDir(r)
		case "D":
			o.path = randPath(r)
		case "MF", "MT":
			o.path, o.isDir = randPath(r), randIsDir(r)
			o.cookie = 1 + r.Int63n(3)
			if r.Intn(100) < 5 {
				o.cookie = 0
			}
		}
		ops = append(ops, o)
	}
	return ops
}

// TestRandomAgainstNaive replays 2000 random operation sequences
// against both implementations and requires identical event streams,
// identical errors and identical internal state after every step.
func TestRandomAgainstNaive(t *testing.T) {
	const sequences = 2000
	var statRenamed, statExpired, statRescan, statErrs int
	for seed := int64(0); seed < sequences; seed++ {
		r := rand.New(rand.NewSource(seed))
		w := 1 + r.Intn(4)
		p := int64(1 + r.Intn(12))
		ops := randOps(r, 40)

		norm, errN := New(w, p)
		naive, errX := NewNaive(w, p)
		if errN != nil || errX != nil {
			t.Fatalf("seed=%d: constructor errors %v / %v", seed, errN, errX)
		}

		var log strings.Builder
		fmt.Fprintf(&log, "seed=%d W=%d P=%d ops=%d\n", seed, w, p, len(ops))
		eventTotal := 0
		for i, o := range ops {
			evsN, errN := apply(norm, o)
			evsX, errX := apply(naive, o)
			eventTotal += len(evsN)
			for _, e := range evsN {
				switch e.Kind {
				case EventRenamed:
					statRenamed++
				case EventDeleted:
					statExpired++
				case EventRescan:
					statRescan++
				}
			}
			if errN != nil {
				statErrs++
			}
			fmt.Fprintf(&log, "  step %02d %-28s -> norm %s err=%v | naive %s err=%v\n",
				i, o, fmtEvents(evsN), errN, fmtEvents(evsX), errX)

			match := fmtEvents(evsN) == fmtEvents(evsX) && errN == errX &&
				norm.DebugState() == naive.DebugState() &&
				norm.WatchedCount() <= w
			if !match {
				t.Errorf("MISMATCH seed=%d W=%d P=%d step=%d op=%s\n"+
					"norm:  events=%s err=%v\nnaive: events=%s err=%v\n"+
					"norm state:\n%s\nnaive state:\n%s\nfull input log:\n%s",
					seed, w, p, i, o,
					fmtEvents(evsN), errN, fmtEvents(evsX), errX,
					norm.DebugState(), naive.DebugState(), log.String())
				break
			}
		}
		// Verdict basis: identical event stream, identical errors and
		// identical internal state at every step, watched count <= W.
		if seed < 3 {
			t.Logf("sequence seed=%d OK (verdict: events+errors+state identical)\n%s",
				seed, log.String())
		} else {
			t.Logf("seed=%d W=%d P=%d ops=%d events=%d verdict=match",
				seed, w, p, len(ops), eventTotal)
		}
	}
	t.Logf("coverage over %d sequences: renamed=%d expired-deleted=%d rescan=%d rejected-ops=%d",
		sequences, statRenamed, statExpired, statRescan, statErrs)
}

// TestConcurrent hammers one Normalizer from many goroutines; with the
// race detector enabled this validates the locking, and the watched
// budget invariant must hold at all times.
func TestConcurrent(t *testing.T) {
	const W = 4
	n, err := New(W, 8)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	stop := make(chan struct{})
	violations := make(chan int, 1)
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
				if got := n.WatchedCount(); got > W {
					select {
					case violations <- got:
					default:
					}
					return
				}
			}
		}
	}()
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			r := rand.New(rand.NewSource(seed))
			for _, o := range randOps(r, 200) {
				apply(n, o)
			}
		}(int64(g) + 1000)
	}
	wg.Wait()
	close(stop)
	select {
	case got := <-violations:
		t.Fatalf("watched count %d exceeded W=%d during concurrent run", got, W)
	default:
	}
	if got := n.WatchedCount(); got > W {
		t.Fatalf("final watched count %d exceeds W=%d", got, W)
	}
}
