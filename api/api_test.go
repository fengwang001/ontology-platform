package api

import (
	"errors"
	"strings"
	"sync"
	"testing"
)

func TestCompileMatch(t *testing.T) {
	cases := []struct {
		s, p string
		want bool
	}{
		{"adceb", "*a*b", true}, {"aab", "*ab", true}, {"ab", "a?b", false},
		{"ab", "a*", true}, {"acdcb", "a*c?b", false}, {"", "**", true},
		{"世", "?", true}, {"漢字", "漢?", true},
	}
	for _, c := range cases {
		q, err := Compile(c.p)
		if err != nil {
			t.Fatalf("Compile(%q): %v", c.p, err)
		}
		got, err := q.Match(c.s)
		if err != nil || got != c.want {
			t.Errorf("(%q,%q)=%v,%v want %v", c.s, c.p, got, err, c.want)
		}
	}
}

func TestRejectionSentinels(t *testing.T) {
	cases := []struct {
		name string
		pat  string
		want error
	}{
		{"unsupported char", "a\x01b", ErrUnsupportedChar},
		{"invalid utf8", "a\xffb", ErrUnsupportedChar},
		{"pattern too long", strings.Repeat("a", 1<<15), ErrInputTooLong},
		{"too many wildcards", strings.Repeat("*", 1<<10), ErrTooManyWildcards},
	}
	for _, c := range cases {
		if _, err := Compile(c.pat); !errors.Is(err, c.want) {
			t.Errorf("%s: got %v want %v", c.name, err, c.want)
		}
	}
	// The three causes must be pairwise distinct.
	if errors.Is(ErrUnsupportedChar, ErrInputTooLong) ||
		errors.Is(ErrInputTooLong, ErrTooManyWildcards) ||
		errors.Is(ErrUnsupportedChar, ErrTooManyWildcards) {
		t.Fatal("sentinel errors are not pairwise distinct")
	}
}

func TestRejectionLeavesState(t *testing.T) {
	good, err := Compile("a*b")
	if err != nil {
		t.Fatal(err)
	}
	// Reject one bad pattern and one bad text; the compiled pattern must
	// behave identically before and after.
	before, _ := good.Match("azzzb")
	reject := []func(){
		func() { _, _ = Compile("a\x00b") },
		func() { _, _ = good.Match(strings.Repeat("a", 1<<15)) },
		func() { _, _ = Compile(strings.Repeat("*", 1<<10)) },
	}
	for i, r := range reject {
		r()
		after, err := good.Match("azzzb")
		if err != nil || after != before {
			t.Fatalf("rejection %d changed state: before=%v after=%v err=%v", i, before, after, err)
		}
	}
	if ok, _ := good.Match("acb"); !ok {
		t.Fatal("normal matching did not continue after rejections")
	}
}

func TestSelfCheck(t *testing.T) {
	if err := SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

func TestConcurrentMatch(t *testing.T) {
	q, err := Compile("*a*a?b")
	if err != nil {
		t.Fatal(err)
	}
	texts := make([]string, 200)
	for i := range texts {
		// Varied lengths ending in 'a' or 'b' to exercise backtracking.
		texts[i] = strings.Repeat("a", (i*7)%50) + string(rune('a'+i%2))
	}
	want := make([]bool, len(texts))
	for i, sx := range texts {
		want[i], _ = q.Match(sx)
	}
	var wg sync.WaitGroup
	got := make([]bool, len(texts))
	for i, sx := range texts {
		wg.Add(1)
		go func(i int, sx string) {
			defer wg.Done()
			got[i], _ = q.Match(sx)
		}(i, sx)
	}
	wg.Wait()
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("text %d: concurrent=%v serial=%v", i, got[i], want[i])
		}
	}
}
