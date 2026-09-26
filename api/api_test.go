package api_test

import (
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"ontology/api"
)

func TestNewErrors(t *testing.T) {
	cases := []struct {
		name string
		pat  []byte
		want error
	}{
		{"nil", nil, api.ErrEmptyPattern},
		{"empty", []byte{}, api.ErrEmptyPattern},
		{"too-long", make([]byte, api.MaxPatLen+1), api.ErrPatternTooLong},
		{"at-limit", make([]byte, api.MaxPatLen), nil},
		{"normal", []byte("abc"), nil},
	}
	for _, c := range cases {
		_, err := api.New(c.pat)
		if !errors.Is(err, c.want) {
			t.Errorf("%s: got %v want %v", c.name, err, c.want)
		}
	}
}

func TestSentinelErrorsDistinct(t *testing.T) {
	errs := []error{api.ErrEmptyPattern, api.ErrPatternTooLong, api.ErrTooManyMatches}
	for i, a := range errs {
		for j, b := range errs {
			if i != j && errors.Is(a, b) {
				t.Errorf("errors %d and %d not distinguishable", i, j)
			}
		}
	}
}

func TestMatchLimitRejectedStateIntact(t *testing.T) {
	m, err := api.New([]byte("a"))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := m.Feed([]byte(strings.Repeat("a", api.MaxMatches))); err != nil ||
		len(got) != api.MaxMatches {
		t.Fatalf("fill: got %d hits, err %v", len(got), err)
	}
	before := m.Matches()
	if _, err := m.Feed([]byte("a")); !errors.Is(err, api.ErrTooManyMatches) {
		t.Fatalf("want ErrTooManyMatches, got %v", err)
	}
	if !slices.Equal(m.Matches(), before) {
		t.Fatal("rejected feed changed collected matches")
	}
	if got, err := m.Feed([]byte("b")); err != nil || len(got) != 0 {
		t.Fatalf("matcher unusable after rejection: %v %v", got, err)
	}
	if got, err := m.Feed(nil); err != nil || got != nil {
		t.Fatalf("empty chunk must be a no-op: %v %v", got, err)
	}
}

func TestConcurrentReaders(t *testing.T) {
	m, err := api.New([]byte("abba"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Feed([]byte(strings.Repeat("abbaab", 500))); err != nil {
		t.Fatal(err)
	}
	want := m.Matches()
	var wg sync.WaitGroup
	errs := make(chan string, 16)
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				if got := m.Matches(); !slices.Equal(got, want) {
					errs <- "Matches diverged"
					return
				}
				if err := m.SelfCheck(); err != nil {
					errs <- err.Error()
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
}

func TestSelfCheck(t *testing.T) {
	m, err := api.New([]byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	if err := m.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
