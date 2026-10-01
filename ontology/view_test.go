package ontology

import (
	"errors"
	"reflect"
	"sort"
	"testing"
)

func mustNew(t *testing.T, lower map[string]string) *View {
	t.Helper()
	v, err := New(lower)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return v
}

func lookupKind(v *View, p string) (NodeType, error) {
	r, err := v.Lookup(p)
	if err != nil {
		return TypeMissing, err
	}
	return r.Type, nil
}

func TestNewRejectsInvalidLower(t *testing.T) {
	cases := []map[string]string{
		{"/b": "x"},
		{"a/../b": "x"},
		{"a//b": "x"},
		{"a//": "x"},
		{"a": "x", "a/": ""},
		{"a/b": "x"},
		{"a": "x", "a/b": "x"},
		{"a/": "", "a/b/c": "x"}, // missing parent b
	}
	for i, c := range cases {
		if _, err := New(c); err == nil {
			t.Fatalf("case %d expected error, got nil: %v", i, c)
		} else if !errors.Is(err, ErrInvalidPath) {
			t.Fatalf("case %d expected ErrInvalidPath, got %v", i, err)
		}
	}
}

func TestRemoveLowerFileWhiteout(t *testing.T) {
	v := mustNew(t, map[string]string{"a/": "", "a/f": "lo"})
	if err := v.Remove("a/f"); err != nil {
		t.Fatal(err)
	}
	up := v.Upper()
	if up["a"].Kind != KindDir {
		t.Fatalf("a should be plain dir, got %v", up["a"])
	}
	if up["a/f"].Kind != KindWhiteout {
		t.Fatalf("a/f should be whiteout, got %v", up["a/f"])
	}
	if _, err := v.Lookup("a/f"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a/f should be hidden: %v", err)
	}
	// Pure upper file removal leaves no whiteout.
	if err := v.Write("u", "up"); err != nil {
		t.Fatal(err)
	}
	if err := v.Remove("u"); err != nil {
		t.Fatal(err)
	}
	if _, ok := v.Upper()["u"]; ok {
		t.Fatalf("upper-only file removal must leave no record")
	}
}

func TestRemoveLowerDirRecreateOpaque(t *testing.T) {
	v := mustNew(t, map[string]string{"b/": "", "b/g": "g"})
	if err := v.Remove("b/g"); err != nil {
		t.Fatal(err)
	}
	// Remove the (now empty-merged) lower dir: whiteout, then recreate as
	// opaque via Mkdir on the whiteout.
	if err := v.Remove("b"); err != nil {
		t.Fatal(err)
	}
	if v.Upper()["b"].Kind != KindWhiteout {
		t.Fatalf("b whiteout expected")
	}
	if err := v.Mkdir("b"); err != nil {
		t.Fatal(err)
	}
	if v.Upper()["b"].Kind != KindOpaque {
		t.Fatalf("b should be opaque, got %v", v.Upper()["b"])
	}
	names, err := v.ReadDir("b")
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 0 {
		t.Fatalf("g must not resurrect, got %v", names)
	}
	// Removing the opaque dir still writes a whiteout (it hides lower b).
	if err := v.Remove("b"); err != nil {
		t.Fatal(err)
	}
	if v.Upper()["b"].Kind != KindWhiteout {
		t.Fatalf("removing opaque dir over lower must whiteout, got %v", v.Upper()["b"])
	}
}

func TestRemoveUnderOpaqueAncestorNoWhiteout(t *testing.T) {
	v := mustNew(t, map[string]string{"d/": "", "d/s/": "", "d/s/f": "x"})
	// Empty d/s, white it out, then recreate as opaque.
	if err := v.Remove("d/s/f"); err != nil {
		t.Fatal(err)
	}
	if err := v.Remove("d/s"); err != nil {
		t.Fatal(err)
	}
	if err := v.Mkdir("d/s"); err != nil {
		t.Fatal(err)
	}
	if err := v.Write("d/s/up", "1"); err != nil {
		t.Fatal(err)
	}
	if err := v.Remove("d/s/up"); err != nil {
		t.Fatal(err)
	}
	if _, ok := v.Upper()["d/s/up"]; ok {
		t.Fatalf("upper-only file under opaque ancestor: no record should remain")
	}
}

func TestWriteReplacesWhiteoutHidesLowerDir(t *testing.T) {
	v := mustNew(t, map[string]string{"a/": "", "a/f/": "", "a/f/x": "x"})
	if err := v.Remove("a/f/x"); err != nil {
		t.Fatal(err)
	}
	if err := v.Remove("a/f"); err != nil {
		t.Fatal(err)
	}
	if err := v.Write("a/f", "data"); err != nil {
		t.Fatal(err)
	}
	r, err := v.Lookup("a/f")
	if err != nil || r.Type != TypeFile || r.Content != "data" {
		t.Fatalf("a/f should be upper file, got %+v %v", r, err)
	}
	if _, err := v.Lookup("a/f/x"); !errors.Is(err, ErrNotDirectory) {
		t.Fatalf("path under upper file must fail with not-a-directory: %v", err)
	}
}

