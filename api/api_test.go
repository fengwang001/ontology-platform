package api

import (
	"errors"
	"math/rand"
	"strings"
	"sync"
	"testing"

	"ontology/ops"
)

func randStr(r *rand.Rand, n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte('a' + r.Intn(4))
	}
	return string(b)
}

func mutate(a string, r *rand.Rand, edits int) string {
	b := []byte(a)
	for e := 0; e < edits && len(b) > 0; e++ {
		p := r.Intn(len(b))
		switch r.Intn(3) {
		case 0:
			b = append(b, 0)
			copy(b[p+1:], b[p:])
			b[p] = byte('a' + r.Intn(4))
		case 1:
			b = append(b[:p], b[p+1:]...)
		default:
			b[p] = byte('a' + r.Intn(4))
		}
	}
	return string(b)
}

func TestEditScriptValid(t *testing.T) {
	r := rand.New(rand.NewSource(3))
	c, err := New(20)
	if err != nil {
		t.Fatal(err)
	}
	pairs := [][2]string{{"abc", "yabd"}, {"", ""}, {"", "xyz"}, {"kitten", "sitting"}}
	for i := 0; i < 60; i++ {
		a := randStr(r, r.Intn(15))
		pairs = append(pairs, [2]string{a, mutate(a, r, r.Intn(6))})
	}
	for _, p := range pairs {
		script, err := c.EditScript(p[0], p[1])
		if err != nil {
			t.Fatalf("(%q,%q): %v", p[0], p[1], err)
		}
		got, err := ops.Apply(p[0], script)
		if err != nil || got != p[1] {
			t.Fatalf("(%q,%q): apply got %q, %v", p[0], p[1], got, err)
		}
		d, _ := c.Distance(p[0], p[1])
		if len(script) != d {
			t.Fatalf("(%q,%q): script len %d != distance %d", p[0], p[1], len(script), d)
		}
	}
}

func TestErrorsDistinct(t *testing.T) {
	if _, err := New(-1); !errors.Is(err, ErrNegativeCap) {
		t.Fatalf("New(-1): %v", err)
	}
	c, _ := New(1)
	if _, err := c.Distance("abc", "yabd"); !errors.Is(err, ErrExceedsCap) {
		t.Fatalf("over cap: %v", err)
	}
	if _, err := c.Distance(strings.Repeat("x", maxLen), "y"); !errors.Is(err, ErrTooLong) {
		t.Fatalf("too long: %v", err)
	}
	if _, err := c.EditScript(strings.Repeat("x", maxLen), "y"); !errors.Is(err, ErrTooLong) {
		t.Fatalf("too long script: %v", err)
	}
	for _, e := range [][2]error{{ErrNegativeCap, ErrTooLong}, {ErrNegativeCap, ErrExceedsCap}, {ErrTooLong, ErrExceedsCap}} {
		if errors.Is(e[0], e[1]) {
			t.Fatalf("errors not distinct: %v vs %v", e[0], e[1])
		}
	}
}

func TestRejectionKeepsState(t *testing.T) {
	c, _ := New(2)
	before, err := c.Distance("abc", "yabd")
	if err != nil || before != 2 {
		t.Fatalf("setup: %d, %v", before, err)
	}
	_, _ = New(-1)                                     // rejected: negative cap
	_, _ = c.Distance(strings.Repeat("x", maxLen), "") // rejected: too long
	_, _ = c.Distance("abcde", "vwxyz")                // rejected: exceeds cap
	after, err := c.Distance("abc", "yabd")
	if err != nil || after != before {
		t.Fatalf("state changed: before %d, after %d, %v", before, after, err)
	}
	s, err := c.EditScript("abc", "yabd") // still fully usable
	if err != nil || len(s) != 2 {
		t.Fatalf("after rejections: %v, %d", err, len(s))
	}
}

func TestConcurrentDistance(t *testing.T) {
	c, _ := New(10)
	r := rand.New(rand.NewSource(4))
	pairs := make([][2]string, 32)
	for i := range pairs {
		a := randStr(r, 1+r.Intn(20))
		pairs[i] = [2]string{a, mutate(a, r, r.Intn(5))}
	}
	want := make([]int, len(pairs))
	for i, p := range pairs {
		want[i], _ = c.Distance(p[0], p[1])
	}
	got := make([]int, len(pairs))
	var wg sync.WaitGroup
	for i, p := range pairs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got[i], _ = c.Distance(p[0], p[1])
			if _, err := c.EditScript(p[0], p[1]); err != nil {
				t.Error(err)
			}
			if err := c.SelfCheck(); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	for i := range pairs {
		if got[i] != want[i] {
			t.Fatalf("pair %d: concurrent %d != serial %d", i, got[i], want[i])
		}
	}
}

func TestSelfCheck(t *testing.T) {
	for _, k := range []int{0, 1, 2, 10} {
		if c, err := New(k); err != nil {
			t.Fatal(err)
		} else if err := c.SelfCheck(); err != nil {
			t.Fatalf("k=%d: %v", k, err)
		}
	}
}
