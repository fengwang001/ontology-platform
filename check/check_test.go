package check

import (
	"errors"
	"math/rand"
	"slices"
	"testing"

	"ontology/hash"
	"ontology/match"
)

func TestFindAllTable(t *testing.T) {
	cases := []struct {
		name, text, pattern string
		want                []int
		wantErr             error
	}{
		{"empty pattern", "abc", "", nil, match.ErrEmptyPattern},
		{"pattern longer", "ab", "abc", []int{}, nil},
		{"exact", "abc", "abc", []int{0}, nil},
		{"none", "abcdef", "gh", []int{}, nil},
		{"overlap", "aaaa", "aa", []int{0, 1, 2}, nil},
		{"multiple", "ababa", "aba", []int{0, 2}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := match.FindAll(tc.text, tc.pattern)
			want, _ := Naive(tc.text, tc.pattern)
			if !errors.Is(err, tc.wantErr) || !slices.Equal(got, want) || !slices.Equal(got, tc.want) {
				t.Fatalf("got=%v want=%v naive=%v err=%v wantErr=%v", got, tc.want, want, err, tc.wantErr)
			}
		})
	}
}

func TestCollision(t *testing.T) {
	text, pattern := "acobaa", "aco"
	got, err := match.FindAll(text, pattern)
	if err != nil || !slices.Equal(got, []int{0}) {
		t.Fatalf("verified match = %v, %v; want [0]", got, err)
	}
	falseMatches := []int{}
	matchHash := func(s string) (h uint64) {
		wh := hash.New(hash.Base, hash.Mod, len(s))
		for i := range len(s) {
			wh.Append(s[i])
		}
		return wh.Value()
	}
	for start := 0; start+len(pattern) <= len(text); start++ {
		if matchHash(text[start:start+len(pattern)]) == matchHash(pattern) {
			falseMatches = append(falseMatches, start)
		}
	}
	if !slices.Equal(falseMatches, []int{0, 3}) {
		t.Fatalf("hash-only positions = %v, want false-positive collision", falseMatches)
	}
}

func TestVerificationBound(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	text := make([]byte, 100000)
	for i := range text {
		text[i] = byte(rng.Intn(26) + int('A'))
	}
	pattern := "A"
	_, stats, err := match.FindAllStats(string(text), pattern)
	if err != nil {
		t.Fatal(err)
	}
	occurrences := stats.Checks - stats.Collisions
	upper := len(pattern) * (occurrences + stats.Collisions)
	if stats.Checks > upper || stats.Collisions > len(text)/100 {
		t.Fatalf("checks=%d collisions=%d upper=%d", stats.Checks, stats.Collisions, upper)
	}
}

func TestDeterministic(t *testing.T) {
	text := "the ontology text repeats the same pattern and pattern"
	first, err := match.FindAll(text, "pattern")
	if err != nil {
		t.Fatal(err)
	}
	for range 5 {
		next, err := match.FindAll(text, "pattern")
		if err != nil || !slices.Equal(first, next) {
			t.Fatalf("first=%v next=%v err=%v", first, next, err)
		}
	}
	t.Run("concurrent", func(t *testing.T) {
		ready := make(chan struct{})
		results := make(chan []int)
		for range 8 {
			go func() { <-ready; got, _ := match.FindAll(text, "pattern"); results <- got }()
		}
		close(ready)
		for range 8 {
			if !slices.Equal(first, <-results) {
				t.Fatal("concurrent result differs")
			}
		}
	})
}
