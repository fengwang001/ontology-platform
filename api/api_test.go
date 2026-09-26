package api_test

import (
	"errors"
	"math/rand"
	"sync"
	"testing"

	"ontology/api"
)

func mustNew(t *testing.T, s string) *api.String {
	t.Helper()
	x, err := api.New(s)
	if err != nil {
		t.Fatalf("New(%q): %v", s, err)
	}
	return x
}

// naiveMinPeriod: first p in 1..n with s[i]==s[i-p] for all i>=p.
func naiveMinPeriod(s string) int {
	for p := 1; p <= len(s); p++ {
		ok := true
		for i := p; i < len(s); i++ {
			if s[i] != s[i-p] {
				ok = false
				break
			}
		}
		if ok {
			return p
		}
	}
	return 0
}

func TestNaiveConsistency(t *testing.T) {
	cases := []string{"a", "abcde", "aaaa", "ababab", "abababa", "abcabcabc", "abcabcab", "abcabcd"}
	r := rand.New(rand.NewSource(11))
	for iter := 0; iter < 200; iter++ {
		n := 1 + r.Intn(50)
		b := make([]byte, n)
		for i := range b {
			b[i] = byte('a' + r.Intn(4))
		}
		cases = append(cases, string(b))
	}
	for _, s := range cases {
		x, n := mustNew(t, s), len(s)
		if mp, _ := x.MinPeriod(); mp != naiveMinPeriod(s) {
			t.Fatalf("MinPeriod(%q)=%d, naive %d", s, mp, naiveMinPeriod(s))
		}
		for p := 1; p <= n; p++ {
			direct := true
			for i := p; i < n; i++ {
				if s[i] != s[i-p] {
					direct = false
					break
				}
			}
			got, err := x.IsPeriodic(p)
			if err != nil || got != direct {
				t.Fatalf("IsPeriodic(%q,%d)=(%v,%v), definition %v", s, p, got, err, direct)
			}
		}
	}
}

func TestPeriodBorderEquivalence(t *testing.T) {
	for _, s := range []string{"ababab", "abababa", "abcabcabc", "abcde", "aaaa", "abcabcd"} {
		x, n, longest := mustNew(t, s), len(s), 0
		for p := 1; p <= n; p++ {
			got, _ := x.IsPeriodic(p)
			b := n - p
			border := b == 0 || s[:b] == s[n-b:]
			if got != border {
				t.Fatalf("(%q): period p=%d %v vs border %d %v", s, p, got, b, border)
			}
			if border && b > longest {
				longest = b
			}
		}
		if mp, _ := x.MinPeriod(); mp != n-longest {
			t.Fatalf("(%q): MinPeriod=%d, n-longestBorder=%d", s, mp, n-longest)
		}
	}
}

func TestPowerSemantics(t *testing.T) {
	cases := []struct {
		s     string
		power bool
	}{
		{"a", false}, {"ab", false}, {"abcde", false},
		{"ababab", true}, {"aaaa", true}, {"aaaaa", true},
		{"abcabcabc", true}, {"abababab", true},
		{"abababa", false},  // period 2 < 7 but 7%2 != 0
		{"abcabcab", false}, // period 3 < 8 but 8%3 != 0
	}
	for _, c := range cases {
		if got := mustNew(t, c.s).IsPower(); got != c.power {
			t.Errorf("IsPower(%q)=%v, want %v", c.s, got, c.power)
		}
	}
}

func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	if _, e := api.New(""); !errors.Is(e, api.ErrEmptyString) {
		t.Fatalf("empty: %v", e)
	}
	if _, e := api.New(string(make([]byte, 1<<20+1))); !errors.Is(e, api.ErrStringTooLong) {
		t.Fatalf("long: %v", e)
	}
	x := mustNew(t, "abababa")
	for _, p := range []int{0, -1, 8} {
		if ok, e := x.IsPeriodic(p); !errors.Is(e, api.ErrPeriodOutOfRange) || ok {
			t.Fatalf("IsPeriodic(%d)=(%v,%v), want sentinel/false", p, ok, e)
		}
	}
	if mp, _ := x.MinPeriod(); mp != 2 || x.IsPower() || !x.SelfCheck() {
		t.Fatalf("state changed after rejection: period=%d power=%v", mp, x.IsPower())
	}
}

// TestConcurrentReaders hammers one instance from N goroutines with no sleeps.
func TestConcurrentReaders(t *testing.T) {
	x := mustNew(t, "abcabcabcabc")
	const N = 64
	var wg sync.WaitGroup
	res := make([][3]interface{}, N)
	start := make(chan struct{})
	for g := 0; g < N; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			<-start
			m, _ := x.MinPeriod()
			q, _ := x.IsPeriodic(3)
			res[g] = [3]interface{}{m, q, x.IsPower()}
		}(g)
	}
	close(start)
	wg.Wait()
	for g := 1; g < N; g++ {
		if res[g] != res[0] {
			t.Fatalf("goroutine %d: %v != %v", g, res[g], res[0])
		}
	}
}
