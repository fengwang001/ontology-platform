package check

import (
	"errors"
	"math/rand"
	"reflect"
	"sync"
	"testing"

	"ontology/hash"
	"ontology/match"
)

func TestFindAll(t *testing.T) {
	cases := []struct{ text, pat string }{
		{"aaaa", "aa"}, {"abc", "abc"}, {"abc", "d"}, {"ab", "abc"},
		{"abracadabra", "abra"}, {"hello world", "o"}, {"", "a"},
	}
	for _, c := range cases {
		for i := 0; i < 3; i++ { // deterministic across calls
			if got := match.FindAll(c.text, c.pat); !reflect.DeepEqual(got, Naive(c.text, c.pat)) {
				t.Fatalf("FindAll(%q,%q)=%v want %v", c.text, c.pat, got, Naive(c.text, c.pat))
			}
		}
	}
	if _, err := match.Find("abc", ""); !errors.Is(err, match.ErrEmptyPattern) {
		t.Fatalf("want ErrEmptyPattern, got %v", err)
	}
}

func TestCollision(t *testing.T) {
	digits := []byte{}
	for v := uint64(match.DefaultMod); v > 0; v /= match.DefaultBase {
		digits = append([]byte{byte(v % match.DefaultBase)}, digits...)
	}
	pat, text := string(make([]byte, 4)), string(digits) // equal hash, different bytes
	if got := match.FindAll(text, pat); len(got) != 0 {
		t.Fatalf("false positive at %v", got)
	}
	if got := hashHits(text, pat, match.DefaultBase, match.DefaultMod); !reflect.DeepEqual(got, []int{0}) {
		t.Fatalf("no-verify matcher should false-positive at [0], got %v", got)
	}
}

func TestRollingHash(t *testing.T) {
	if _, err := hash.New(1, 7); !errors.Is(err, hash.ErrInvalidParam) {
		t.Fatal("want ErrInvalidParam")
	}
	r, _ := hash.New(10, 1000)
	if err := r.Remove('x'); !errors.Is(err, hash.ErrEmptyWindow) {
		t.Fatal("want ErrEmptyWindow")
	}
	s := "rolling-hash-window"
	for i := range s {
		r.Append(s[i])
		if r.Len() > 5 {
			r.Remove(s[i-5])
		}
	}
	f, _ := hash.New(10, 1000)
	for i := len(s) - 5; i < len(s); i++ {
		f.Append(s[i])
	}
	if r.Value() != f.Value() {
		t.Fatalf("rolled %d != fresh %d", r.Value(), f.Value())
	}
}

func TestComparisonBound(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	buf := make([]byte, 100000)
	for i := range buf {
		buf[i] = byte('a' + rng.Intn(3))
	}
	text, pat := string(buf), "aab"
	match.ResetComparisons()
	if got := match.FindAll(text, pat); !reflect.DeepEqual(got, Naive(text, pat)) {
		t.Fatal("mismatch with naive")
	}
	hits := len(hashHits(text, pat, match.DefaultBase, match.DefaultMod))
	if c := match.Comparisons(); c > int64(len(pat)*hits) {
		t.Fatalf("comparisons %d > m*(occurrences+collisions)=%d", c, len(pat)*hits)
	}
}

func TestConcurrent(t *testing.T) {
	text, pat := "abcabcabcabc", "abc"
	want := match.FindAll(text, pat)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if got := match.FindAll(text, pat); !reflect.DeepEqual(got, want) {
				t.Error("concurrent result mismatch")
			}
		}()
	}
	wg.Wait()
}
