package query

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"

	"ontology/ival"
	"ontology/tree"
)

// TestOrderIndependent 同一份多重集按不同插入顺序构建，查询逐位相同。
func TestOrderIndependent(t *testing.T) {
	base := []ival.Interval{
		iv(1, 3), iv(3, 5), iv(2, 6), iv(2, 6), iv(0, 2),
		iv(7, 8), iv(2, 6), iv(4, 10), iv(0, 2),
	}
	orders := [][]int{
		{0, 1, 2, 3, 4, 5, 6, 7, 8},
		{8, 7, 6, 5, 4, 3, 2, 1, 0},
		{4, 2, 8, 6, 0, 3, 7, 1, 5},
	}
	queries := []ival.Interval{iv(0, 10), iv(2, 4), iv(3, 3), iv(5, 8)}
	var ref []ival.Interval
	for oi, ord := range orders {
		tr := tree.New()
		for _, idx := range ord {
			if err := tr.Insert(base[idx]); err != nil {
				t.Fatal(err)
			}
		}
		e := New(tr)
		var got []ival.Interval
		for _, q := range queries {
			r, err := e.Overlap(q)
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, r...)
		}
		if oi == 0 {
			ref = got
			continue
		}
		if !reflect.DeepEqual(ref, got) {
			t.Fatalf("order %d differs:\n%v\n%v", oi, ref, got)
		}
	}
}

// TestRandomMatchesNaive query 层固定种子对照朴素扫描。
func TestRandomMatchesNaive(t *testing.T) {
	for _, seed := range []int64{7, 77} {
		t.Run(fmt.Sprintf("seed%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			tr := tree.New()
			e := New(tr)
			var all []ival.Interval
			naive := func(q ival.Interval) []ival.Interval {
				var out []ival.Interval
				for _, x := range all {
					if x.Overlaps(q) {
						out = append(out, x)
					}
				}
				return sortedKey(out)
			}
			for k := 0; k < 500; k++ {
				l := rng.Int63n(50)
				x := iv(l, l+rng.Int63n(12))
				if rng.Intn(3) == 0 && len(all) > 0 {
					v := all[rng.Intn(len(all))]
					if err := tr.Delete(v); err != nil {
						t.Fatal(err)
					}
					all = removeFirst(all, v)
				} else {
					if err := tr.Insert(x); err != nil {
						t.Fatal(err)
					}
					all = append(all, x)
				}
				ql := rng.Int63n(60)
				qr := ql + rng.Int63n(12)
				q := iv(ql, qr)
				got, err := e.Overlap(q)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got, naive(q)) {
					t.Fatalf("mismatch at step %d q=%v", k, q)
				}
				if err := tr.SelfCheck(); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func removeFirst(s []ival.Interval, v ival.Interval) []ival.Interval {
	for i := range s {
		if s[i] == v {
			return append(s[:i], s[i+1:]...)
		}
	}
	return s
}

// buildDisjoint 插入 N 个互不重叠区间 [2k,2k+1)。
func buildDisjoint(t *testing.T, n int) (*Engine, []ival.Interval) {
	t.Helper()
	tr := tree.New(tree.WithMaxIntervals(n + 1))
	data := make([]ival.Interval, n)
	for k := 0; k < n; k++ {
		data[k] = iv(int64(2*k), int64(2*k+1))
		if err := tr.Insert(data[k]); err != nil {
			t.Fatal(err)
		}
	}
	if err := tr.SelfCheck(); err != nil {
		t.Fatal(err)
	}
	return New(tr), data
}

// TestStabVisitSublinear 命中一个区间的点查访问量必须只随树高增长。
func TestStabVisitSublinear(t *testing.T) {
	cases := []struct {
		n     int
		bound uint64
	}{
		{1000, 100},
		{100_000, 100},
	}
	for _, c := range cases {
		t.Run(fmt.Sprintf("n%d", c.n), func(t *testing.T) {
			e, data := buildDisjoint(t, c.n)
			// 点落在最后一个区间内，只命中它一个。
			x := data[c.n-1].L
			got := e.Stab(x)
			if len(got) != 1 {
				t.Fatalf("want exactly 1 hit, got %d", len(got))
			}
			v := e.visits.Load()
			t.Logf("N=%d 单点命中访问节点数=%d", c.n, v)
			if v > c.bound {
				t.Fatalf("visits=%d exceeds bound %d (疑似线性扫描)", v, c.bound)
			}
		})
	}
}

// TestOverlapVisitProportionalToHits 命中 M 个时访问量应 ~ M+树高，而非 N。
func TestOverlapVisitProportionalToHits(t *testing.T) {
	const n = 100_000
	e, _ := buildDisjoint(t, n)
	// 查询覆盖最中间 500 个区间，且左右大片区域被 MaxR 剪枝。
	const m = 500
	lo := int64(2 * (n/2 - m/2))
	// 右端点开：取第 (n/2+m/2) 个区间的左端点，恰好覆盖 m 个区间。
	hi := int64(2 * (n/2 + m/2))
	got, err := e.Overlap(iv(lo, hi))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != m {
		t.Fatalf("hits=%d want %d", len(got), m)
	}
	v := e.visits.Load()
	bound := uint64(4*m + 100)
	t.Logf("N=%d 命中 M=%d 访问节点数=%d（上限 %d）", n, m, v, bound)
	if v > bound {
		t.Fatalf("visits=%d 与 N 同量级，应约为 M+树高 (<=%d)", v, bound)
	}
}
