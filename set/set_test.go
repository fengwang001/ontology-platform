package set_test

import (
	"errors"
	"sync"
	"testing"

	"ontology/engine"
	"ontology/set"
	"ontology/syntax"
)

// eval re-decides path from rules single-threadedly: the oracle.
func eval(rules []string, path string) set.Decision {
	d := set.Decision{Verdict: set.Undecided, Index: -1}
	for i, raw := range rules {
		pat, inc := raw, true
		if pat[0] == '!' {
			pat, inc = pat[1:], false
		}
		p, err := syntax.Compile(pat, syntax.Limits{})
		if err != nil {
			panic(err)
		}
		if engine.Match(p, path) {
			d.Verdict, d.Raw, d.Index = set.Include, raw, i
			if !inc {
				d.Verdict = set.Exclude
			}
		}
	}
	return d
}

func TestRules(t *testing.T) {
	rules := []string{"*.go", "!internal/**", "internal/x.go"}
	s, err := set.New(rules, syntax.Limits{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		path string
		want set.Decision
	}{
		{"main.go", set.Decision{Verdict: set.Include, Raw: "*.go", Index: 0, Version: 1}},
		{"internal/a.go", set.Decision{Verdict: set.Exclude, Raw: "!internal/**", Index: 1, Version: 1}},
		{"internal/x.go", set.Decision{Verdict: set.Include, Raw: "internal/x.go", Index: 2, Version: 1}},
		{"README.md", set.Decision{Verdict: set.Undecided, Raw: "", Index: -1, Version: 1}},
	}
	for _, c := range cases {
		if got := s.Explain(c.path); got != c.want {
			t.Errorf("Explain(%q) = %+v, want %+v", c.path, got, c.want)
		}
	}
	if s.Decide("README.md") == set.Exclude {
		t.Error("undecided must be distinguishable from excluded")
	}
}

func TestReplaceKeepsOld(t *testing.T) {
	s, err := set.New([]string{"*.go"}, syntax.Limits{}, 2)
	if err != nil {
		t.Fatal(err)
	}
	before := s.Version()
	for _, bad := range [][]string{{"!unclosed["}, {"a", "b", "c"}} {
		if err := s.Replace(bad); err == nil {
			t.Errorf("Replace(%v) succeeded, want error", bad)
		}
		if s.Version() != before || s.Decide("main.go") != set.Include {
			t.Errorf("Replace(%v) disturbed the old version", bad)
		}
	}
	if _, err := set.New([]string{"a", "b", "c"}, syntax.Limits{}, 2); !errors.Is(err, set.ErrTooManyRules) {
		t.Errorf("New over limit: %v, want ErrTooManyRules", err)
	}
}

func TestConcurrentVersions(t *testing.T) {
	rulesA := []string{"**"}
	rulesB := []string{"!**", "keep/me"}
	s, err := set.New(rulesA, syntax.Limits{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	versions := map[uint64][]string{s.Version(): rulesA}
	record := func(rules []string) {
		mu.Lock()
		versions[s.Version()] = rules
		mu.Unlock()
	}
	type obs struct {
		path string
		got  set.Decision
	}
	obsCh := make(chan obs, 4096)
	obsDone := make(chan []obs)
	go func() { // drain concurrently so queriers never block
		var all []obs
		for o := range obsCh {
			all = append(all, o)
		}
		obsDone <- all
	}()
	var wg sync.WaitGroup
	paths := []string{"x", "keep/me", "a/b/c", ""}
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				p := paths[(g+i)%len(paths)]
				obsCh <- obs{p, s.Explain(p)}
			}
		}(g)
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	for i := 0; i < 300; i++ { // swap A/B with failing swaps interleaved
		if err := s.Replace([]string{"!broken["}); err == nil {
			t.Error("broken replace succeeded")
		}
		if i%2 == 0 {
			if err := s.Replace(rulesB); err == nil {
				record(rulesB)
			}
		} else if err := s.Replace(rulesA); err == nil {
			record(rulesA)
		}
	}
	<-done
	close(obsCh)
	for _, o := range <-obsDone {
		mu.Lock()
		rules, ok := versions[o.got.Version]
		mu.Unlock()
		if !ok {
			t.Fatalf("version %d never recorded", o.got.Version)
		}
		if want := eval(rules, o.path); o.got.Verdict != want.Verdict ||
			o.got.Raw != want.Raw || o.got.Index != want.Index {
			t.Fatalf("Explain(%q) = %+v, version %d wants %+v", o.path, o.got, o.got.Version, want)
		}
	}
}
