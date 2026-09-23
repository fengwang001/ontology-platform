package set_test

import (
	"errors"
	"sync"
	"testing"

	"ontology/set"
	"ontology/syntax"
)

func TestVerdicts(t *testing.T) {
	s := set.New(set.Options{})
	if err := s.Replace([]string{"a/*", "!a/secret"}); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		path  string
		want  set.Verdict
		rule  string
		index int
	}{
		{"a/x", set.Included, "a/*", 0},
		{"a/secret", set.Excluded, "!a/secret", 1},
		{"b", set.Undecided, "", -1},
	}
	for _, c := range cases {
		d := s.Explain(c.path)
		if d.Verdict != c.want || d.Rule != c.rule || d.Index != c.index {
			t.Errorf("Explain(%q) = %+v, want verdict %d rule %q index %d",
				c.path, d, c.want, c.rule, c.index)
		}
		if got := s.Query(c.path); got != c.want {
			t.Errorf("Query(%q) = %d, want %d", c.path, got, c.want)
		}
	}
	if set.Undecided == set.Excluded {
		t.Error("undecided must be distinguishable from excluded")
	}
}

func TestReplaceKeepsOld(t *testing.T) {
	s := set.New(set.Options{})
	if err := s.Replace([]string{"x"}); err != nil {
		t.Fatal(err)
	}
	v0 := s.Explain("x").Version
	if err := s.Replace([]string{"y", "[bad"}); err == nil {
		t.Fatal("broken rule set must fail")
	}
	d := s.Explain("x")
	if d.Version != v0 || d.Verdict != set.Included {
		t.Errorf("old version not kept: %+v", d)
	}
	if s.Query("y") != set.Undecided {
		t.Error("failed replace must not apply partially")
	}
}

func TestSetLimits(t *testing.T) {
	s := set.New(set.Options{MaxRules: 1, MaxPatternBytes: 6, MaxDoubleStar: 1})
	if err := s.Replace([]string{"a", "b"}); !errors.Is(err, set.ErrTooManyRules) {
		t.Errorf("rules limit: %v", err)
	}
	if err := s.Replace([]string{"abcdefg"}); !errors.Is(err, syntax.ErrTooLong) {
		t.Errorf("bytes limit: %v", err)
	}
	if err := s.Replace([]string{"**/**"}); !errors.Is(err, syntax.ErrTooManyDouble) {
		t.Errorf("** limit: %v", err)
	}
	if v := s.Explain("a").Version; v != 0 {
		t.Errorf("failed replaces changed version to %d", v)
	}
}

func TestConcurrentVersions(t *testing.T) {
	vA := []string{"**"}
	vB := []string{"!**"}
	var ref [2]set.Decision
	for i, rules := range [][]string{vA, vB} {
		s := set.New(set.Options{})
		if err := s.Replace(rules); err != nil {
			t.Fatal(err)
		}
		ref[i] = s.Explain("any/path")
	}
	s := set.New(set.Options{})
	if err := s.Replace(vA); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				d := s.Explain("any/path")
				want := ref[(d.Version-1)%2]
				if d.Verdict != want.Verdict || d.Rule != want.Rule || d.Index != want.Index {
					t.Errorf("version %d: got %+v, want %+v", d.Version, d, want)
					return
				}
			}
		}()
	}
	for i := 0; i < 2000; i++ {
		if i%2 == 0 {
			_ = s.Replace(vB)
		} else {
			_ = s.Replace(vA)
		}
		if err := s.Replace([]string{"[broken"}); err == nil {
			t.Error("broken replace must fail")
		}
	}
	close(stop)
	wg.Wait()
}
