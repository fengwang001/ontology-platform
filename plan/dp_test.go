package plan

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"ontology/catalog"
	"ontology/stats"
)

// chainView 构造 n 张表的链式谓词视图：T0-T1-...-T(n-1)。
func chainView(n int) *catalog.View {
	v := &catalog.View{}
	for i := 0; i < n; i++ {
		v.Tables = append(v.Tables, catalog.TableInfo{
			Name: fmt.Sprintf("T%02d", i), Rows: float64(1000 * (i + 1))})
	}
	for i := 0; i+1 < n; i++ {
		l := catalog.ColRef{Table: fmt.Sprintf("T%02d", i), Column: "k"}
		r := catalog.ColRef{Table: fmt.Sprintf("T%02d", i+1), Column: "k"}
		v.Preds = append(v.Preds, catalog.PredInfo{
			Pred: catalog.Predicate{Left: l, Right: r}, Sel: 0.01})
	}
	return v
}

func factorial(n uint64) uint64 {
	f := uint64(1)
	for i := uint64(2); i <= n; i++ {
		f *= i
	}
	return f
}

func TestEnumerationComplexity(t *testing.T) {
	const n = 12
	res, err := Select(chainView(n))
	if err != nil {
		t.Fatal(err)
	}
	fact := factorial(n)
	cases := []struct {
		name string
		got  uint64
		cap  uint64
	}{
		{"subsets within 2^n", res.Subsets(), 1 << n},
		{"partitions within 3^n", res.Partitions(), 531441}, // 3^12
		{"subsets far below n!", res.Subsets() * 1000, fact},
		{"partitions far below n!", res.Partitions() * 100, fact},
	}
	for _, tc := range cases {
		if tc.got > tc.cap {
			t.Errorf("%s: got %d > cap %d", tc.name, tc.got, tc.cap)
		}
	}
	t.Logf("n=%d subsets=%d partitions=%d n!=%d", n, res.Subsets(), res.Partitions(), fact)
}

func TestTooManyTables(t *testing.T) {
	v := chainView(20)
	_, err := Select(v)
	if !errors.Is(err, ErrTooManyTables) {
		t.Fatalf("err=%v, want errors.Is ErrTooManyTables", err)
	}
	// 超限必须在枚举前拒绝：内存分配有界（不得出现 2^20 量级的状态表）。
	allocs := testing.AllocsPerRun(20, func() {
		if _, err := Select(v); !errors.Is(err, ErrTooManyTables) {
			t.Fatal("expected ErrTooManyTables")
		}
	})
	if allocs > 100 {
		t.Errorf("allocs=%v, want bounded small constant", allocs)
	}
}

func TestConcurrentSelect(t *testing.T) {
	v := chainView(10)
	res, err := Select(v)
	if err != nil {
		t.Fatal(err)
	}
	want := res.Best.String()
	var wg sync.WaitGroup
	for g := 0; g < 32; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				r, err := Select(v)
				if err != nil {
					t.Error(err)
					return
				}
				if got := r.Best.String(); got != want {
					t.Errorf("got %s, want %s", got, want)
					return
				}
			}
		}()
	}
	wg.Wait()
}

func TestNoRowDataAccess(t *testing.T) {
	before := stats.RowDataReads()
	if _, err := Select(chainView(8)); err != nil {
		t.Fatal(err)
	}
	if got := stats.RowDataReads(); got != before {
		t.Errorf("row data accessed during planning: %d reads", got-before)
	}
}
