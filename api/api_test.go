package api

import (
	"errors"
	"math/rand"
	"reflect"
	"sync"
	"testing"

	"ontology/ac"
)

type tcase struct {
	pats []string
	text string
}

func unbuild() { mu.Lock(); cur = nil; mu.Unlock() }
func rstr(r *rand.Rand, n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = "ab"[r.Intn(2)]
	}
	return string(b)
}

// cases returns fixed edge cases plus loop-generated random ones.
func cases() []tcase {
	cs := []tcase{
		{[]string{"a", "ab", "bab"}, "abab"},
		{[]string{"aa", "aaa"}, "aaaaa"},
		{[]string{"he", "she", "hers"}, "ushers"},
		{[]string{"ab", "ba", "aba"}, "ababa"},
		{[]string{"x"}, ""},
	}
	r := rand.New(rand.NewSource(7))
	for c := 0; c < 20; c++ {
		pats := make([]string, 1+r.Intn(6))
		for i := range pats {
			pats[i] = rstr(r, 1+r.Intn(5))
		}
		cs = append(cs, tcase{pats, rstr(r, r.Intn(150))})
	}
	return cs
}
func TestMatchAgainstNaive(t *testing.T) {
	for _, c := range cases() {
		if err := New(c.pats); err != nil {
			t.Fatal(err)
		}
		got, err := Match(c.text)
		if err != nil {
			t.Fatal(err)
		}
		if !sameMultiset(got, naive(c.pats, c.text)) {
			t.Errorf("pats=%v text=%q: %v != naive", c.pats, c.text, got)
		}
	}
}
func TestPositionSelfConsistent(t *testing.T) {
	for _, c := range cases() {
		if err := New(c.pats); err != nil {
			t.Fatal(err)
		}
		got, _ := Match(c.text)
		for _, m := range got {
			p := c.pats[m.Pattern]
			if m.End < len(p) || m.End > len(c.text) || c.text[m.End-len(p):m.End] != p {
				t.Errorf("pats=%v text=%q: bad position %+v", c.pats, c.text, m)
			}
		}
	}
}
func TestErrorsDistinct(t *testing.T) {
	unbuild()
	_, nbErr := Match("x")
	if err := New([]string{"ok"}); err != nil {
		t.Fatal(err)
	}
	_, textErr := Match("ab\xff")
	rejections := map[error]error{
		nbErr: ErrNotBuilt, New(nil): ErrEmptyPatterns,
		New([]string{""}): ErrEmptyPattern, New([]string{"\xff"}): ErrInvalidUTF8,
		textErr: ErrInvalidUTF8,
	}
	sents := []error{ErrEmptyPatterns, ErrEmptyPattern, ErrInvalidUTF8, ErrNotBuilt}
	for err, sent := range rejections {
		for _, s := range sents {
			if errors.Is(err, s) != (s == sent) {
				t.Errorf("errors.Is(%v, %v) wrong", err, s)
			}
		}
	}
	var u *UTF8Error
	if !errors.As(textErr, &u) || u.Offset != 2 {
		t.Errorf("offset = %+v, want 2", u)
	}
}
func TestStateUnchangedAfterRejection(t *testing.T) {
	unbuild()
	if err := New([]string{"he", "she", "hers"}); err != nil {
		t.Fatal(err)
	}
	base, err := Match("ushers")
	if err != nil {
		t.Fatal(err)
	}
	_ = New(nil)
	_ = New([]string{""})
	_ = New([]string{"\xff"})
	_, _ = Match("\xff")
	after, err := Match("ushers")
	if err != nil || !reflect.DeepEqual(base, after) {
		t.Fatal("rejected ops changed state")
	}
}
func TestConcurrentMatch(t *testing.T) {
	unbuild()
	if err := New([]string{"a", "ab", "bab", "ba"}); err != nil {
		t.Fatal(err)
	}
	want, err := Match("abababab")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	bad := make(chan []ac.Match, 8*50)
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				if got, _ := Match("abababab"); !reflect.DeepEqual(got, want) {
					bad <- got
				}
			}
		}()
	}
	wg.Wait()
	close(bad)
	for got := range bad {
		t.Errorf("concurrent %v != %v", got, want)
	}
}
func TestSelfCheck(t *testing.T) {
	if err := SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
