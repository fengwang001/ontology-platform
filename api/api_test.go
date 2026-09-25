package api

import (
	"bytes"
	"errors"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"testing"
)

const term = '$'

// cases returns the fixed corpus plus random '$'-free strings of the given sizes.
func cases(seed int64, sizes ...int) []string {
	out := []string{"", "a", "banana", "aaaa", "mississippi", "abracadabra", "xy xy xy z"}
	rng := rand.New(rand.NewSource(seed))
	const abc = "abcdefghijklmnopqrstuvwxyz0123456789"
	for _, m := range sizes {
		b := make([]byte, m)
		for i := range b {
			b[i] = abc[rng.Intn(len(abc))]
		}
		out = append(out, string(b))
	}
	return out
}

func TestRoundTrip(t *testing.T) {
	if err := SelfCheck(); err != nil {
		t.Fatal(err)
	}
	list := append(cases(42, 1, 2, 7, 64, 255, 1000), "xx", "xxxxx", strings.Repeat("x", 100))
	for _, s := range list {
		last, primary, err := Transform([]byte(s), term)
		if err != nil {
			t.Fatalf("Transform(%q): %v", s, err)
		}
		got, err := Inverse(last, primary, term)
		if err != nil || string(got) != s {
			t.Fatalf("roundtrip(%q) = %q, %v", s, got, err)
		}
	}
}

func TestAgainstNaive(t *testing.T) {
	for _, s := range cases(9, 3, 17, 50) {
		wantLast, wantPrimary, _ := naiveForward([]byte(s), term)
		last, primary, err := Transform([]byte(s), term)
		if err != nil || !bytes.Equal(last, wantLast) || primary != wantPrimary {
			t.Fatalf("forward(%q) diverges from naive reference", s)
		}
		if got, err := Inverse(last, primary, term); err != nil ||
			!bytes.Equal(got, naiveInverse(wantLast, wantPrimary, term)) {
			t.Fatalf("inverse(%q) diverges from naive reference", s)
		}
	}
}

func TestLastIsPermutation(t *testing.T) {
	for _, s := range cases(5, 10, 200) {
		last, _, err := Transform([]byte(s), term)
		if err != nil {
			t.Fatal(err)
		}
		_, _, f := naiveForward([]byte(s), term)
		sorted := append([]byte{}, last...)
		sort.Slice(sorted, func(a, b int) bool { return sorted[a] < sorted[b] })
		if !bytes.Equal(sorted, f) {
			t.Fatalf("sort(last) != F for %q", s)
		}
		var cnt [256]int
		for _, c := range last {
			cnt[c]++
		}
		for i := 0; i < len(s); i++ {
			cnt[s[i]]--
		}
		if cnt[term]--; cnt != [256]int{} {
			t.Fatalf("last counts != s counts + one term for %q", s)
		}
	}
}

func TestFailureAtomicity(t *testing.T) {
	_, _, eTerm := Transform([]byte("ban$ana"), term)
	_, eNeg := Inverse([]byte("annb$aa"), -1, term)
	_, eBig := Inverse([]byte("annb$aa"), 7, term)
	_, eCnt := Inverse([]byte("annb$aa$"), 0, term)
	for _, c := range [][2]error{
		{eTerm, ErrTerminatorInInput}, {eNeg, ErrInvalidPrimary},
		{eBig, ErrInvalidPrimary}, {eCnt, ErrTerminatorCount},
	} {
		if !errors.Is(c[0], c[1]) {
			t.Fatalf("got %v, want %v", c[0], c[1])
		}
	}
	for _, p := range [][2]error{{eTerm, eNeg}, {eTerm, eCnt}, {eNeg, eCnt}} {
		if errors.Is(p[0], p[1]) {
			t.Fatal("sentinel errors must be mutually distinct")
		}
	}
	if l, p, _ := Transform([]byte("a$a"), term); l != nil || p != 0 {
		t.Fatal("rejected Transform returned partial output")
	}
	if got, err := Inverse([]byte("annb$aa"), 4, term); err != nil || string(got) != "banana" {
		t.Fatal("state broken after rejections")
	}
}

func TestConcurrent(t *testing.T) {
	input := []byte("concurrent bwt roundtrip check")
	const n = 32
	results := make([][]byte, n)
	var wg sync.WaitGroup
	for g := 0; g < n; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			last, primary, err := Transform(input, term)
			if err == nil {
				results[g], err = Inverse(last, primary, term)
			}
			if err != nil {
				t.Error(err)
			}
		}(g)
	}
	wg.Wait()
	for g, r := range results {
		if !bytes.Equal(r, input) {
			t.Fatalf("goroutine %d: got %q", g, r)
		}
	}
}
