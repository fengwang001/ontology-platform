package api

import (
	"errors"
	"math/rand"
	"slices"
	"sync"
	"testing"
)

type step struct {
	kind   byte // u upsert, d delete, f feed, w watermark
	key, v string
	x      int64
	want   error
	out    []Joined
}

func run(t *testing.T, j *Joiner, steps []step) {
	for i, s := range steps {
		call := map[byte]func() ([]Joined, error){
			'u': func() ([]Joined, error) { return nil, j.Upsert(s.key, s.x, s.v) },
			'd': func() ([]Joined, error) { return nil, j.Delete(s.key, s.x) },
			'f': func() ([]Joined, error) { return j.Feed(s.key, s.x) },
			'w': func() ([]Joined, error) { return j.Watermark(s.x) },
		}
		o, err := call[s.kind]()
		if !errors.Is(err, s.want) || !slices.Equal(o, s.out) {
			t.Fatalf("step %d: out=%v err=%v, want %v/%v", i, o, err, s.out, s.want)
		}
	}
}
func write(t *testing.T, j *Joiner, tab map[string]map[int64]ent, key string, vf int64, tomb bool) {
	e, err := ent{val: "v"}, j.Upsert(key, vf, "v")
	if tomb {
		e, err = ent{tomb: true}, j.Delete(key, vf)
	}
	if err != nil {
		t.Fatal(err)
	}
	tab[key][vf] = e
}
func checkAll(t *testing.T, out []Joined, tab map[string]map[int64]ent, evs []Joined, n int) {
	if len(out) != n {
		t.Fatalf("%d outputs, want %d", len(out), n)
	}
	seen := map[int]bool{}
	for _, r := range out {
		val, found := naiveAsOf(tab, r.Key, r.TS)
		bad := seen[r.Seq] || r.Seq < 0 || r.Seq >= n || r.Found != found || r.Value != val ||
			(evs != nil && (r.Key != evs[r.Seq].Key || r.TS != evs[r.Seq].TS))
		if bad {
			t.Fatalf("seq %d: %+v vs naive (%q,%v)", r.Seq, r, val, found)
		}
		seen[r.Seq] = true
	}
}
func TestThirteenSteps(t *testing.T) {
	if !New(10).SelfCheck() {
		t.Fatal("SelfCheck")
	}
	run(t, New(10), []step{
		{'u', "k", "A", 10, nil, nil}, {'f', "k", "", 12, nil, nil},
		{'u', "k", "B", 20, nil, nil}, {'f', "k", "", 25, nil, nil},
		{'w', "k", "", 11, nil, nil}, {'u', "k", "C", 12, nil, nil},
		{'f', "k", "", 22, nil, nil}, {'d', "k", "", 22, nil, nil},
		{'w', "k", "", 22, nil, []Joined{{Key: "k", Value: "C", TS: 12, Seq: 0, Found: true}, {Key: "k", TS: 22, Seq: 2}}},
		{'u', "k", "X", 22, ErrLateVersion, nil},
		{'f', "k", "", 8, nil, []Joined{{Key: "k", TS: 8, Seq: 3}}},
		{'u', "k", "D", 30, nil, nil},
		{'w', "k", "", 30, nil, []Joined{{Key: "k", TS: 25, Seq: 1}}},
	})
}
func TestRejectedLeavesNoState(t *testing.T) {
	if errors.Is(ErrEmptyKey, ErrLateVersion) || errors.Is(ErrEmptyKey, ErrWatermarkBack) ||
		errors.Is(ErrEmptyKey, ErrBufferFull) || errors.Is(ErrLateVersion, ErrWatermarkBack) ||
		errors.Is(ErrLateVersion, ErrBufferFull) || errors.Is(ErrWatermarkBack, ErrBufferFull) {
		t.Fatal("sentinels not distinguishable")
	}
	j := New(1) // buffer of 1: second buffered event overflows
	run(t, j, []step{
		{'f', "", "", 1, ErrEmptyKey, nil}, {'u', "", "x", 1, ErrEmptyKey, nil},
		{'f', "k", "", 10, nil, nil}, {'f', "k", "", 11, ErrBufferFull, nil},
		{'w', "k", "", 5, nil, nil}, {'u', "k", "x", 5, ErrLateVersion, nil},
		{'w', "k", "", 4, ErrWatermarkBack, nil}, {'w', "k", "", 5, nil, nil},
	})
	j.Flush()
	if err, o := j.Upsert("k", 100, "y"), j.Outputs(); !errors.Is(err, ErrLateVersion) || len(o) != 1 || o[0].Seq != 0 || o[0].Found {
		t.Fatalf("post-flush: err=%v outputs=%v", err, o)
	}
}
func TestNaiveDifferential(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for it := 0; it < 30; it++ {
		j := New(1000)
		tab := map[string]map[int64]ent{"a": {}, "b": {}, "c": {}}
		var evs []Joined
		vwm, wmSet := int64(0), false
		for op := 0; op < 50; op++ {
			key := []string{"a", "b", "c"}[rng.Intn(3)]
			var o []Joined
			var err error
			switch rng.Intn(4) {
			case 0, 1: // vwm is 0 before the first watermark, so +vwm is safe
				write(t, j, tab, key, rng.Int63n(6)+1+vwm, rng.Intn(2) == 1)
			case 2:
				ts := rng.Int63n(120) - 20
				o, err = j.Feed(key, ts)
				evs = append(evs, Joined{Key: key, TS: ts})
			default:
				w := rng.Int63n(5) + vwm
				o, err = j.Watermark(w)
				wmSet, vwm = true, w
			}
			if err != nil {
				t.Fatal(err)
			}
			for i, r := range o {
				if (i > 0 && (r.TS < o[i-1].TS || r.TS == o[i-1].TS && r.Seq <= o[i-1].Seq)) || (wmSet && r.TS > vwm) {
					t.Fatalf("bad release: %v (vwm %d)", o, vwm)
				}
			}
		}
		j.Flush()
		checkAll(t, j.Outputs(), tab, evs, len(evs))
	}
}
func TestConcurrentFeed(t *testing.T) {
	j := New(1 << 16)
	tab := map[string]map[int64]ent{"a": {}, "b": {}, "c": {}, "d": {}}
	for k := 0; k < 16; k++ {
		write(t, j, tab, []string{"a", "b", "c", "d"}[k/4], int64(k%4*10), (k/4+k%4)%3 == 0)
	}
	const G, N = 8, 64
	var wg sync.WaitGroup
	for g := 0; g < G; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < N; i++ {
				if _, err := j.Feed([]string{"a", "b", "c", "d"}[(g*N+i)%4], int64((g*N+i)*7%100)); err != nil {
					t.Error(err)
				}
			}
		}(g)
	}
	wg.Wait()
	j.Flush()
	checkAll(t, j.Outputs(), tab, nil, G*N)
}
