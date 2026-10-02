package overagg

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// nRow is one row tracked by the naive reference model.
type nRow struct {
	key string
	ts  int64
	val int64
	seq int64
}

// naive is a straightforward rescan-everything reference implementation of
// the specification, used to cross-check the incremental aggregator.
type naive struct {
	r, al, cap int64
	wm         int64
	seq        int64
	buf        []nRow
	rel        []nRow // retained released rows
	late       int64
	reissued   int64
	invalid    int64
	full       int64
	released   int64
}

func newNaive(r, al, c int64) *naive {
	return &naive{r: r, al: al, cap: c, wm: -1}
}

func (n *naive) frame(key string, ts int64) (sum, cnt, max int64) {
	first := true
	for _, r := range n.rel {
		if r.key != key || r.ts < ts-n.r || r.ts > ts {
			continue
		}
		sum += r.val
		cnt++
		if first || r.val > max {
			max = r.val
		}
		first = false
	}
	return sum, cnt, max
}

func (n *naive) insert(key string, ts, val int64) (InsertResult, Output) {
	if len(key) == 0 || ts < 0 || ts > MaxParam || val < -MaxVal || val > MaxVal {
		n.invalid++
		return InsertInvalid, Output{}
	}
	if ts <= n.wm-n.al {
		n.late++
		return InsertLate, Output{}
	}
	if ts <= n.wm {
		sum, cnt, max := n.frame(key, ts)
		sum += val
		cnt++
		if cnt == 1 || val > max {
			max = val
		}
		n.rel = append(n.rel, nRow{key: key, ts: ts, val: val})
		n.reissued++
		return InsertReissued, out(key, ts, val, sum, cnt, max)
	}
	if int64(len(n.buf)) >= n.cap {
		n.full++
		return InsertFull, Output{}
	}
	n.seq++
	n.buf = append(n.buf, nRow{key: key, ts: ts, val: val, seq: n.seq})
	return InsertBuffered, Output{}
}

func (n *naive) advance(w int64) ([]Output, error) {
	if w < 0 || w > MaxParam {
		return nil, ErrInvalidParam
	}
	if w < n.wm {
		return nil, ErrWatermarkRegression
	}
	if w == n.wm {
		return nil, nil
	}
	var rel []nRow
	keep := n.buf[:0]
	for _, r := range n.buf {
		if r.ts <= w {
			rel = append(rel, r)
		} else {
			keep = append(keep, r)
		}
	}
	n.buf = keep
	sort.Slice(rel, func(i, j int) bool {
		if rel[i].ts != rel[j].ts {
			return rel[i].ts < rel[j].ts
		}
		return rel[i].seq < rel[j].seq
	})
	// Append every released row first so that ties see each other.
	n.rel = append(n.rel, rel...)
	var outs []Output
	for _, r := range rel {
		sum, cnt, max := n.frame(r.key, r.ts)
		outs = append(outs, out(r.key, r.ts, r.val, sum, cnt, max))
	}
	n.released += int64(len(rel))
	n.wm = w
	limit := w - n.r - n.al
	kept := n.rel[:0]
	for _, r := range n.rel {
		if r.ts > limit {
			kept = append(kept, r)
		}
	}
	n.rel = kept
	return outs, nil
}

func normOuts(o []Output) []Output {
	if len(o) == 0 {
		return nil
	}
	return o
}