func TestWriteDeepFillsPlainAncestors(t *testing.T) {
	v := mustNew(t, map[string]string{"p/": "", "p/q/": ""})
	if err := v.Write("p/q/r", "z"); err != nil {
		t.Fatal(err)
	}
	up := v.Upper()
	if up["p"].Kind != KindDir || up["p/q"].Kind != KindDir ||
		up["p/q/r"].Kind != KindFile {
		t.Fatalf("ancestors not filled as plain dirs: %v", up)
	}
	if err := v.Write("zz/yy", "1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing ancestor must be ErrNotFound, got %v", err)
	}
}

func TestRemoveDirDiscardsSubtree(t *testing.T) {
	// White out both lower children so merged x is empty; removing x must
	// discard those child whiteout records and leave a single whiteout.
	v := mustNew(t, map[string]string{"x/": "", "x/lo": "lo"})
	if err := v.Remove("x/lo"); err != nil {
		t.Fatal(err)
	}
	if err := v.Write("x/up", "u"); err != nil {
		t.Fatal(err)
	}
	if err := v.Remove("x/up"); err != nil { // upper-only: record dropped
		t.Fatal(err)
	}
	if err := v.Remove("x"); err != nil {
		t.Fatal(err)
	}
	up := v.Upper()
	if up["x"].Kind != KindWhiteout {
		t.Fatalf("x must be whiteout, got %v", up["x"])
	}
	if _, ok := up["x/lo"]; ok {
		t.Fatalf("x/lo whiteout must be discarded with subtree: %v", up)
	}
}

func TestUpperFileMasksLowerDir(t *testing.T) {
	v := mustNew(t, map[string]string{"d/": "", "d/f/": "", "d/f/q": "lo"})
	if err := v.Remove("d/f/q"); err != nil {
		t.Fatal(err)
	}
	if err := v.Remove("d/f"); err != nil {
		t.Fatal(err)
	}
	if err := v.Write("d/f", "up"); err != nil {
		t.Fatal(err)
	}
	r, err := v.Lookup("d/f")
	if err != nil || r.Type != TypeFile || r.Content != "up" {
		t.Fatalf("upper file should win: %+v %v", r, err)
	}
	if _, err := v.Lookup("d/f/q"); !errors.Is(err, ErrNotDirectory) {
		t.Fatalf("path under upper file must fail not-a-directory: %v", err)
	}
}

func TestRenameUpperDirMasksLowerFile(t *testing.T) {
	v := mustNew(t, map[string]string{"g": "lofile"})
	if err := v.Remove("g"); err != nil {
		t.Fatal(err)
	}
	if err := v.Mkdir("g"); err != nil {
		t.Fatal(err)
	}
	k, err := lookupKind(v, "g")
	if err != nil || k != TypeDirectory {
		t.Fatalf("dir should mask lower file after remove+mkdir: %v %v", k, err)
	}
	if v.Upper()["g"].Kind != KindOpaque {
		t.Fatalf("mkdir on whiteout should be opaque: %v", v.Upper()["g"])
	}
	if err := v.Write("g/h", "x"); err != nil {
		t.Fatalf("write inside opaque dir should work: %v", err)
	}
	// A pure upper directory renamed directly onto an existing lower file
	// conflicts with "not a directory".
	w := mustNew(t, map[string]string{"g": "lofile"})
	if err := w.Mkdir("x"); err != nil {
		t.Fatal(err)
	}
	if err := w.Rename("x", "g"); !errors.Is(err, ErrNotDirectory) {
		t.Fatalf("dir over file must be ErrNotDirectory, got %v", err)
	}
}

func TestReadDirUnionOrdering(t *testing.T) {
	v := mustNew(t, map[string]string{
		"a/": "", "a/low1": "x", "a/z/": "", "a/z/q": "q",
		"b/": "", "b/g": "g",
		"c": "c",
	})
	if err := v.Write("a/up", "u"); err != nil {
		t.Fatal(err)
	}
	if err := v.Remove("a/low1"); err != nil {
		t.Fatal(err)
	}
	got, err := v.ReadDir("")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"a", "b", "c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("root = %v want %v", got, want)
	}
	got, err = v.ReadDir("a")
	if err != nil {
		t.Fatal(err)
	}
	want = []string{"up", "z"} // low1 whiteout-hidden; up sorts before z
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("a = %v want %v", got, want)
	}
	// Every listed name resolves; unlisted does not.
	for _, n := range got {
		if _, err := v.Lookup("a/" + n); err != nil {
			t.Fatalf("listed %s not lookupable: %v", n, err)
		}
	}
	if _, err := v.Lookup("a/low1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unlisted low1 lookupable: %v", err)
	}
}

