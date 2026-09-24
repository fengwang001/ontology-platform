package store

import (
	"errors"
	"math/rand"
	"sync"
	"testing"

	"ontology/interval"
)

func mustInterval(t *testing.T, start, end int64) interval.I {
	t.Helper()
	iv, err := interval.New(start, end)
	if err != nil {
		t.Fatal(err)
	}
	return iv
}

func TestWriteErrors(t *testing.T) {
	s := New()
	v := mustInterval(t, 2020, 2021)
	if _, err := s.Write("k", 100, v); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		key  string
		iv   interval.I
		want error
	}{
		{"overlap same key", "k", mustInterval(t, 2020, 2021), ErrOverlap},
		{"overlap partial", "k", mustInterval(t, 2020, 2022), ErrOverlap},
		{"empty interval", "k", interval.I{Start: 5, End: 5}, interval.ErrEmpty},
		{"other key ok", "other", mustInterval(t, 2020, 2021), nil},
		{"disjoint same key ok", "k", mustInterval(t, 2021, 2022), nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := s.Write(c.key, 1, c.iv)
			if !errors.Is(err, c.want) {
				t.Fatalf("err=%v, want %v", err, c.want)
			}
		})
	}
}

func TestCorrectAppendsAndCloses(t *testing.T) {
	s := New()
	v := mustInterval(t, 2020, 2021)
	t1, err := s.Write("salary", 100, v)
	if err != nil {
		t.Fatal(err)
	}
	t2, err := s.Correct("salary", 120, v)
	if err != nil {
		t.Fatal(err)
	}
	recs := s.Snapshot("salary")
	if len(recs) != 2 {
		t.Fatalf("len=%d, want 2", len(recs))
	}
	if recs[0].Value != 100 || recs[0].Tx.End != t2 || recs[0].Tx.Start != t1 {
		t.Errorf("old record not closed at t2: %+v", recs[0])
	}
	if recs[1].Value != 120 || recs[1].Tx.Start != t2 {
		t.Errorf("new record wrong: %+v", recs[1])
	}
	if err := s.checkDisjoint(); err != nil {
		t.Fatal(err)
	}
}

func TestCorrectNotFound(t *testing.T) {
	s := New()
	_, err := s.Correct("ghost", 1, mustInterval(t, 0, 1))
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v, want ErrNotFound", err)
	}
}

func TestDeleteTruncatesValid(t *testing.T) {
	s := New()
	if _, err := s.Write("k", 42, mustInterval(t, 2000, 2030)); err != nil {
		t.Fatal(err)
	}
	txDel, err := s.Delete("k", 2020)
	if err != nil {
		t.Fatal(err)
	}
	recs := s.Snapshot("k")
	if len(recs) != 2 {
		t.Fatalf("len=%d, want 2", len(recs))
	}
	if recs[1].Valid.End != 2020 || recs[1].Tx.Start != txDel {
		t.Errorf("truncated record wrong: %+v", recs[1])
	}
	if _, err := s.Delete("ghost", 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v, want ErrNotFound", err)
	}
	if _, err := s.Delete("k", 2000); !errors.Is(err, interval.ErrEmpty) {
		t.Fatalf("err=%v, want ErrEmpty", err)
	}
}

func TestCorrectTouchedBound(t *testing.T) {
	s := New()
	v := mustInterval(t, 2020, 2021)
	if _, err := s.Write("k", 0, v); err != nil {
		t.Fatal(err)
	}
	const versions = 10
	for i := 1; i < versions; i++ {
		if _, err := s.Correct("k", int64(i), v); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Correct("k", 99, v); err != nil {
		t.Fatal(err)
	}
	if got, bound := s.Touched(), versions+2; got > bound {
		t.Fatalf("touched=%d > bound=%d", got, bound)
	}
}

func TestRandomOpsKeepDisjoint(t *testing.T) {
	s := New()
	rng := rand.New(rand.NewSource(1))
	keys := []string{"a", "b", "c", "d"}
	for i := 0; i < 1000; i++ {
		key := keys[rng.Intn(len(keys))]
		start := int64(rng.Intn(100))
		iv := interval.I{Start: start, End: start + 1 + int64(rng.Intn(10))}
		switch rng.Intn(3) {
		case 0:
			_, _ = s.Write(key, int64(i), iv)
		case 1:
			_, _ = s.Correct(key, int64(i), iv)
		default:
			_, _ = s.Delete(key, start+1)
		}
	}
	if err := s.checkDisjoint(); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentCorrectKeepsDisjoint(t *testing.T) {
	s := New()
	v := mustInterval(t, 2020, 2021)
	if _, err := s.Write("hot", 0, v); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for g := 0; g < 100; g++ {
		wg.Add(1)
		go func(base int64) {
			defer wg.Done()
			for i := int64(0); i < 100; i++ {
				_, _ = s.Correct("hot", base+i, v)
			}
		}(int64(g) * 100)
	}
	wg.Wait()
	if err := s.checkDisjoint(); err != nil {
		t.Fatal(err)
	}
}
