package ontology

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

var collisionNames = [...]string{"q7bv8", "d48mk", "xg0av"}

func TestConstructorAndCollisionFixture(t *testing.T) {
	for _, limit := range []int{0, -1, 1 << 31} {
		_, err := NewHashDirectory(0, limit)
		if !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("limit=%d got %v", limit, err)
		}
	}
	h := hashName(0, collisionNames[0])
	for _, name := range collisionNames[1:] {
		if got := hashName(0, name); got != h {
			t.Fatalf("%s hash=%d want %d", name, got, h)
		}
	}
}

func TestMinorReuseAndHashLimit(t *testing.T) {
	d := mustDirectory(t, 0, 2)
	order := []string{collisionNames[2], collisionNames[0], collisionNames[1]}
	for idx, name := range order[:2] {
		key, err := d.Add(name, uint64(idx+1))
		if err != nil {
			t.Fatal(err)
		}
		if minor := key - entryKey(hashName(0, name), 0); minor != Key(idx) {
			t.Fatalf("%s minor=%d want %d", name, minor, idx)
		}
	}
	_, err := d.Add(order[2], 3)
	if !errors.Is(err, ErrHashFull) {
		t.Fatalf("M+1 got %v", err)
	}
	if err := d.Remove(order[0]); err != nil {
		t.Fatal(err)
	}
	key, err := d.Add("reuse", 4)
	if err != nil {
		t.Fatal(err)
	}
	if minor := key - entryKey(hashName(0, "reuse"), 0); minor != 0 {
		t.Fatalf("reused minor=%d want 0", minor)
	}
}

func TestRenameSameHashAndForeignFull(t *testing.T) {
	d := mustDirectory(t, 0, 2)
	p, q, r := collisionNames[0], collisionNames[1], collisionNames[2]
	mustAdd(t, d, p, 1)
	mustAdd(t, d, q, 2)
	key, err := d.Rename(p, r)
	if err != nil {
		t.Fatal(err)
	}
	if minor := key - entryKey(hashName(0, r), 0); minor != 0 {
		t.Fatalf("rename minor=%d want 0", minor)
	}
	if _, err := d.CookieOf(p); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old name remains, got %v", err)
	}

	other := mustDirectory(t, 0, 2)
	mustAdd(t, other, q, 2)
	mustAdd(t, other, r, 3)
	mustAdd(t, other, "source", 1)
	_, err = other.Rename("source", collisionNames[0])
	if !errors.Is(err, ErrHashFull) {
		t.Fatalf("foreign full rename got %v", err)
	}
	if _, err := other.CookieOf("source"); err != nil {
		t.Fatalf("failed rename changed state: %v", err)
	}

	cases := [][2]string{{"", q}, {p, ""}, {"missing", q}, {q, q}, {q, r}}
	want := []error{ErrInvalidArgument, ErrInvalidArgument, ErrNotFound, ErrExists, ErrExists}
	for idx, tc := range cases {
		if _, err := d.Rename(tc[0], tc[1]); !errors.Is(err, want[idx]) {
			t.Fatalf("Rename(%q,%q) got %v want %v", tc[0], tc[1], err, want[idx])
		}
	}
}

func TestConcurrentOperations(t *testing.T) {
	d := mustDirectory(t, 0, 4096)
	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				name := fmt.Sprintf("w%d-n%d", worker, i)
				_, _ = d.Add(name, uint64(worker*1000+i))
				_, _, _, _ = d.ReadDir(Cookie{}, 8)
				_ = d.Remove(name)
			}
		}(worker)
	}
	wg.Wait()
}

func TestReadDirPagingAndInsertVisibility(t *testing.T) {
	d := mustDirectory(t, 0, 8)
	p, q, r := collisionNames[0], collisionNames[1], collisionNames[2]
	mustAdd(t, d, p, 1)
	mustAdd(t, d, q, 2)
	mustAdd(t, d, r, 3)

	entries, c, done, err := d.ReadDir(Cookie{Gen: 1}, 1)
	if err != nil || done || len(entries) != 1 || entries[0].Name != p {
		t.Fatalf("first page entries=%v done=%v err=%v", entries, done, err)
	}
	if c.Pos != entries[0].Key+1 {
		t.Fatalf("cursor=%+v key=%d", c, entries[0].Key)
	}
	entries, c, done, err = d.ReadDir(c, 1)
	if err != nil || done || len(entries) != 1 || entries[0].Name != q {
		t.Fatalf("second page entries=%v done=%v err=%v", entries, done, err)
	}
	mustAdd(t, d, "abb", 10)
	mustAdd(t, d, "0-before-cursor", 11)
	entries, _, done, err = d.ReadDir(c, 10)
	if err != nil {
		t.Fatal(err)
	}
	got := entryNames(entries)
	if !slicesEqual(got, []string{"xg0av", "0-before-cursor"}) {
		t.Fatalf("visibility got=%v", got)
	}

	qKey := keyOf(t, d, q)
	entries, _, _, err = d.ReadDir(Cookie{Gen: 1, Pos: qKey}, 1)
	if err != nil || len(entries) != 1 || entries[0].Name != q {
		t.Fatalf("Pos at key entries=%v err=%v", entries, err)
	}

	empty, zeroCursor, _, err := d.ReadDir(Cookie{Gen: 99}, 0)
	if err != nil || len(empty) != 0 || zeroCursor.Gen != 1 || zeroCursor.Pos != 0 {
		t.Fatalf("zero cursor empty=%v cursor=%+v err=%v", empty, zeroCursor, err)
	}
	if _, _, _, err := d.ReadDir(Cookie{Gen: 1}, -1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("negative n got %v", err)
	}
	if _, _, _, err := d.ReadDir(Cookie{Gen: 2, Pos: 1}, 0); !errors.Is(err, ErrCursorExpired) {
		t.Fatalf("stale n=0 got %v", err)
	}
}

