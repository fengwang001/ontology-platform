package djoin

import (
	"math/rand"
	"ontology/rel"
	"reflect"
	"sync"
	"testing"
)

func genBatch(rng *rand.Rand, live tab) []rel.Row {
	out, deleted := []rel.Row{}, false
	for i := 0; i < 3; i++ {
		k, v := int64(rng.Intn(8)), string(rune('a'+rng.Intn(3)))
		sign := 1
		if !deleted && rng.Intn(3) == 0 && live[k][v] > 0 {
			sign, deleted = -1, true
		}
		out = append(out, rr(k, v, sign))
	}
	return out
}

func bump(t *testing.T, m map[jKey]int, g jKey, d int) {
	m[g] += d
	if m[g] < 0 {
		t.Fatalf("negative multiplicity at %v", g)
	}
	if m[g] == 0 {
		delete(m, g)
	}
}

// checkSeq feeds section-3 + 120 random batches, verifying I1/I2/I3 per batch.
func checkSeq(t *testing.T, seed int64) {
	e, rng, nr, ns := New(1<<20), rand.New(rand.NewSource(seed)), tab{}, tab{}
	down := map[jKey]int{}
	sdr, sds, _ := section3()
	for i := 0; i < 123; i++ {
		dR, dS := genBatch(rng, nr), genBatch(rng, ns)
		if i < 3 {
			dR, dS = sdr[i], sds[i]
		}
		old := naiveJoin(nr, ns)
		out, err := e.Feed(dR, dS)
		if err != nil {
			t.Fatalf("batch %d: %v", i, err)
		}
		applyNet(nr, dR)
		applyNet(ns, dS)
		for _, d := range out {
			g := jKey{d.K, d.A, d.B}
			bump(t, old, g, d.Mult)
			bump(t, down, g, d.Mult)
		}
		if !reflect.DeepEqual(old, naiveJoin(nr, ns)) || !reflect.DeepEqual(vm(e.View()), naiveJoin(nr, ns)) {
			t.Fatalf("batch %d: delta/view mismatch", i)
		}
	}
}
func TestViewEqualsRecompute(t *testing.T) { checkSeq(t, 1); checkSeq(t, 2) }
func TestDeltaMatchesNaive(t *testing.T)   { checkSeq(t, 3); checkSeq(t, 4) }
func TestPrefixNonNegative(t *testing.T)   { checkSeq(t, 5); checkSeq(t, 6) }
func TestRejectionAtomic(t *testing.T) {
	e := New(1)
	if _, err := e.Feed([]rel.Row{rr(1, "a", 1)}, []rel.Row{rr(1, "p", 1)}); err != nil {
		t.Fatal(err)
	}
	before := e.View()
	for _, c := range []struct {
		dR, dS []rel.Row
		want   error
	}{
		{[]rel.Row{rr(2, "b", 0)}, nil, ErrInvalidChange},
		{[]rel.Row{rr(2, "", 1)}, nil, ErrInvalidChange},
		{[]rel.Row{rr(1, "a", -1), rr(1, "a", -1)}, nil, ErrDeleteMissing},
		{[]rel.Row{rr(2, "x", 1)}, []rel.Row{rr(2, "p", 1)}, ErrViewLimit},
	} {
		if _, err := e.Feed(c.dR, c.dS); err != c.want {
			t.Errorf("got %v want %v", err, c.want)
		}
		if !reflect.DeepEqual(e.View(), before) {
			t.Error("state changed after rejection")
		}
	}
	if _, err := e.Feed(nil, []rel.Row{rr(1, "p", -1)}); err != nil { // still usable
		t.Error(err)
	}
}
func TestProbeCountBounded(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		e := New(1 << 21)
		dr, ds := make([]rel.Row, 0, m), make([]rel.Row, 0, m)
		for i := 0; i < m; i++ {
			dr = append(dr, rr(int64(i), "a", 1))
			ds = append(ds, rr(int64(i), "b", 1))
		}
		_, err1 := e.Feed(dr, ds)
		_, err2 := e.Feed([]rel.Row{rr(0, "a2", 1)}, nil) // K=0 matches exactly 1 S row
		if err1 != nil || err2 != nil {
			t.Fatal(err1, err2)
		}
		if e.probe > 2 {
			t.Errorf("m=%d: probe=%d grows with table size", m, e.probe)
		}
	}
}
func TestConcurrentFeeds(t *testing.T) {
	e, nr, ns := New(1<<20), tab{}, tab{}
	stop, done := make(chan struct{}), make(chan struct{})
	go func() { // every snapshot must be consistent: no half batch, no negatives
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			for _, d := range e.View() {
				if d.Mult != 1 || d.A != "a" || d.B != "b" {
					t.Errorf("bad snapshot tuple %+v", d)
				}
			}
		}
	}()
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		k := int64(g)
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := e.Feed([]rel.Row{rr(k, "a", 1)}, []rel.Row{rr(k, "b", 1)}); err != nil {
				t.Error(err)
			}
		}()
		nr[k], ns[k] = map[string]int{"a": 1}, map[string]int{"b": 1}
	}
	wg.Wait()
	close(stop)
	<-done
	if !reflect.DeepEqual(vm(e.View()), naiveJoin(nr, ns)) {
		t.Error("final view != recompute")
	}
}
func TestSelfCheck(t *testing.T) {
	if err := New(1).SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
