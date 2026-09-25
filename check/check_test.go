package check

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"reflect"
	"sync"
	"testing"

	"ontology/hash"
	"ontology/match"
)

func hashOf(s string) (v int64) {
	h, _ := hash.New(2, 251, len(s))
	for i := range s {
		h.Append(s[i])
	}
	return h.Value()
}

func fakeFindAll(text, pattern string) (got []int) {
	for i := 0; i <= len(text)-len(pattern); i++ {
		if hashOf(text[i:i+len(pattern)]) == hashOf(pattern) {
			got = append(got, i)
		}
	}
	return got
}

func TestFindAll(t *testing.T) {
	tests := []struct {
		name, text, pattern string
		want                []int
	}{
		{"all", "aaaaa", "aa", []int{0, 1, 2, 3}},
		{"equal", "abc", "abc", []int{0}},
		{"none", "abcdef", "xyz", []int{}},
		{"longer", "ab", "abc", []int{}},
		{"overlaps", "abababa", "aba", []int{0, 2, 4}},
		{"empty error", "abc", "", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := match.FindAll(tt.text, tt.pattern)
			if tt.pattern == "" && !errors.Is(err, match.ErrEmptyPattern) {
				t.Fatalf("error = %v", err)
			}
			if tt.pattern != "" && (!reflect.DeepEqual(got, tt.want) || !reflect.DeepEqual(got, NaiveFindAll(tt.text, tt.pattern))) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCollision(t *testing.T) {
	text, pattern := `! "`, ` "`
	if hashOf(text[:2]) != hashOf(pattern) || hashOf(text[1:]) != hashOf(pattern) {
		t.Fatal("fixture does not collide")
	}
	got, _ := match.FindAll(text, pattern)
	if !reflect.DeepEqual(got, []int{1}) || !reflect.DeepEqual(fakeFindAll(text, pattern), []int{0, 1}) {
		t.Fatalf("correct=%v naive-hash=%v", got, fakeFindAll(text, pattern))
	}
}

func TestVerificationBound(t *testing.T) {
	text := make([]byte, 100000)
	pattern := "ABCDEFGH"
	rng := rand.New(rand.NewPCG(1, 2))
	for i := range text {
		text[i] = byte(32 + rng.Uint64()%95)
	}
	got, _ := match.FindAll(string(text), pattern)
	collisions := 0
	for i := 0; i <= len(text)-len(pattern); i++ {
		if hashOf(string(text[i:i+len(pattern)])) == hashOf(pattern) {
			collisions++
		}
	}
	if count := match.VerificationCount(); count > len(pattern)*(len(got)+collisions) {
		t.Fatalf("verification count %d > bound %d", count, len(pattern)*(len(got)+collisions))
	}
}

func TestConcurrent(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			text := fmt.Sprintf("abababa-%d-ab", i)
			if got, _ := match.FindAll(text, "ab"); !reflect.DeepEqual(got, NaiveFindAll(text, "ab")) {
				t.Errorf("got %v", got)
			}
		}(i)
	}
	wg.Wait()
}
