package patch_test

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/edit"
	"ontology/patch"
	"ontology/udiff"
)

func mustParse(t *testing.T, s string) *udiff.Patch {
	t.Helper()
	p, err := udiff.Parse([]byte(s), udiff.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestOffset(t *testing.T) {
	// src has "b\na" at lines 2-3 and 4-5; header records line 3.
	src := []byte("z\nb\na\nb\na\n")
	cases := []struct {
		name, hdr, want string
		fuzz            int
	}{
		{"tie picks earlier", "@@ -3,2 +3,2 @@", "z\nB\na\nb\na\n", 3},
		{"nearest wins", "@@ -5,2 +5,2 @@", "z\nb\na\nB\na\n", 3},
	}
	for _, c := range cases {
		p := mustParse(t, "--- a\n+++ b\n"+c.hdr+"\n-b\n+B\n a\n")
		out, err := patch.Apply(src, p, c.fuzz)
		if err != nil || string(out) != c.want {
			t.Fatalf("%s: got %q, %v", c.name, out, err)
		}
	}
	// drift propagation: hunk 1 applies at +2, hunk 2 must follow the shift
	p := mustParse(t, "--- a\n+++ b\n@@ -1 +1 @@\n-a\n+A\n@@ -3 +3 @@\n-c\n+C\n")
	out, err := patch.Apply([]byte("i\ni\na\nb\nc\n"), p, 3)
	if err != nil || string(out) != "i\ni\nA\nb\nC\n" {
		t.Fatalf("drift: got %q, %v", out, err)
	}
}

func TestAtomic(t *testing.T) {
	p := mustParse(t, "--- a\n+++ b\n@@ -1 +1 @@\n-a\n+A\n@@ -3 +3 @@\n-c\n+C\n")
	src := []byte("a\nb\nX\n")
	if _, err := patch.Apply(src, p, 0); !errors.Is(err, patch.ErrContext) {
		t.Fatalf("want ErrContext hunk 2, got %v", err)
	}
	s := patch.NewStore()
	s.Put("d", src)
	if _, err := s.Apply("d", p, 0); err == nil {
		t.Fatal("want error")
	}
	got, ver := s.Get("d")
	if string(got) != string(src) || ver != 0 || len(s.Log()) != 0 {
		t.Fatalf("state changed on failed apply: %q v%d", got, ver)
	}
}

func TestErrorClasses(t *testing.T) {
	_, ferr := udiff.Parse([]byte("junk\n"), udiff.Limits{})
	shift := mustParse(t, "--- a\n+++ b\n@@ -1 +1 @@\n-a\n+A\n")
	far := []byte("x\nx\nx\nx\nx\na\n")
	_, oerr := patch.Apply(far, shift, 2)
	none := mustParse(t, "--- a\n+++ b\n@@ -1 +1 @@\n-zzz\n+A\n")
	_, cerr := patch.Apply(far, none, 2)
	_, terr := edit.Diff([]string{"1\n", "2\n"}, []string{"3\n", "4\n"}, 1)
	cases := []struct {
		err  error
		want error
	}{
		{ferr, udiff.ErrFormat}, {cerr, patch.ErrContext},
		{oerr, patch.ErrOffset}, {terr, edit.ErrTooBig},
	}
	alls := []error{udiff.ErrFormat, patch.ErrContext, patch.ErrOffset, edit.ErrTooBig}
	for _, c := range cases {
		for _, s := range alls {
			if errors.Is(c.err, s) != (s == c.want) {
				t.Fatalf("err %v: errors.Is(%v) wrong", c.err, s)
			}
		}
	}
}

func TestTruncateAndFlip(t *testing.T) {
	a := []byte("l1\nl2\nl3\nl4\nl5\nl6\nl7\nl8\n")
	b := []byte("l1\nL2\nl3\nl4\nl5\nl6\nL7\nl8\n")
	p, _ := udiff.Diff(a, b, 1, -1)
	data := udiff.Render(p)
	for i := 0; i <= len(data); i++ {
		q, err := udiff.Parse(data[:i], udiff.Limits{})
		if err != nil {
			continue
		}
		out, err := patch.Apply(a, q, 0)
		if err != nil {
			continue
		}
		if back, err := patch.Reverse(out, q, 0); err != nil || string(back) != string(a) {
			t.Fatalf("truncation %d: applied but not explainable", i)
		}
	}
	lines := strings.Split(string(data), "\n")
	for i, ln := range lines {
		variants := []string{}
		if ln != "" && (ln[0] == ' ' || ln[0] == '-' || ln[0] == '+') {
			variants = append(variants, string(map[byte]byte{' ': '-', '-': '+', '+': ' '}[ln[0]])+ln[1:])
		}
		if j := strings.IndexAny(ln, "0123456789"); j >= 0 && !strings.HasPrefix(ln, "+") {
			d := ln[j]
			variants = append(variants, ln[:j]+string('0'+(d-'0'+1)%10)+ln[j+1:])
		}
		for _, v := range variants {
			cp := append([]string(nil), lines...)
			cp[i] = v
			q, err := udiff.Parse([]byte(strings.Join(cp, "\n")), udiff.Limits{})
			if err != nil {
				continue
			}
			if _, err := patch.Apply(a, q, 0); err == nil {
				t.Fatalf("flip line %d to %q: accepted", i, v)
			}
		}
	}
}

func TestLimits(t *testing.T) {
	p, _ := udiff.Diff([]byte("a\nb\nc\nd\ne\n"), []byte("A\nb\nc\nd\nE\n"), 0, -1)
	data := udiff.Render(p)
	if _, err := udiff.Parse(data, udiff.Limits{MaxBytes: len(data) - 1}); !errors.Is(err, udiff.ErrLimit) {
		t.Fatalf("MaxBytes: got %v", err)
	}
	if _, err := udiff.Parse(data, udiff.Limits{MaxHunks: 1}); !errors.Is(err, udiff.ErrLimit) {
		t.Fatalf("MaxHunks: got %v", err)
	}
	if _, err := udiff.Parse(data, udiff.Limits{MaxBytes: len(data), MaxHunks: 2}); err != nil {
		t.Fatalf("within limits: %v", err)
	}
}

func TestStoreConcurrent(t *testing.T) {
	s := patch.NewStore()
	src := []byte("a\nb\nc\n")
	s.Put("d", src)
	const N = 16
	var okN, badN atomic.Int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < N; i++ {
		p := mustParse(t, fmt.Sprintf("--- a\n+++ b\n@@ -2 +2 @@\n-b\n+B%d\n", i))
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if _, err := s.Apply("d", p, 0); err == nil {
				okN.Add(1)
			} else {
				badN.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()
	if okN.Load()+badN.Load() != N {
		t.Fatalf("ok %d + bad %d != %d", okN.Load(), badN.Load(), N)
	}
	got, ver := s.Get("d")
	if int64(ver) != okN.Load() || len(s.Log()) != int(ver) {
		t.Fatalf("version %d, ok %d, log %d", ver, okN.Load(), len(s.Log()))
	}
	cur := src
	for _, c := range s.Log() {
		var err error
		if cur, err = patch.Apply(cur, c.Patch, 0); err != nil {
			t.Fatal(err)
		}
	}
	if string(got) != string(cur) {
		t.Fatalf("final %q != serial replay %q", got, cur)
	}
}
