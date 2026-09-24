package join

import (
	"fmt"
	"math"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

func pi(v int64) *int64                    { return &v }
func ins(k string, lv int64, rv *int64) Op { return Op{Insert: true, K: k, LV: lv, RV: rv} }
func del(k string, lv int64, rv *int64) Op { return Op{K: k, LV: lv, RV: rv} }
func batchView(lefts, rights map[string]int64) []Row {
	keys := make([]string, 0, len(lefts))
	for k := range lefts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	rows := make([]Row, 0, len(keys))
	for _, k := range keys {
		var rv *int64
		if x, ok := rights[k]; ok {
			rv = &x
		}
		rows = append(rows, Row{K: k, LV: lefts[k], RV: rv})
	}
	return rows
}
func step(t *testing.T, r *rand.Rand, v *View, lefts, rights map[string]int64) []Op {
	key := fmt.Sprintf("k%02d", r.Intn(20))
	var ops []Op
	var err error
	switch r.Intn(4) {
	case 0:
		lv := r.Int63n(1000)
		ops, err = v.PutL(key, lv)
		lefts[key] = lv
	case 1:
		rv := r.Int63n(1000)
		ops, err = v.PutR(key, rv)
		rights[key] = rv
	case 2:
		if _, ok := lefts[key]; !ok {
			return nil
		}
		ops, err = v.DelL(key)
		delete(lefts, key)
	default:
		if _, ok := rights[key]; !ok {
			return nil
		}
		ops, err = v.DelR(key)
		delete(rights, key)
	}
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return ops
}

func TestEightSteps(t *testing.T) {
	v := New()
	calls := []struct {
		run  func() ([]Op, error)
		want []Op
	}{
		{func() ([]Op, error) { return v.PutL("k1", 10) }, []Op{ins("k1", 10, nil)}},
		{func() ([]Op, error) { return v.PutL("k2", 20) }, []Op{ins("k2", 20, nil)}},
		{func() ([]Op, error) { return v.PutR("k2", 200) }, []Op{del("k2", 20, nil), ins("k2", 20, pi(200))}},
		{func() ([]Op, error) { return v.PutR("k1", 100) }, []Op{del("k1", 10, nil), ins("k1", 10, pi(100))}},
		{func() ([]Op, error) { return v.PutR("k3", 300) }, nil},
		{func() ([]Op, error) { return v.PutL("k3", 30) }, []Op{ins("k3", 30, pi(300))}},
		{func() ([]Op, error) { return v.PutR("k2", 250) }, []Op{del("k2", 20, pi(200)), ins("k2", 20, pi(250))}},
		{func() ([]Op, error) { return v.DelL("k1") }, []Op{del("k1", 10, pi(100))}},
	}
	for i, c := range calls {
		if got, err := c.run(); err != nil || !reflect.DeepEqual(got, c.want) {
			t.Fatalf("step %d: got %v, %v", i+1, got, err)
		}
	}
	want := []Row{{K: "k2", LV: 20, RV: pi(250)}, {K: "k3", LV: 30, RV: pi(300)}}
	if got := v.View(); !reflect.DeepEqual(got, want) {
		t.Fatalf("view %v != %v", got, want)
	}
}

func TestBatchRecomputeRandom(t *testing.T) {
	for seed := int64(0); seed < 10; seed++ {
		v := New()
		lefts, rights := map[string]int64{}, map[string]int64{}
		r := rand.New(rand.NewSource(seed))
		for i := 0; i < 200; i++ {
			step(t, r, v, lefts, rights)
			if got, want := v.View(), batchView(lefts, rights); !reflect.DeepEqual(got, want) {
				t.Fatalf("seed %d op %d: view %v != batch %v", seed, i, got, want)
			}
		}
	}
}

func TestLogPrefixRandom(t *testing.T) {
	for seed := int64(0); seed < 10; seed++ {
		v := New()
		lefts, rights := map[string]int64{}, map[string]int64{}
		down := map[string]Row{}
		r := rand.New(rand.NewSource(seed))
		for i := 0; i < 200; i++ {
			for _, o := range step(t, r, v, lefts, rights) {
				if o.Insert {
					if _, dup := down[o.K]; dup {
						t.Fatalf("seed %d op %d: insert duplicates %q", seed, i, o.K)
					}
					down[o.K] = Row{K: o.K, LV: o.LV, RV: o.RV}
				} else {
					if cur, ok := down[o.K]; !ok || !reflect.DeepEqual(cur, Row{K: o.K, LV: o.LV, RV: o.RV}) {
						t.Fatalf("seed %d op %d: retract mismatches %q", seed, i, o.K)
					}
					delete(down, o.K)
				}
			}
		}
		want := batchView(lefts, rights)
		if got := v.View(); !reflect.DeepEqual(got, want) || len(down) != len(want) {
			t.Fatalf("seed %d: downstream/view/batch diverge", seed)
		}
	}
}

func TestChecksLogarithmic(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		v := New()
		for i := 0; i < m; i++ {
			_, _ = v.PutL(fmt.Sprintf("k%05d", i), int64(i))
		}
		if _, err := v.PutR(fmt.Sprintf("k%05d", m/2), 7); err != nil {
			t.Fatal(err)
		}
		if got, bound := v.checks, 2*int(math.Log2(float64(m)))+3; got > bound {
			t.Fatalf("m=%d: checks=%d > bound=%d", m, got, bound)
		}
	}
}
