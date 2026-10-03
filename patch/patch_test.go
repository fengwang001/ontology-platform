package patch_test

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"ontology/edit"
	"ontology/hunk"
	"ontology/patch"
	"ontology/udiff"
)

func mustApply(t *testing.T, src, p string, fuzz int) string {
	t.Helper()
	hs, err := udiff.Parse([]byte(p))
	if err != nil {
		t.Fatal(err)
	}
	out, err := patch.Apply([]byte(src), hs, fuzz)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func TestOffset(t *testing.T) {
	cases := []struct{ name, src, p, want string }{
		{"shifted", "0\n0\n1\n2\n3\n", "--- a\n+++ b\n@@ -2 +2 @@\n-2\n+X\n", "0\n0\n1\nX\n3\n"},
		{"nearest", "z\nold\nz\nold\n", "--- a\n+++ b\n@@ -3 +3 @@\n-old\n+new\n", "z\nnew\nz\nold\n"},
		{"tie-first", "old\nz\nold\n", "--- a\n+++ b\n@@ -2 +2 @@\n-old\n+new\n", "new\nz\nold\n"},
	}
	for _, c := range cases {
		if got := mustApply(t, c.src, c.p, 3); got != c.want {
			t.Fatalf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

func TestAtomicReject(t *testing.T) {
	src := "1\n2\n3\n4\n5\n"
	p := "--- a\n+++ b\n@@ -1 +1 @@\n-1\n+A\n@@ -5 +5 @@\n-NINE\n+B\n"
	hs, err := udiff.Parse([]byte(p))
	if err != nil {
		t.Fatal(err)
	}
	out, err := patch.Apply([]byte(src), hs, 0)
	if out != nil || !errors.Is(err, patch.ErrContext) || !strings.Contains(err.Error(), "hunk 2") {
		t.Fatalf("out=%q err=%v", out, err)
	}
}

func TestErrorClasses(t *testing.T) {
	if _, err := udiff.Parse([]byte("junk")); !errors.Is(err, udiff.ErrFormat) {
		t.Fatalf("format: %v", err)
	}
	parse := func(s string) []hunk.Hunk {
		hs, err := udiff.Parse([]byte(s))
		if err != nil {
			t.Fatal(err)
		}
		return hs
	}
	_, err := patch.Apply([]byte("x\n"), parse("--- a\n+++ b\n@@ -1 +1 @@\n-y\n+z\n"), 0)
	if !errors.Is(err, patch.ErrContext) {
		t.Fatalf("context: %v", err)
	}
	far := strings.Repeat("p\n", 50) + "y\n"
	_, err = patch.Apply([]byte(far), parse("--- a\n+++ b\n@@ -1 +1 @@\n-y\n+z\n"), 2)
	if !errors.Is(err, patch.ErrOffset) {
		t.Fatalf("offset: %v", err)
	}
	_, err = edit.Diff([]string{"1", "2"}, []string{"a", "b"}, 1)
	if !errors.Is(err, edit.ErrTooLarge) {
		t.Fatalf("too large: %v", err)
	}
}

func TestLimits(t *testing.T) {
	src := "1\n2\n"
	p := udiff.Diff([]byte(src), []byte("1\nX\n2\nY\n"), 0)
	st := patch.NewStore(patch.Options{MaxBytes: len(p) - 1})
	st.Put("d", []byte(src))
	if err := st.Apply("d", p); !errors.Is(err, patch.ErrLimit) {
		t.Fatalf("bytes: %v", err)
	}
	st = patch.NewStore(patch.Options{MaxHunks: 1})
	st.Put("d", []byte(src))
	if err := st.Apply("d", p); !errors.Is(err, patch.ErrLimit) {
		t.Fatalf("hunks: %v", err)
	}
	if got, v := st.Text("d"); string(got) != src || v != 0 {
		t.Fatalf("state changed: %q v=%d", got, v)
	}
}

func TestConcurrent(t *testing.T) {
	const n = 16
	base := "l0\nl1\nl2\nl3\nl4\nl5\nl6\nl7\n"
	patches := make([][]byte, n)
	for i := 0; i < n; i++ {
		mod := strings.Replace(base, fmt.Sprintf("l%d\n", i%8), fmt.Sprintf("w%d\n", i), 1)
		patches[i] = udiff.Diff([]byte(base), []byte(mod), 0)
	}
	st := patch.NewStore(patch.Options{})
	st.Put("doc", []byte(base))
	var wg sync.WaitGroup
	fails := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := st.Apply("doc", patches[i]); err != nil {
				fails <- err
			}
		}(i)
	}
	wg.Wait()
	close(fails)
	nFail := 0
	for range fails {
		nFail++
	}
	nOK := n - nFail
	final, ver := st.Text("doc")
	if ver != nOK {
		t.Fatalf("version %d != successes %d", ver, nOK)
	}
	text := []byte(base)
	for _, p := range st.Log() {
		hs, err := udiff.Parse(p)
		if err != nil {
			t.Fatal(err)
		}
		if text, err = patch.Apply(text, hs, 0); err != nil {
			t.Fatal(err)
		}
	}
	if string(text) != string(final) {
		t.Fatalf("replay %q != final %q", text, final)
	}
}
