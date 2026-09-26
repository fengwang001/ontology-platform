package api_test

import (
	"errors"
	"math/rand"
	"strings"
	"sync"
	"testing"

	"ontology/api"
	"ontology/match"
	"ontology/parse"
)

// TestDotStarSemantics pins the precise meaning of '.' and '*':
// '.' is exactly one character, '*' binds to the previous element
// and may repeat it zero or more times; matching is anchored.
func TestDotStarSemantics(t *testing.T) {
	cases := []struct {
		s, p string
		want bool
	}{
		{"aab", "c*a*b", true},
		{"a", "a.", false},
		{"mississippi", "mis*is*p*.", false},
		{"ab", ".*", true},
		{"", "", true},
		{"", "a*", true},
		{"", ".*", true},
		{"", "a*b*", true},
		{"", ".", false},
		{"a", ".", true},
		{"ab", ".", false},
		{"aa", "a", false},
		{"aaa", "a*a", true},
		{"aabb", "a*b*.", true},
		{"b", "a*b", true},
		{"abcd", "d*", false},
	}
	for _, c := range cases {
		if got := match.Match(c.s, c.p); got != c.want {
			t.Errorf("Match(%q,%q)=%v, want %v", c.s, c.p, got, c.want)
		}
	}
}

// TestNaiveConsistency feeds random valid patterns and random texts
// to both the memoized DP and the naive oracle; results must agree
// pair by pair.
func TestNaiveConsistency(t *testing.T) {
	rng := rand.New(rand.NewSource(20260926))
	alphabet := []byte{'a', 'b', '.'}
	for trial := 0; trial < 3000; trial++ {
		var pb strings.Builder
		for k := 0; k < 1+rng.Intn(7); k++ {
			pb.WriteByte(alphabet[rng.Intn(len(alphabet))])
			if rng.Intn(2) == 0 {
				pb.WriteByte('*')
			}
		}
		var sb strings.Builder
		for k := 0; k < rng.Intn(8); k++ {
			sb.WriteByte("ab"[rng.Intn(2)])
		}
		p, s := pb.String(), sb.String()
		if got, want := match.Match(s, p), match.Naive(s, p); got != want {
			t.Fatalf("Match(%q,%q)=%v, Naive=%v", s, p, got, want)
		}
	}
}

// TestFailureNoTrace: the three fault classes are distinguishable
// sentinel errors, and every rejection leaves all state untouched.
func TestFailureNoTrace(t *testing.T) {
	m, err := api.Compile("a*b.")
	if err != nil {
		t.Fatal(err)
	}
	before, err := m.Match("aabx")
	if err != nil || !before {
		t.Fatalf("baseline: %v %v", before, err)
	}
	faults := []struct {
		name string
		pat  string
		want error
	}{
		{"leading star", "*a", api.ErrSyntax},
		{"double star", "a**", api.ErrSyntax},
		{"unsupported char", "a b", api.ErrUnsupported},
		{"pattern too long", strings.Repeat("a", parse.MaxLen+1), api.ErrTooLong},
	}
	for _, f := range faults {
		if _, err := api.Compile(f.pat); !errors.Is(err, f.want) {
			t.Errorf("%s: got %v, want %v", f.name, err, f.want)
		}
	}
	if errors.Is(api.ErrSyntax, api.ErrUnsupported) || errors.Is(api.ErrUnsupported, api.ErrTooLong) || errors.Is(api.ErrSyntax, api.ErrTooLong) {
		t.Error("sentinel errors are not mutually distinguishable")
	}
	long := strings.Repeat("a", parse.MaxLen+1)
	if _, err := m.Match(long); !errors.Is(err, api.ErrTooLong) {
		t.Errorf("long text: got %v", err)
	}
	after, err := m.Match("aabx")
	if err != nil || after != before {
		t.Fatalf("state changed after rejections: %v %v", after, err)
	}
}

// TestConcurrentConsistent hammers one compiled matcher from many
// goroutines; every result must equal the serial result. No sleeps.
func TestConcurrentConsistent(t *testing.T) {
	m, err := api.Compile("mis*is*p*.")
	if err != nil {
		t.Fatal(err)
	}
	texts := []string{"mississippi", "mis", "mississipp", "misisip", "mi", "mississippix"}
	want := make([]bool, len(texts))
	for i, s := range texts {
		want[i], err = m.Match(s)
		if err != nil {
			t.Fatal(err)
		}
	}
	const workers = 64
	got := make([][]bool, workers)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		got[w] = make([]bool, len(texts))
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i, s := range texts {
				got[w][i], _ = m.Match(s)
			}
		}(w)
	}
	wg.Wait()
	for w := 0; w < workers; w++ {
		for i := range texts {
			if got[w][i] != want[i] {
				t.Fatalf("worker %d text %q: got %v, want %v", w, texts[i], got[w][i], want[i])
			}
		}
	}
	if err := api.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
