package ontology

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"testing"
)

func values(entries []Entry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.Value
	}
	return out
}

func TestInsertEmptySequence(t *testing.T) {
	s := NewSequence(nil, 0)
	key, err := s.Insert("", "", "only")
	if err != nil {
		t.Fatalf("insert into empty sequence: %v", err)
	}
	if key == "" {
		t.Fatal("empty key returned")
	}
	if got := values(s.Snapshot()); len(got) != 1 || got[0] != "only" {
		t.Fatalf("snapshot = %v", got)
	}
	if err := s.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

func TestInsertAroundSingleElement(t *testing.T) {
	s := NewSequence(nil, 0)
	mid, err := s.Insert("", "", "m")
	if err != nil {
		t.Fatal(err)
	}
	front, err := s.Insert("", mid, "front")
	if err != nil {
		t.Fatalf("insert before single element: %v", err)
	}
	back, err := s.Insert(mid, "", "back")
	if err != nil {
		t.Fatalf("insert after single element: %v", err)
	}
	if !(front < mid && mid < back) {
		t.Fatalf("keys out of order: %q %q %q", front, mid, back)
	}
	want := []string{"front", "m", "back"}
	if got := values(s.Snapshot()); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

func TestConcurrentInsertSameGap(t *testing.T) {
	const n = 64
	s := NewSequence(nil, 0)
	if _, err := s.Insert("", "", "left-bound"); err != nil {
		t.Fatal(err)
	}
	rightKey, err := s.Insert("", "", "right-bound")
	if err != nil {
		t.Fatal(err)
	}
	leftKey := s.Snapshot()[0].Key

	want := make([]string, 0, n)
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			v := fmt.Sprintf("v%03d", i)
			if _, err := s.Insert(leftKey, rightKey, v); err != nil {
				errs <- fmt.Errorf("insert %s: %w", v, err)
			}
		}(i)
		want = append(want, fmt.Sprintf("v%03d", i))
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	sort.Strings(want)
	want = append([]string{"left-bound"}, append(want, "right-bound")...)
	got := values(s.Snapshot())
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("final order = %v, want %v", got, want)
	}
	if err := s.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck after concurrent inserts: %v", err)
	}
}

func TestInsertDuplicateValue(t *testing.T) {
	s := NewSequence(nil, 0)
	if _, err := s.Insert("", "", "x"); err != nil {
		t.Fatal(err)
	}
	_, err := s.Insert("", "", "x")
	if !errors.Is(err, ErrDuplicateValue) {
		t.Fatalf("got %v, want ErrDuplicateValue", err)
	}
}

func TestLongestKey(t *testing.T) {
	const maxLen = 8
	s := NewSequence(nil, maxLen)
	if length, remaining := s.LongestKey(); length != 0 || remaining != maxLen {
		t.Fatalf("empty: length=%d remaining=%d", length, remaining)
	}
	if _, err := s.Insert("", "", "a"); err != nil {
		t.Fatal(err)
	}
	length, remaining := s.LongestKey()
	if length != 1 || remaining != maxLen-1 {
		t.Fatalf("length=%d remaining=%d, want 1 and %d", length, remaining, maxLen-1)
	}
}

func TestSelfCheckDetectsCorruption(t *testing.T) {
	s := NewSequence(nil, 0)
	if _, err := s.Insert("", "", "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Insert("", "", "b"); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.entries[1].Key = s.entries[0].Key // duplicate key
	s.mu.Unlock()
	if err := s.SelfCheck(); err == nil {
		t.Fatal("SelfCheck missed duplicate keys")
	}
}