// TestRandomAgainstNaive replays 2000 random operation sequences against
// both the incremental aggregator and the naive rescan model, comparing
// every output and every piece of observable state.
func TestRandomAgainstNaive(t *testing.T) {
	keys := []string{"a", "b", "c"}
	for seed := int64(0); seed < 2000; seed++ {
		rng := rand.New(rand.NewSource(seed))

		r := int64(rng.Intn(6))
		if rng.Intn(4) == 0 {
			r = int64(rng.Intn(1000))
		}
		al := int64(rng.Intn(4))
		if rng.Intn(4) == 0 {
			al = int64(rng.Intn(1000))
		}
		capN := int64(1 + rng.Intn(6))
		if rng.Intn(3) == 0 {
			capN = MaxCap
		}

		a, err := New(r, al, capN)
		if err != nil {
			t.Fatalf("seed=%d: New: %v", seed, err)
		}
		n := newNaive(r, al, capN)

		var log strings.Builder
		fmt.Fprintf(&log, "seed=%d R=%d AL=%d Cap=%d\n", seed, r, al, capN)
		fail := func(format string, args ...any) {
			t.Logf("\n%s", log.String())
			t.Fatalf("seed=%d: "+format, append([]any{seed}, args...)...)
		}

		var inserts int64
		ops := 30 + rng.Intn(60)
		for i := 0; i < ops; i++ {
			wm := a.WM()
			if wm != n.wm {
				fail("wm diverged: %d vs %d", wm, n.wm)
			}
			if rng.Intn(10) < 3 {
				var w int64
				switch rng.Intn(6) {
				case 0:
					w = wm // no-op
				case 1:
					w = wm - 1 - int64(rng.Intn(3)) // regression or invalid
				case 2:
					w = MaxParam + 1 // invalid
				default:
					w = wm + 1 + int64(rng.Intn(12))
				}
				fmt.Fprintf(&log, "Advance(%d) wm=%d\n", w, wm)
				gotOut, gotErr := a.Advance(w)
				wantOut, wantErr := n.advance(w)
				if gotErr != wantErr {
					fail("Advance(%d) err = %v, want %v", w, gotErr, wantErr)
				}
				if !reflect.DeepEqual(normOuts(gotOut), normOuts(wantOut)) {
					fail("Advance(%d) = %v, want %v", w, gotOut, wantOut)
				}
				fmt.Fprintf(&log, "  -> %d outputs, err=%v\n", len(gotOut), gotErr)
				continue
			}

			key := keys[rng.Intn(len(keys))]
			if rng.Intn(20) == 0 {
				key = "" // invalid
			}
			base := wm
			if base < 0 {
				base = 0
			}
			var ts int64
			switch rng.Intn(6) {
			case 0:
				ts = base + int64(rng.Intn(15))
			case 1:
				ts = base - int64(rng.Intn(8))
			case 2:
				ts = base
			case 3:
				ts = base - al
			case 4:
				ts = MaxParam + 1 // invalid
			default:
				ts = int64(rng.Intn(20))
			}
			val := int64(rng.Intn(41) - 20)
			if rng.Intn(30) == 0 {
				val = MaxVal + 1 // invalid
			}
			inserts++
			got, gotOut := a.Insert([]byte(key), ts, val)
			want, wantOut := n.insert(key, ts, val)
			fmt.Fprintf(&log, "Insert(%q,%d,%d) wm=%d wm-AL=%d -> %v %v\n",
				key, ts, val, wm, wm-al, got, gotOut)
			if got != want {
				fail("Insert(%q,%d,%d) = %v, want %v", key, ts, val, got, want)
			}
			if got == InsertReissued && !reflect.DeepEqual(gotOut, wantOut) {
				fail("Insert(%q,%d,%d) reissue = %+v, want %+v", key, ts, val, gotOut, wantOut)
			}
		}

		if got, want := a.Retained(), int64(len(n.rel)); got != want {
			fail("Retained() = %d, want %d", got, want)
		}
		if got, want := a.Seq(), n.seq; got != want {
			fail("Seq() = %d, want %d", got, want)
		}
		st := a.Stats()
		if st.Late != n.late || st.Reissued != n.reissued || st.Invalid != n.invalid ||
			st.Full != n.full || st.Released != n.released || st.Buffered != int64(len(n.buf)) {
			fail("Stats = %+v, naive = %+v", st, n)
		}
		if total := st.Invalid + st.Full + st.Late + st.Reissued + st.Buffered + st.Released; total != inserts {
			fail("insert classes sum = %d, want %d", total, inserts)
		}
		if seed < 3 {
			t.Logf("sequence log (inputs, outputs, classification basis wm/wm-AL):\n%s", log.String())
			t.Logf("seed=%d OK: inserts=%d stats=%+v", seed, inserts, st)
		}
	}
}