func TestRejectionDoesNotMutateUpper(t *testing.T) {
	v := mustNew(t, map[string]string{"a/": "", "a/f": "x", "c": "c"})
	try := func(fn func() error) {
		t.Helper()
		before := sortedUpper(v)
		err := fn()
		if err == nil {
			t.Fatalf("expected rejection")
		}
		after := sortedUpper(v)
		if !reflect.DeepEqual(before, after) {
			t.Fatalf("upper changed by rejected op: %v -> %v", before, after)
		}
	}
	try(func() error { _, err := v.Lookup("../x"); return err })
	try(func() error { return v.Mkdir("../x") })      // invalid path
	try(func() error { return v.Mkdir("a/f") })       // existing file
	try(func() error { return v.Mkdir("a/f/n") })     // file ancestor
	try(func() error { return v.Mkdir("missing/x") }) // missing ancestor
	try(func() error { return v.Write("a", "x") })    // dir
	try(func() error { return v.Remove("nope") })     // not found
	try(func() error { return v.Rename("a", "a/f") }) // into self (a/f under a)
	try(func() error { return v.Rename("a", "z") })   // cross-layer dir
	try(func() error { return v.Rename("c", "a") })   // file -> non-empty dir? a has f
}

func sortedUpper(v *View) []string {
	var keys []string
	for k := range v.Upper() {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func TestSpecExample(t *testing.T) {
	v := mustNew(t, map[string]string{
		"a/": "", "a/f": "f",
		"b/": "", "b/g": "g",
		"c": "c",
	})
	if err := v.Mkdir("x"); err != nil {
		t.Fatal(err)
	}
	if err := v.Write("x/y", "1"); err != nil {
		t.Fatal(err)
	}
	if err := v.Remove("b/g"); err != nil {
		t.Fatal(err)
	}
	if err := v.Rename("x", "b"); err != nil {
		t.Fatal(err)
	}
	up := v.Upper()
	if up["b"].Kind != KindOpaque || up["b/y"].Kind != KindFile {
		t.Fatalf("b should be opaque with file y: %v", up)
	}
	if _, ok := up["x"]; ok {
		t.Fatalf("x must disappear: %v", up)
	}
	root, _ := v.ReadDir("")
	if !reflect.DeepEqual(root, []string{"a", "b", "c"}) {
		t.Fatalf("root = %v", root)
	}
	b, _ := v.ReadDir("b")
	if !reflect.DeepEqual(b, []string{"y"}) {
		t.Fatalf("b = %v, g must not resurrect", b)
	}
	if err := v.Rename("a", "z"); !errors.Is(err, ErrCrossLayer) {
		t.Fatalf("expect cross-layer, got %v", err)
	}
	if err := v.Rename("c", "d"); err != nil {
		t.Fatal(err)
	}
	if v.Upper()["c"].Kind != KindWhiteout || v.Upper()["d"].Kind != KindFile {
		t.Fatalf("c whiteout and d file expected: %v", v.Upper())
	}
}

func TestWhiteoutNeverAppearsInReadDir(t *testing.T) {
	v := mustNew(t, map[string]string{"a/": "", "a/f": "f", "a/g": "g"})
	if err := v.Remove("a/f"); err != nil {
		t.Fatal(err)
	}
	names, err := v.ReadDir("a")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(names, []string{"g"}) {
		t.Fatalf("whiteout must hide name entirely: %v", names)
	}
	if _, err := v.Lookup("a/f"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("whiteout lookup must be not found: %v", err)
	}
}

func TestUpperReturnsAscendingSorted(t *testing.T) {
	v := mustNew(t, map[string]string{})
	for _, p := range []string{"a", "a/b"} {
		if err := v.Mkdir(p); err != nil {
			t.Fatal(err)
		}
	}
	if err := v.Mkdir("a/b/c"); err != nil {
		t.Fatal(err)
	}
	if err := v.Write("z", "x"); err != nil {
		t.Fatal(err)
	}
	if err := v.Write("m", "x"); err != nil {
		t.Fatal(err)
	}
	// Upper returns a map; iterate deterministically via sorted keys.
	keys := make([]string, 0)
	for k := range v.Upper() {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	want := []string{"a", "a/b", "a/b/c", "m", "z"}
	if !reflect.DeepEqual(keys, want) {
		t.Fatalf("upper keys = %v want %v", keys, want)
	}
}
