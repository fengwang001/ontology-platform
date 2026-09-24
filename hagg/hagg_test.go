package hagg

import (
	"math/rand"
	"reflect"
	"strconv"
	"testing"

	"ontology/hop"
)

type nEv struct {
	key       string
	ts, clock int64
}

func naive(p hop.Params, evs []nEv) (map[string]map[int64]int64, int64) { // brute-force reference: end>arrival clock admits; all-closed drops
	m := map[string]map[int64]int64{}
	var drop int64
	for _, e := range evs {
		ks := p.OpenKs(p.Ks(e.ts), e.clock)
		if len(ks) == 0 {
			drop++
		}
		for _, k := range ks {
			if m[e.key] == nil {
				m[e.key] = map[int64]int64{}
			}
			m[e.key][k]++
		}
	}
	return m, drop
}
func toMap(rs []Result) map[string]map[int64]int64 { // results keyed by (key,k)
	m := map[string]map[int64]int64{}
	for _, r := range rs {
		if m[r.Key] == nil {
			m[r.Key] = map[int64]int64{}
		}
		m[r.Key][r.K] = r.Count
	}
	return m
}

func TestNaiveReference(t *testing.T) { // invariant 1: any interleaving + Flush matches naive, per (key,window)
	type op struct {
		add       bool
		key       string
		ts, clock int64
	}
	fixed := []op{{true, "x", -5, 0}, {true, "x", 0, 0}, {true, "x", -1, 0}, {false, "", 0, 0}, {true, "x", -3, 0}, {true, "x", -13, 0}, {false, "", 0, 4}, {true, "x", 8, 0}, {false, "", 0, 12}}
	scen := [][]op{fixed} // table: the nine-step NOTES scenario plus random orders
	for seed := int64(0); seed < 30; seed++ {
		rnd := rand.New(rand.NewSource(seed))
		ops := make([]op, 300)
		for i := range ops {
			if rnd.Intn(3) == 0 {
				ops[i] = op{clock: int64(rnd.Intn(80)) - 20}
			} else {
				ops[i] = op{add: true, key: "k" + strconv.Itoa(rnd.Intn(6)), ts: int64(rnd.Intn(100)) - 30}
			}
		}
		scen = append(scen, ops)
	}
	for si, ops := range scen {
		a, _ := New(12, 4, 1_000_000)
		evs := []nEv{}
		clk := int64(-1 << 63)
		for _, o := range ops {
			if o.add {
				if err := a.Add(o.key, o.ts); err != nil {
					t.Fatal(err)
				}
				evs = append(evs, nEv{o.key, o.ts, clk})
			} else if o.clock >= clk { // replay only non-decreasing advances
				if _, err := a.Advance(o.clock); err != nil {
					t.Fatal(err)
				}
				clk = o.clock
			}
		}
		a.Flush()
		want, drop := naive(hop.Params{Size: 12, Slide: 4}, evs)
		if !reflect.DeepEqual(toMap(a.Results()), want) || a.Dropped() != drop {
			t.Fatalf("scenario %d differs from the naive reference", si)
		}
	}
}
func TestMembershipExact(t *testing.T) { // invariant 2: exactly open size/slide windows admit; -inf admits all
	a, _ := New(12, 4, 1000)
	for _, ts := range []int64{-5, 0, 100} { // 3 keys, disjoint windows
		if err := a.Add("k"+strconv.FormatInt(ts, 10), ts); err != nil {
			t.Fatal(err)
		}
	}
	if len(a.open) != 9 { // each event opens exactly size/slide = 3
		t.Fatalf("at -inf each event must open 3 windows, got %d", len(a.open))
	}
	if _, err := a.Advance(0); err != nil {
		t.Fatal(err)
	}
	before := len(a.open)
	if err := a.Add("late", -3); err != nil { // k=-3 closed; -2 and -1 admit it
		t.Fatal(err)
	}
	if len(a.open) != before+2 || a.Dropped() != 0 {
		t.Fatalf("partial-late event must enter exactly 2 windows, got %d", len(a.open)-before)
	}
	if err := a.Add("dead", -13); err != nil || a.Dropped() != 1 || len(a.open) != before+2 { // all windows closed: dropped, opens nothing
		t.Fatalf("all-closed event must drop once and open nothing, drop=%d", a.Dropped())
	}
}
func TestOrderAndUniqueness(t *testing.T) { // invariant 3: (end,key) ordered, (key,window) unique
	for seed := int64(0); seed < 10; seed++ { // table over random streams
		rnd := rand.New(rand.NewSource(seed + 77))
		a, _ := New(12, 4, 1_000_000)
		clk, total := int64(-1<<63), 0
		seen := map[[2]string]bool{}
		check := func(rs []Result) {
			for i, r := range rs {
				if i > 0 && (r.End < rs[i-1].End || r.End == rs[i-1].End && r.Key < rs[i-1].Key) {
					t.Fatalf("seed=%d batch not (end,key) ordered", seed)
				}
				id := [2]string{r.Key, strconv.FormatInt(r.K, 10)}
				if seen[id] {
					t.Fatalf("seed=%d window emitted twice %v", seed, id)
				}
				seen[id] = true
			}
			total += len(rs)
		}
		for i := 0; i < 200; i++ {
			if rnd.Intn(3) == 0 {
				tt := int64(rnd.Intn(60)) - 10
				if tt >= clk {
					rs, _ := a.Advance(tt)
					clk = tt
					check(rs)
				}
			} else if err := a.Add("k"+strconv.Itoa(rnd.Intn(5)), int64(rnd.Intn(90))-20); err != nil {
				t.Fatal(err)
			}
		}
		check(a.Flush())
		if len(a.Results()) != total {
			t.Fatal("cumulative Results length differs from emitted batches")
		}
	}
}
