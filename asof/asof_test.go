package asof

import (
	"fmt"
	"sync"
	"testing"

	"ontology/interval"
	"ontology/store"
)

func mustInterval(t *testing.T, start, end int64) interval.I {
	t.Helper()
	iv, err := interval.New(start, end)
	if err != nil {
		t.Fatal(err)
	}
	return iv
}

func TestSalaryCorrection(t *testing.T) {
	st := store.New()
	v := mustInterval(t, 2020, 2021)
	t1, _ := st.Write("salary", 100, v)
	t2, _ := st.Correct("salary", 120, v)
	q := New(st)
	cases := []struct {
		validAt, txAt int64
		want          int64
		found         bool
	}{
		{2020, t1, 100, true},    // 当时以为 100
		{2020, t2, 120, true},    // 更正后为 120
		{2021, t1, 0, false},     // 有效区间之外
		{2021, t2, 0, false},     // 有效区间之外
		{2020, t1 - 1, 0, false}, // 事务时刻过早
	}
	for _, c := range cases {
		t.Run(fmt.Sprintf("v%d_tx%d", c.validAt, c.txAt), func(t *testing.T) {
			got, found := q.Query("salary", c.validAt, c.txAt)
			if got != c.want || found != c.found {
				t.Fatalf("got (%d,%v), want (%d,%v)", got, found, c.want, c.found)
			}
		})
	}
}

func TestDeleteSemantics(t *testing.T) {
	st := store.New()
	txW, _ := st.Write("k", 42, mustInterval(t, 2000, 2030))
	txD, _ := st.Delete("k", 2020)
	txZ, _ := st.Write("zero", 0, mustInterval(t, 2000, 2030))
	q := New(st)
	cases := []struct {
		name          string
		key           string
		validAt, txAt int64
		want          int64
		found         bool
	}{
		{"删除前有效时刻仍可见", "k", 2019, txD, 42, true},
		{"删除点后不存在", "k", 2021, txD, 0, false},
		{"删除前的事务时刻不知删除", "k", 2021, txW, 42, true},
		{"零值与不存在可区分-零值", "zero", 2010, txZ, 0, true},
		{"零值与不存在可区分-不存在", "k", 2021, txD, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, found := q.Query(c.key, c.validAt, c.txAt)
			if got != c.want || found != c.found {
				t.Fatalf("got (%d,%v), want (%d,%v)", got, found, c.want, c.found)
			}
		})
	}
}

func TestQueryBoundaries(t *testing.T) {
	st := store.New()
	tx, _ := st.Write("k", 7, mustInterval(t, 10, 20))
	q := New(st)
	cases := []struct {
		validAt int64
		found   bool
	}{
		{10, true},  // 起点命中
		{20, false}, // 终点不命中
		{19, true},
		{9, false},
	}
	for _, c := range cases {
		if _, found := q.Query("k", c.validAt, tx); found != c.found {
			t.Errorf("validAt=%d found=%v, want %v", c.validAt, found, c.found)
		}
	}
}

func TestThreeCorrectionsThreeValues(t *testing.T) {
	st := store.New()
	v := mustInterval(t, 2020, 2021)
	tx1, _ := st.Write("k", 1, v)
	tx2, _ := st.Correct("k", 2, v)
	tx3, _ := st.Correct("k", 3, v)
	q := New(st)
	cases := []struct {
		txAt int64
		want int64
	}{
		{tx1, 1},
		{tx2, 2},
		{tx3, 3},
	}
	for _, c := range cases {
		if got, found := q.Query("k", 2020, c.txAt); !found || got != c.want {
			t.Errorf("tx=%d got (%d,%v), want (%d,true)", c.txAt, got, found, c.want)
		}
	}
}

func TestCheckedBound(t *testing.T) {
	st := store.New()
	v := mustInterval(t, 2020, 2021)
	for k := 0; k < 1000; k++ {
		key := fmt.Sprintf("key-%d", k)
		if _, err := st.Write(key, 0, v); err != nil {
			t.Fatal(err)
		}
		for i := 1; i < 10; i++ {
			if _, err := st.Correct(key, int64(i), v); err != nil {
				t.Fatal(err)
			}
		}
	}
	q := New(st)
	if _, found := q.Query("key-500", 2020, 1<<60); !found {
		t.Fatal("want found")
	}
	if got, bound := q.Checked(), int64(4*10); got > bound {
		t.Fatalf("checked=%d > bound=%d", got, bound)
	}
}

func TestQueryIsPure(t *testing.T) {
	st := store.New()
	v := mustInterval(t, 2020, 2021)
	tx, _ := st.Write("k", 5, v)
	st.Correct("k", 6, v)
	q := New(st)
	want, wantFound := q.Query("k", 2020, tx)
	wantChecked := q.Checked()
	for i := 0; i < 1000; i++ {
		got, found := q.Query("k", 2020, tx)
		if got != want || found != wantFound || q.Checked() != wantChecked {
			t.Fatalf("iter %d: (%d,%v,%d) != (%d,%v,%d)",
				i, got, found, q.Checked(), want, wantFound, wantChecked)
		}
	}
}

func TestQueryConcurrentWithWrites(t *testing.T) {
	st := store.New()
	v := mustInterval(t, 2020, 2021)
	tx, _ := st.Write("hot", 0, v)
	q := New(st)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(2)
		go func(base int64) {
			defer wg.Done()
			for i := int64(0); i < 100; i++ {
				st.Correct("hot", base+i, v)
			}
		}(int64(g) * 1000)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				q.Query("hot", 2020, tx)
			}
		}()
	}
	wg.Wait()
	if err := st.CheckDisjoint(); err != nil {
		t.Fatal(err)
	}
}