// runScript applies a deterministic pseudo-random script and records every
// observable result.
func runScript(seed int64) string {
	rng := rand.New(rand.NewSource(seed))
	a, err := New(3, 2, 8)
	if err != nil {
		panic(err)
	}
	var b strings.Builder
	keys := []string{"a", "b", "c"}
	for i := 0; i < 300; i++ {
		if rng.Intn(4) == 0 {
			w := a.WM() + int64(rng.Intn(8)) - 1
			outs, err := a.Advance(w)
			fmt.Fprintf(&b, "A(%d)=%v,%v\n", w, outs, err)
			continue
		}
		key := keys[rng.Intn(len(keys))]
		ts := a.WM() + int64(rng.Intn(12)) - 3
		val := int64(rng.Intn(41) - 20)
		res, o := a.Insert([]byte(key), ts, val)
		fmt.Fprintf(&b, "I(%s,%d,%d)=%v,%v\n", key, ts, val, res, o)
	}
	st := a.Stats()
	fmt.Fprintf(&b, "final wm=%d retained=%d seq=%d stats=%+v\n",
		a.WM(), a.Retained(), a.Seq(), st)
	return b.String()
}

// TestReplayDeterminism: the same operation sequence replays to identical
// outputs, watermark, buffer, retained count and late count.
func TestReplayDeterminism(t *testing.T) {
	for seed := int64(0); seed < 20; seed++ {
		if first, second := runScript(seed), runScript(seed); first != second {
			t.Fatalf("seed=%d: replay diverged\nfirst:\n%s\nsecond:\n%s", seed, first, second)
		}
	}
}

// TestConcurrent hammers one aggregator from many goroutines and checks the
// observable invariants: monotone watermark, buffer capacity, per-Advance
// output ordering, and the insert-class bookkeeping.
func TestConcurrent(t *testing.T) {
	a, err := New(5, 3, 1000)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	const inserters = 4
	const insertsEach = 5000

	var wg sync.WaitGroup
	var violations atomic.Int64
	var nextW atomic.Int64

	for g := 0; g < inserters; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(g)))
			key := fmt.Sprintf("k%d", g)
			for i := 0; i < insertsEach; i++ {
				a.Insert([]byte(key), int64(rng.Intn(300)), int64(rng.Intn(100)))
			}
		}(g)
	}
	for g := 0; g < 2; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 400; i++ {
				w := nextW.Add(1) - 1
				outs, err := a.Advance(w)
				if err == ErrWatermarkRegression {
					continue
				}
				if err != nil {
					violations.Add(1)
					continue
				}
				for j := 1; j < len(outs); j++ {
					if outs[j-1].Ts > outs[j].Ts {
						violations.Add(1)
					}
				}
				for _, o := range outs {
					if o.Ts > w {
						violations.Add(1)
					}
				}
			}
		}()
	}
	for g := 0; g < 2; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lastWM := int64(-1)
			for i := 0; i < 3000; i++ {
				if wm := a.WM(); wm < lastWM {
					violations.Add(1)
				} else {
					lastWM = wm
				}
				if a.Buffered() > 1000 {
					violations.Add(1)
				}
				_ = a.Retained()
				_ = a.Stats()
			}
		}()
	}
	wg.Wait()

	if v := violations.Load(); v != 0 {
		t.Fatalf("%d invariant violations under concurrency", v)
	}
	st := a.Stats()
	if total := st.Invalid + st.Full + st.Late + st.Reissued + st.Buffered + st.Released; total != inserters*insertsEach {
		t.Fatalf("insert classes sum = %d, want %d", total, inserters*insertsEach)
	}
	if a.Buffered() > 1000 {
		t.Fatalf("buffer holds %d rows, cap is 1000", a.Buffered())
	}
	t.Logf("final wm=%d retained=%d stats=%+v", a.WM(), a.Retained(), st)
}
