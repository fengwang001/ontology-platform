package ontology

import (
	"errors"
	"testing"
)

// Renaming a pure upper directory onto a lower directory path makes the
// destination opaque; the lower contents do not appear.
func TestRenameUpperDirOverLowerDirBecomesOpaque(t *testing.T) {
	v := mustNew(t, map[string]string{"b/": "", "b/g": "g", "b/h": "h"})
	if err := v.Mkdir("x"); err != nil {
		t.Fatal(err)
	}
	if err := v.Write("x/y", "1"); err != nil {
		t.Fatal(err)
	}
	// b is non-empty, so empty it first.
	if err := v.Remove("b/g"); err != nil {
		t.Fatal(err)
	}
	if err := v.Remove("b/h"); err != nil {
		t.Fatal(err)
	}
	if err := v.Rename("x", "b"); err != nil {
		t.Fatal(err)
	}
	if v.Upper()["b"].Kind != KindOpaque {
		t.Fatalf("b must be opaque, got %v", v.Upper()["b"])
	}
	names, err := v.ReadDir("b")
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || names[0] != "y" {
		t.Fatalf("b should contain only y, got %v", names)
	}
}

// Renaming an opaque directory leaves a whiteout at the source.
func TestRenameOpaqueLeavesWhiteout(t *testing.T) {
	v := mustNew(t, map[string]string{"b/": "", "b/g": "g"})
	if err := v.Remove("b/g"); err != nil {
		t.Fatal(err)
	}
	if err := v.Remove("b"); err != nil {
		t.Fatal(err)
	}
	if err := v.Mkdir("b"); err != nil { // opaque
		t.Fatal(err)
	}
	if err := v.Write("b/k", "k"); err != nil {
		t.Fatal(err)
	}
	if err := v.Rename("b", "z"); err != nil {
		t.Fatal(err)
	}
	if v.Upper()["b"].Kind != KindWhiteout {
		t.Fatalf("source must be whiteout, got %v", v.Upper()["b"])
	}
	if v.Upper()["z"].Kind != KindOpaque {
		t.Fatalf("opaqueness must carry to destination, got %v", v.Upper()["z"])
	}
	if v.Upper()["z/k"].Kind != KindFile {
		t.Fatalf("subtree moves with prefix: %v", v.Upper())
	}
	if _, err := v.Lookup("b/g"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("lower content at whiteout source stays hidden: %v", err)
	}
}

// Renaming at a location unreachable from the lower layer writes no
// whiteout at the source.
func TestRenameUnreachableNoWhiteout(t *testing.T) {
	v := mustNew(t, map[string]string{"o/": "", "o/d/": "", "o/d/f": "f"})
	if err := v.Remove("o/d/f"); err != nil {
		t.Fatal(err)
	}
	if err := v.Remove("o/d"); err != nil {
		t.Fatal(err)
	}
	if err := v.Mkdir("o/d"); err != nil { // opaque
		t.Fatal(err)
	}
	// A child purely in the upper layer under the opaque ancestor.
	if err := v.Mkdir("o/d/p"); err != nil {
		t.Fatal(err)
	}
	if err := v.Write("o/d/p/q", "z"); err != nil {
		t.Fatal(err)
	}
	if err := v.Rename("o/d/p", "o/d/r"); err != nil {
		t.Fatal(err)
	}
	up := v.Upper()
	if _, ok := up["o/d/p"]; ok {
		t.Fatalf("source record must be gone: %v", up)
	}
	if up["o/d/r"].Kind != KindDir || up["o/d/r/q"].Content != "z" {
		t.Fatalf("subtree moved incorrectly: %v", up)
	}
}

// Renaming onto a whiteout produces an opaque directory when the moved
// node is a directory.
func TestRenameDirOntoWhiteoutBecomesOpaque(t *testing.T) {
	v := mustNew(t, map[string]string{"w/": "", "w/lo": "lo"})
	if err := v.Remove("w/lo"); err != nil {
		t.Fatal(err)
	}
	if err := v.Remove("w"); err != nil {
		t.Fatal(err)
	}
	if err := v.Mkdir("p"); err != nil {
		t.Fatal(err)
	}
	if err := v.Rename("p", "w"); err != nil {
		t.Fatal(err)
	}
	if v.Upper()["w"].Kind != KindOpaque {
		t.Fatalf("rename onto whiteout dir path must be opaque: %v", v.Upper()["w"])
	}
}

