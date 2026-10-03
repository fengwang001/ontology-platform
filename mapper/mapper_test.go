package mapper

import (
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"testing"
)

func mustAdd(t *testing.T, m *Mapper, parent int, src string, isDir bool) int {
	t.Helper()
	id, err := m.Add(parent, src, isDir)
	if err != nil {
		t.Fatalf("Add(%q) unexpected error: %v", src, err)
	}
	return id
}

func TestBaseMappingExamples(t *testing.T) {
	cases := []struct{ in, want string }{
		{"a:b?.", "a%3Ab%3F%2E"},
		{"CON", "%43ON"},
		{"NUL.", "NUL%2E"},
		{"Aux.txt", "%41ux.txt"},
	}
	for _, c := range cases {
		if got := baseMap(c.in, 255); got != c.want {
			t.Errorf("baseMap(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestEscapingInjective(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	alphabet := []rune("abcABC%.: <>*?|\\\"" + "\x01你好ðzZ")
	seen := map[string]string{} // exact (case-sensitive) mapped name
	for i := 0; i < 5000; i++ {
		n := 1 + rng.Intn(12)
		var b strings.Builder
		for j := 0; j < n; j++ {
			b.WriteRune(alphabet[rng.Intn(len(alphabet))])
		}
		src := b.String()
		got := baseMap(src, 255)
		if prev, ok := seen[got]; ok && prev != src {
			t.Fatalf("mapped collision: %q vs %q -> %q", prev, src, got)
		}
		seen[got] = src
		if strings.ContainsAny(got, "<>:\"\\|?*") {
			t.Fatalf("forbidden char survives in %q from %q", got, src)
		}
	}

	// The escape marker itself is injective because raw '%' becomes %25,
	// so escaped output can never be confused with a literal percent sign:
	// "con%2E" (literal) differs from "NUL." (dot trailing rule).
	if baseMap("con%2E", 255) == baseMap("NUL.", 255) {
		t.Fatal("percent escaping not injective")
	}
	if baseMap("a%3A", 255) != "a%253A" {
		t.Fatalf("literal percent must escape first: %q", baseMap("a%3A", 255))
	}
}

func TestReservedStems(t *testing.T) {
	cases := []struct{ in, want string }{
		{"con", "%63on"},
		{"Prn.txt", "%50rn.txt"},
		{"lpt9.log", "%6Cpt9.log"},
		{".con", ".con"},
		{"con.x", "%63on.x"},
		{"con%2E", "con%252E"}, // literal '%' escapes before anything else
		{"CON ", "CON%20"},
		{"AUX.", "AUX%2E"},
	}
	for _, c := range cases {
		if got := baseMap(c.in, 255); got != c.want {
			t.Errorf("baseMap(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestTruncationAtTokenBoundary(t *testing.T) {
	in := strings.Repeat(":b", 8) // 8 raw ':' and 'b': 8*3+8 = 32 bytes
	got := baseMap(in, 16)
	if len(got) > 16 || !strings.Contains(got, "~") {
		t.Fatalf("got %q len %d", got, len(got))
	}
	head := got[:strings.IndexByte(got, '~')]
	if strings.HasSuffix(head, "%") || strings.HasSuffix(head, "%3") {
		t.Fatalf("escape token cut in half: %q", got)
	}

	long := strings.Repeat("好", 200)
	got = baseMap(long, 40)
	if len(got) > 40 {
		t.Fatalf("len %d", len(got))
	}
	head = got[:len(got)-9]
	if !strings.HasSuffix(head, "好") {
		t.Fatalf("rune cut: %q", got)
	}
	if baseMap(long, 40) != got {
		t.Fatal("hash mapping not deterministic")
	}

	esc := strings.Repeat(":", 50)
	got = baseMap(esc, 16)
	tail := got[strings.IndexByte(got, '~'):]
	prefix := got[:strings.IndexByte(got, '~')]
	for i := 0; i < len(prefix); {
		if got[i] == '%' {
			if i+3 > len(prefix) {
				t.Fatalf("partial escape before %q: %q", tail, got)
			}
			i += 3
		} else {
			i++
		}
	}
}

func TestUniquenessSequence(t *testing.T) {
	m := New(Config{MaxBytes: 64, MaxPath: 4096, MaxEntries: 100})
	mustAdd(t, m, 0, "Readme.md", false)
	mustAdd(t, m, 0, "README.md", false)
	mustAdd(t, m, 0, "readme.MD", false)
	names, err := m.Names(0)
	if err != nil || fmt.Sprint(names) != "[README~2.md Readme.md readme~3.MD]" {
		t.Fatalf("names=%v err=%v", names, err)
	}

	if err := m.Remove(0, "Readme.md"); err != nil {
		t.Fatal(err)
	}
	mustAdd(t, m, 0, "ReadMe.md", false)
	names, _ = m.Names(0)
	if fmt.Sprint(names) != "[README~2.md ReadMe.md readme~3.MD]" {
		t.Fatalf("after reuse names=%v", names)
	}
}

func TestCollisionCandidateShortensBase(t *testing.T) {
	m := New(Config{MaxBytes: 16, MaxPath: 4096, MaxEntries: 100})
	mustAdd(t, m, 0, "aaaaaaaaaaaaaaaa", false) // 16 a's
	id, err := m.Add(0, "AAAAAAAAAAAAAAAA", false)
	if err != nil {
		t.Fatal(err)
	}
	if p, _ := m.Path(id); p != "AAAAAAAAAAAAAA~2" || len(p) != 16 {
		t.Fatalf("path=%q", p)
	}

	m2 := New(Config{MaxBytes: 16, MaxPath: 4096, MaxEntries: 100})
	mustAdd(t, m2, 0, "AAAAAAAAAAAA:", false)   // "AAAAAAAAAAAA%3A" = 15 bytes
	id, err = m2.Add(0, "aaaaaaaaaaaa:", false) // fold-collides
	if err != nil {
		t.Fatal(err)
	}
	// The trailing %3A escape token must drop as a whole.
	if p, _ := m2.Path(id); p != "aaaaaaaaaaaa~2" || len(p) != 14 {
		t.Fatalf("path=%q", p)
	}
}

func TestCandidateSkipNumber(t *testing.T) {
	m := New(Config{MaxBytes: 64, MaxPath: 4096, MaxEntries: 100})
	mustAdd(t, m, 0, "a", false)
	mustAdd(t, m, 0, "a~2", false)
	id, err := m.Add(0, "A", false)
	if err != nil {
		t.Fatal(err)
	}
	if p, _ := m.Path(id); p != "A~3" {
		t.Fatalf("got %q, want A~3", p)
	}
}

func TestRenameReleaseBeforeAllocate(t *testing.T) {
	m := New(Config{MaxBytes: 64, MaxPath: 4096, MaxEntries: 100})
	id := mustAdd(t, m, 0, "Readme.md", false)
	if err := m.Rename(0, "Readme.md", "readme.md"); err != nil {
		t.Fatal(err)
	}
	if p, _ := m.Path(id); p != "readme.md" {
		t.Fatalf("path=%q", p)
	}
	if err := m.Rename(0, "readme.md", "readme.md"); err != nil {
		t.Fatal(err)
	}

	mustAdd(t, m, 0, "other", false)
	if err := m.Rename(0, "readme.md", "other"); !errors.Is(err, ErrExists) {
		t.Fatalf("err=%v", err)
	}
	if got, err := m.Lookup(0, "readme.md"); err != nil || got != id {
		t.Fatalf("state changed after failed rename: %d %v", got, err)
	}
}

func TestRenameDirSubtreePathCheck(t *testing.T) {
	m := New(Config{MaxBytes: 16, MaxPath: 24, MaxEntries: 100})
	dir := mustAdd(t, m, 0, "projects", true)
	file := mustAdd(t, m, dir, "readme.txt", false)
	if p, _ := m.Path(file); p != "projects/readme.txt" {
		t.Fatalf("setup path %q", p)
	}
	err := m.Rename(0, "projects", "projects-archive")
	if !errors.Is(err, ErrPathTooLong) {
		t.Fatalf("err=%v", err)
	}
	if p, _ := m.Path(dir); p != "projects" {
		t.Fatalf("dir path after rollback %q", p)
	}
	if p, _ := m.Path(file); p != "projects/readme.txt" {
		t.Fatalf("file path after rollback %q", p)
	}
	names, _ := m.Names(0)
	if len(names) != 1 || names[0] != "projects" {
		t.Fatalf("root names after rollback %v", names)
	}

	if err := m.Rename(0, "projects", "proj"); err != nil {
		t.Fatal(err)
	}
	if p, _ := m.Path(file); p != "proj/readme.txt" || len(p) > 24 {
		t.Fatalf("path %q", p)
	}
}

func TestErrorsAndPriority(t *testing.T) {
	m := New(Config{MaxBytes: 16, MaxPath: 16, MaxEntries: 1})
	if _, err := m.Add(0, "a/b", false); !errors.Is(err, ErrInvalidName) {
		t.Fatalf("invalid name err=%v", err)
	}
	if _, err := m.Add(99, "x", false); !errors.Is(err, ErrNoParent) {
		t.Fatalf("no parent err=%v", err)
	}
	leaf := mustAdd(t, m, 0, "leaf", false)
	if _, err := m.Add(leaf, "x", false); !errors.Is(err, ErrNoParent) {
		t.Fatalf("file parent err=%v", err)
	}
	if _, err := m.Add(0, "leaf", false); !errors.Is(err, ErrExists) {
		t.Fatalf("exists err=%v", err)
	}
	if _, err := m.Add(0, "z", false); !errors.Is(err, ErrFull) {
		t.Fatalf("full err=%v", err)
	}

	m2 := New(Config{MaxBytes: 16, MaxPath: 16, MaxEntries: 10})
	d := mustAdd(t, m2, 0, "abcdefghijklmnop", true) // 16-byte dir name
	if _, err := m2.Add(d, "x", false); !errors.Is(err, ErrPathTooLong) {
		t.Fatalf("path too long err=%v", err)
	}
}

func TestRemoveSemantics(t *testing.T) {
	m := New(Config{MaxBytes: 16, MaxPath: 4096, MaxEntries: 10})
	if err := m.Remove(0, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v", err)
	}
	d := mustAdd(t, m, 0, "d", true)
	mustAdd(t, m, d, "f", false)
	if err := m.Remove(0, "d"); !errors.Is(err, ErrNotEmpty) {
		t.Fatalf("err=%v", err)
	}
	if err := m.Remove(d, "f"); err != nil {
		t.Fatal(err)
	}
	if err := m.Remove(0, "d"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Lookup(0, "d"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v", err)
	}
}

func TestPathAndNames(t *testing.T) {
	m := New(Config{MaxBytes: 64, MaxPath: 4096, MaxEntries: 100})
	if p, err := m.Path(0); err != nil || p != "" {
		t.Fatalf("root path=%q err=%v", p, err)
	}
	if _, err := m.Names(9); !errors.Is(err, ErrNotFound) {
		t.Fatalf("names err=%v", err)
	}
	d := mustAdd(t, m, 0, "Dir", true)
	f := mustAdd(t, m, d, "F", false)
	if p, _ := m.Path(f); p != "Dir/F" {
		t.Fatalf("path=%q", p)
	}
	ns, _ := m.Names(d)
	if len(ns) != 1 || ns[0] != "F" {
		t.Fatalf("names=%v", ns)
	}
	if _, err := m.Path(12345); !errors.Is(err, ErrNotFound) {
		t.Fatalf("path err=%v", err)
	}
}

func TestConcurrentAccess(t *testing.T) {
	m := New(Config{MaxBytes: 64, MaxPath: 4096, MaxEntries: 100000})
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				name := fmt.Sprintf("g%d_n%d", g, i)
				if _, err := m.Add(0, name, false); err != nil {
					t.Errorf("add: %v", err)
					continue
				}
				if err := m.Rename(0, name, name+"x"); err != nil {
					t.Errorf("rename: %v", err)
				}
				if err := m.Remove(0, name+"x"); err != nil {
					t.Errorf("remove: %v", err)
				}
			}
		}(g)
	}
	wg.Wait()
	ns, _ := m.Names(0)
	if len(ns) != 0 {
		t.Fatalf("leftover entries: %v", ns)
	}
}

func TestCannotFitCandidate(t *testing.T) {
	m := New(Config{MaxBytes: 16, MaxPath: 4096, MaxEntries: 100})
	// t0 = "a." + 14 q's = 16 bytes: base 1 byte, ext 15 bytes. The "~2"
	// candidate needs 1+2+15=18; dropping the only base token still leaves
	// 2+15=17 > 16, so fitting is impossible.
	first := "a." + strings.Repeat("q", 14)
	mustAdd(t, m, 0, first, false)
	upper := "A." + strings.Repeat("Q", 14)
	if _, err := m.Add(0, upper, false); !errors.Is(err, ErrCannotFit) {
		t.Fatalf("err=%v", err)
	}
	// Failed Add changed nothing.
	if ns, _ := m.Names(0); len(ns) != 1 {
		t.Fatalf("names=%v", ns)
	}
}

func TestInvalidNameVariants(t *testing.T) {
	m := New(Config{MaxBytes: 16, MaxPath: 16, MaxEntries: 10})
	for _, bad := range []string{"", "a/b", "a\x00b", "\xff", "x/y"} {
		if _, err := m.Add(0, bad, false); !errors.Is(err, ErrInvalidName) {
			t.Fatalf("Add(%q) err=%v", bad, err)
		}
		if err := m.Rename(0, "x", bad); !errors.Is(err, ErrInvalidName) {
			t.Fatalf("Rename(%q) err=%v", bad, err)
		}
	}
}
