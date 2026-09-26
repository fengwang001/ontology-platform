package api_test

import (
	"errors"
	"math/rand"
	"strings"
	"sync"
	"testing"

	"ontology/api"
	"ontology/parse"
)

// TestCompileRejectKinds pins the three distinct, decidable rejections and
// ensures a rejection returns no matcher.
func TestCompileRejectKinds(t *testing.T) {
	cases := []struct {
		name    string
		pattern string
		sent    error
	}{
		{"unsupported", "a\x00b", parse.ErrUnsupportedChar},
		{"too long", strings.Repeat("a", parse.MaxLen+1), parse.ErrTooLong},
		{"too many wildcards", strings.Repeat("*", parse.MaxWildcards+1), parse.ErrTooManyWildcards},
	}
	for _, c := range cases {
		m, err := api.Compile(c.pattern)
		if err == nil || m != nil {
			t.Errorf("%s: expected rejection, got matcher=%v err=%v", c.name, m, err)
		}
		if !errors.Is(err, c.sent) {
			t.Errorf("%s: err=%v is not sentinel %v", c.name, err, c.sent)
		}
	}
	if errors.Is(parse.ErrUnsupportedChar, parse.ErrTooLong) ||
		errors.Is(parse.ErrTooLong, parse.ErrTooManyWildcards) {
		t.Fatal("sentinel errors must be pairwise distinct")
	}
}

// TestRejectLeavesState verifies invariant 4: after rejected Compiles and
// rejected Matches no state changes and normal service still works.
func TestRejectLeavesState(t *testing.T) {
	m, err := api.Compile("*a*b")
	if err != nil {
		t.Fatal(err)
	}
	before, _ := m.Match("adceb")
	for _, bad := range []string{"a\tb", strings.Repeat("*", parse.MaxWildcards+1), "a\x7fb"} {
		if g, e := api.Compile(bad); e == nil || g != nil {
			t.Fatalf("pattern %q should be rejected", bad)
		}
	}
	if _, e := m.Match(strings.Repeat("a", parse.MaxLen+1)); !errors.Is(e, parse.ErrTooLong) {
		t.Fatalf("overlong text must return ErrTooLong, got %v", e)
	}
	for _, c := range []struct {
		s    string
		want bool
	}{{"adceb", true}, {"xyz", false}, {"adce", false}} {
		got, e := m.Match(c.s)
		if e != nil || got != c.want {
			t.Errorf("after rejects Match(%q)=%v,%v want %v", c.s, got, e, c.want)
		}
	}
	after, _ := m.Match("adceb")
	if before != true || after != before {
		t.Fatal("behavior changed after rejections")
	}
}

// TestSelfCheck requires the exported self-check to pass.
func TestSelfCheck(t *testing.T) {
	if err := api.SelfCheck(); err != nil {
		t.Fatalf("api.SelfCheck: %v", err)
	}
}

// TestConcurrentMatch runs many goroutines against one compiled matcher and
// requires pairwise equality with serial results; SelfCheck runs alongside.
func TestConcurrentMatch(t *testing.T) {
	patterns := []string{"*a*b", "a*c?b", "????", "*"}
	rng := rand.New(rand.NewSource(7))
	const N = 200
	for _, p := range patterns {
		m, err := api.Compile(p)
		if err != nil {
			t.Fatal(err)
		}
		texts := make([]string, N)
		for i := range texts {
			texts[i] = randText(rng)
		}
		serial := make([]bool, N)
		for i, tx := range texts {
			serial[i], _ = m.Match(tx)
		}
		concur := make([]bool, N)
		var wg sync.WaitGroup
		for i, tx := range texts {
			wg.Add(1)
			go func(i int, tx string) { defer wg.Done(); concur[i], _ = m.Match(tx) }(i, tx)
		}
		for g := 0; g < 4; g++ {
			wg.Add(1)
			go func() { defer wg.Done(); _ = api.SelfCheck() }()
		}
		wg.Wait()
		for i := range serial {
			if concur[i] != serial[i] {
				t.Fatalf("pattern %q text %q: concurrent=%v serial=%v", p, texts[i], concur[i], serial[i])
			}
		}
	}
}

func randText(rng *rand.Rand) string {
	n := rng.Intn(9)
	b := make([]byte, n)
	for i := range b {
		b[i] = "abxyz"[rng.Intn(5)]
	}
	return string(b)
}