func TestRenameDirConflicts(t *testing.T) {
	// Destination non-empty directory rejects; empty directory replaces.
	v := mustNew(t, map[string]string{})
	if err := v.Mkdir("src"); err != nil {
		t.Fatal(err)
	}
	if err := v.Mkdir("dst"); err != nil {
		t.Fatal(err)
	}
	if err := v.Write("dst/child", "c"); err != nil {
		t.Fatal(err)
	}
	if err := v.Rename("src", "dst"); !errors.Is(err, ErrDirNotEmpty) {
		t.Fatalf("non-empty dst must reject, got %v", err)
	}
	if err := v.Remove("dst/child"); err != nil {
		t.Fatal(err)
	}
	if err := v.Rename("src", "dst"); err != nil {
		t.Fatalf("empty dst directory must be replaced: %v", err)
	}
	if _, ok := v.Upper()["src"]; ok {
		t.Fatalf("src must disappear: %v", v.Upper())
	}
	if v.Upper()["dst"].Kind != KindDir {
		t.Fatalf("dst must be directory: %v", v.Upper())
	}

	// Into-self variants.
	w := mustNew(t, map[string]string{"d/": ""})
	if err := w.Mkdir("d/u"); err != nil {
		t.Fatal(err)
	}
	if err := w.Rename("d/u", "d/u"); !errors.Is(err, ErrIntoSelf) {
		t.Fatalf("rename onto self: %v", err)
	}
	if err := w.Rename("d/u", "d/u/v"); !errors.Is(err, ErrIntoSelf) {
		t.Fatalf("rename into subtree: %v", err)
	}

	// File vs directory conflicts.
	if err := w.Write("ff", "1"); err != nil {
		t.Fatal(err)
	}
	if err := w.Rename("ff", "d"); !errors.Is(err, ErrIsDirectory) {
		t.Fatalf("file into dir must be ErrIsDirectory, got %v", err)
	}
	if err := w.Rename("d/u", "ff"); !errors.Is(err, ErrNotDirectory) {
		t.Fatalf("dir onto file must be ErrNotDirectory, got %v", err)
	}
}

func TestRenameFileReplacesFileAndWhiteoutsLower(t *testing.T) {
	v := mustNew(t, map[string]string{"a/": "", "a/f": "lo", "g": "G"})
	if err := v.Write("h", "up"); err != nil {
		t.Fatal(err)
	}
	// Upper-only file -> lower file path: write replaces, remove drops
	// source, destination hides lower via the new upper file.
	if err := v.Rename("h", "g"); err != nil {
		t.Fatal(err)
	}
	if v.Upper()["g"].Kind != KindFile || v.Upper()["g"].Content != "up" {
		t.Fatalf("g should hold moved file: %v", v.Upper())
	}
	if _, ok := v.Upper()["h"]; ok {
		t.Fatalf("source h dropped")
	}
	// Lower file moved elsewhere: content follows, source whiteout.
	if err := v.Rename("a/f", "a/f2"); err != nil {
		t.Fatal(err)
	}
	if v.Upper()["a/f"].Kind != KindWhiteout {
		t.Fatalf("lower file source must be whiteout: %v", v.Upper())
	}
	if r, err := v.Lookup("a/f2"); err != nil || r.Content != "lo" {
		t.Fatalf("content should be copied from lower file: %+v %v", r, err)
	}
}

func TestRenameErrorOrdering(t *testing.T) {
	v := mustNew(t, map[string]string{"a/": "", "a/f": "x", "c": "c"})
	// invalid old beats missing new
	if err := v.Rename("../bad", "nope/x"); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("got %v", err)
	}
	// old missing beats bad new ancestors
	if err := v.Rename("nope", "a/f/x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v", err)
	}
	// old found, new ancestor missing
	if err := v.Rename("c", "missing/x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v", err)
	}
	// new ancestor a file -> not directory
	if err := v.Rename("c", "a/f/x"); !errors.Is(err, ErrNotDirectory) {
		t.Fatalf("got %v", err)
	}
}