func TestRehashMigrationAndAtomicReject(t *testing.T) {
	d := mustDirectory(t, 0, 8)
	p, q, r := collisionNames[0], collisionNames[1], collisionNames[2]
	mustAdd(t, d, p, 1)
	mustAdd(t, d, q, 2)
	mustAdd(t, d, r, 3)
	_, oldAfterP, _, err := d.ReadDir(Cookie{Gen: 1}, 1)
	if err != nil {
		t.Fatal(err)
	}

	if err := d.Rehash(0); err != nil {
		t.Fatal(err)
	}
	names := readAll(t, d, Cookie{Gen: 2})
	if !slicesEqual(names, []string{q, p, r}) {
		t.Fatalf("same-seed rehash names=%v", names)
	}
	for idx, name := range []string{q, p, r} {
		if minor := keyOf(t, d, name) - entryKey(hashName(0, name), 0); minor != Key(idx) {
			t.Fatalf("%s minor=%d want %d", name, minor, idx)
		}
	}
	empty, migrated, done, err := d.ReadDir(oldAfterP, 0)
	if err != nil || len(empty) != 0 || done {
		t.Fatalf("migrate n=0 empty=%v done=%v err=%v", empty, done, err)
	}
	wantCursor := Cookie{Gen: 2, Pos: keyOf(t, d, p) + 1}
	if migrated != wantCursor {
		t.Fatalf("migrated=%+v want=%+v", migrated, wantCursor)
	}

	if err := d.Remove(p); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := d.ReadDir(oldAfterP, 10); !errors.Is(err, ErrCursorExpired) {
		t.Fatalf("deleted migration got %v", err)
	}
	bad := Cookie{Gen: 1, Pos: 0xffffffff00000001}
	if _, _, _, err := d.ReadDir(bad, 10); !errors.Is(err, ErrCursorExpired) {
		t.Fatalf("bad snapshot key got %v", err)
	}
	if err := d.Rehash(1); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := d.ReadDir(oldAfterP, 10); !errors.Is(err, ErrCursorExpired) {
		t.Fatalf("two rehashes got %v", err)
	}

	full := mustDirectory(t, 1, 1)
	mustAdd(t, full, p, 1)
	mustAdd(t, full, q, 2)
	if err := full.Rehash(0); !errors.Is(err, ErrHashFull) {
		t.Fatalf("overfull rehash got %v", err)
	}
	_, c, _, err := full.ReadDir(Cookie{}, 10)
	if err != nil || c.Gen != 1 {
		t.Fatalf("failed rehash changed gen: c=%+v err=%v", c, err)
	}
	if got := readAll(t, full, Cookie{Gen: 1}); !slicesEqual(got, []string{p, q}) {
		t.Fatalf("failed rehash changed state: %v", got)
	}
}

func TestCookieOfAndEmptyDone(t *testing.T) {
	d := mustDirectory(t, 0, 4)
	mustAdd(t, d, collisionNames[0], 1)
	c, err := d.CookieOf(collisionNames[0])
	if err != nil {
		t.Fatal(err)
	}
	entries, _, done, err := d.ReadDir(c, 10)
	if err != nil || len(entries) != 0 || !done {
		t.Fatalf("CookieOf continuation entries=%v done=%v err=%v", entries, done, err)
	}
	_, _, done, err = d.ReadDir(Cookie{}, 0)
	if err != nil || done {
		t.Fatalf("n=0 at start done=%v err=%v", done, err)
	}
	if _, err := d.CookieOf(""); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("empty CookieOf got %v", err)
	}
	if _, err := d.CookieOf("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing CookieOf got %v", err)
	}
}

func mustDirectory(t *testing.T, seed uint32, limit int) *HashDirectory {
	t.Helper()
	d, err := NewHashDirectory(seed, limit)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func mustAdd(t *testing.T, d *HashDirectory, name string, ino uint64) Key {
	t.Helper()
	key, err := d.Add(name, ino)
	if err != nil {
		t.Fatalf("Add(%s): %v", name, err)
	}
	return key
}

func keyOf(t *testing.T, d *HashDirectory, name string) Key {
	t.Helper()
	c, err := d.CookieOf(name)
	if err != nil {
		t.Fatal(err)
	}
	return c.Pos - 1
}

func readAll(t *testing.T, d *HashDirectory, c Cookie) []string {
	t.Helper()
	entries, _, _, err := d.ReadDir(c, 1000)
	if err != nil {
		t.Fatal(err)
	}
	return entryNames(entries)
}

func entryNames(entries []DirEntry) []string {
	names := make([]string, len(entries))
	for i, entry := range entries {
		names[i] = entry.Name
	}
	return names
}

func slicesEqual[T comparable](got, want []T) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
