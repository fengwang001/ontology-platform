package asof

import (
	"fmt"
	"sync"
	"testing"

	"ontology/interval"
	"ontology/store"
)

func mustInterval(t *testing.T, start, end int64) interval.Interval {
	t.Helper()
	iv, err := interval.New(start, end)
	if err != nil {
		t.Fatal(err)
	}
	return iv
}

func salaryStore(t *testing.T) (*store.Store, int64, int64) {
	t.Helper()
	s := store.New()
	sal := mustInterval(t, 2020, 2021)
	tx1, err := s.Put("alice", 100, sal)
	if err != nil {
		t.Fatal(err)
	}
	tx2, err := s.Correct("alice", 120, sal)
	if err != nil {
		t.Fatal(err)
	}
	return s, tx1, tx2
}

func TestSalaryCorrection(t *testing.T) {
	s, tx1, tx2 := salaryStore(t)
	cases := []struct {
		validAt, txAt int64
		want          int64
		found         bool
	}{
		{2020, tx1, 100, true},
		{2020, tx2, 120, true},
		{2022, tx1, 0, false},
		{2022, tx2, 0, false},
	}
	for _, c := range cases {
		r, ok := Query(s, "alice", c.validAt, c.txAt)
		if ok != c.found || (ok && r.Value != c.want) {
			t.Errorf("Query(2020-ish=%d, tx=%d) = (%v, %v), want value %d found %v",
				c.validAt, c.txAt, r.Value, ok, c.want, c.found)
		}
	}
}

func TestDeleteSemantics(t *testing.T) {
	s := store.New()
	span := mustInterval(t, 2020, 2030)
	if _, err := s.Put("bob", 77, span); err != nil {
		t.Fatal(err)
	}
	delTx, err := s.Delete("bob", 2025)
	if err != nil {
		t.Fatal(err)
	}
	if r, ok := Query(s, "bob", 2020, delTx); !ok || r.Value != 77 {
		t.Fatalf("history before delete should be visible, got %v %v", r, ok)
	}
	if _, ok := Query(s, "bob", 2026, delTx); ok {
		t.Fatal("after delete point should be absent")
	}
	zs := store.New()
	ztx, _ := zs.Put("zero", 0, span)
	if r, ok := Query(zs, "zero", 2020, ztx); !ok || r.Value != 0 {
		t.Fatal("zero value must be distinguishable from absent")
	}
}

func TestBoundaryCutPoints(t *testing.T) {
	s := store.New()
	iv := mustInterval(t, 10, 20)
	tx, _ := s.Put("k", 5, iv)
	for at := int64(9); at <= 21; at++ {
		_, ok := Query(s, "k", at, tx)
		if want := at >= 10 && at < 20; ok != want {
			t.Errorf("Query(valid=%d) found=%v, want %v", at, ok, want)
		}
	}
	if _, ok := Query(s, "k", 10, tx-1); ok {
		t.Error("tx earlier than first record should be absent")
	}
}

func TestThreeCorrections(t *testing.T) {
	s := store.New()
	iv := mustInterval(t, 2020, 2021)
	tx1, _ := s.Put("k", 100, iv)
	tx2, _ := s.Correct("k", 120, iv)
	tx3, _ := s.Correct("k", 130, iv)
	cases := []struct {
		txAt int64
		want int64
	}{{tx1, 100}, {tx2, 120}, {tx3, 130}}
	for _, c := range cases {
		if r, ok := Query(s, "k", 2020, c.txAt); !ok || r.Value != c.want {
			t.Errorf("tx=%d: got %v %v, want %d", c.txAt, r.Value, ok, c.want)
		}
	}
}

func bigStore(t *testing.T) *store.Store {
	t.Helper()
	s := store.New()
	iv := mustInterval(t, 2020, 2021)
	for i := 0; i < 1000; i++ {
		key := fmt.Sprintf("k%d", i)
		if _, err := s.Put(key, 0, iv); err != nil {
			t.Fatal(err)
		}
		for v := 1; v < 10; v++ {
			if _, err := s.Correct(key, int64(v), iv); err != nil {
				t.Fatal(err)
			}
		}
	}
	return s
}

func TestQueryDeterministic(t *testing.T) {
	s := bigStore(t)
	first, ok := Query(s, "k500", 2020, 10000)
	if !ok {
		t.Fatal("expected hit")
	}
	wantChecked := Checked()
	for i := 0; i < 1000; i++ {
		r, ok := Query(s, "k500", 2020, 10000)
		if !ok || r != first || Checked() != wantChecked {
			t.Fatalf("iteration %d not deterministic", i)
		}
	}
}

func TestComplexityBounds(t *testing.T) {
	s := bigStore(t)
	if _, ok := Query(s, "k999", 2020, 10000); !ok {
		t.Fatal("expected hit")
	}
	if got := Checked(); got > 4*10 {
		t.Fatalf("point query checked %d records, bound %d", got, 4*10)
	}
	if _, err := s.Correct("k999", 99, mustInterval(t, 2020, 2021)); err != nil {
		t.Fatal(err)
	}
	if got := s.Touched(); got > 10+2 {
		t.Fatalf("correct touched %d records, bound %d", got, 10+2)
	}
}

func TestConcurrentQueryAndWrite(t *testing.T) {
	s := store.New()
	iv := mustInterval(t, 2020, 2021)
	if _, err := s.Put("k", 0, iv); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(2)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				_, _ = s.Correct("k", int64(g*200+i), iv)
			}
		}(g)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				_, _ = Query(s, "k", 2020, int64(i))
			}
		}()
	}
	wg.Wait()
	if err := s.CheckInvariant(); err != nil {
		t.Fatal(err)
	}
}
