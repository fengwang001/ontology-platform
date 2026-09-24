package store

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"

	"ontology/interval"
)

func mustInterval(t *testing.T, start, end int64) interval.Interval {
	t.Helper()
	iv, err := interval.New(start, end)
	if err != nil {
		t.Fatal(err)
	}
	return iv
}

func TestMutateErrors(t *testing.T) {
	iv2020 := mustInterval(t, 2020, 2021)
	cases := []struct {
		name string
		run  func(s *Store) error
		want error
	}{
		{"put empty interval", func(s *Store) error {
			_, err := s.Put("k", 1, interval.Interval{Start: 5, End: 5})
			return err
		}, interval.ErrEmpty},
		{"put overlap", func(s *Store) error {
			_, err := s.Put("k", 2, mustInterval(t, 2020, 2025))
			return err
		}, ErrOverlap},
		{"correct missing", func(s *Store) error {
			_, err := s.Correct("nope", 1, iv2020)
			return err
		}, ErrNotFound},
		{"correct empty interval", func(s *Store) error {
			_, err := s.Correct("k", 1, interval.Interval{})
			return err
		}, interval.ErrEmpty},
		{"delete missing", func(s *Store) error {
			_, err := s.Delete("nope", 2020)
			return err
		}, ErrNotFound},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := New()
			if _, err := s.Put("k", 100, iv2020); err != nil {
				t.Fatal(err)
			}
			if err := c.run(s); !errors.Is(err, c.want) {
				t.Fatalf("want %v, got %v", c.want, err)
			}
		})
	}
}

func TestCorrectClosesOldTxInterval(t *testing.T) {
	s := New()
	iv := mustInterval(t, 2020, 2021)
	tx1, _ := s.Put("k", 100, iv)
	tx2, _ := s.Correct("k", 120, iv)
	recs := s.Versions("k")
	if len(recs) != 2 {
		t.Fatalf("want 2 versions, got %d", len(recs))
	}
	old, cur := recs[0], recs[1]
	if old.Tx.Start != tx1 || old.Tx.End != tx2 || old.Value != 100 {
		t.Fatalf("old version not closed properly: %+v", old)
	}
	if cur.Tx.Start != tx2 || !cur.Current() || cur.Value != 120 {
		t.Fatalf("new version wrong: %+v", cur)
	}
}

func TestDeleteTruncatesValidInterval(t *testing.T) {
	s := New()
	if _, err := s.Put("k", 100, mustInterval(t, 2020, 2030)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Delete("k", 2025); err != nil {
		t.Fatal(err)
	}
	recs := s.Versions("k")
	if len(recs) != 2 {
		t.Fatalf("want 2 versions, got %d", len(recs))
	}
	keep := recs[1]
	if keep.Valid.Start != 2020 || keep.Valid.End != 2025 || !keep.Current() {
		t.Fatalf("truncated version wrong: %+v", keep)
	}
	if err := s.checkInvariant(); err != nil {
		t.Fatal(err)
	}
}

func TestInvariantAfterRandomOps(t *testing.T) {
	s := New()
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 1000; i++ {
		key := fmt.Sprintf("k%d", rng.Intn(20))
		start := int64(rng.Intn(50))
		iv := interval.Interval{Start: start, End: start + 1 + int64(rng.Intn(5))}
		switch rng.Intn(3) {
		case 0:
			_, _ = s.Put(key, int64(i), iv)
		case 1:
			_, _ = s.Correct(key, int64(i), iv)
		default:
			_, _ = s.Delete(key, start)
		}
	}
	if err := s.checkInvariant(); err != nil {
		t.Fatal(err)
	}
}

func TestInvariantAfterConcurrentCorrect(t *testing.T) {
	s := New()
	iv := mustInterval(t, 2020, 2021)
	if _, err := s.Put("k", 0, iv); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for g := 0; g < 100; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				_, _ = s.Correct("k", int64(g*100+i), iv)
			}
		}(g)
	}
	wg.Wait()
	if err := s.checkInvariant(); err != nil {
		t.Fatal(err)
	}
}
