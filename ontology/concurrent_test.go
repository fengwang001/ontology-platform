package ontology

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

func TestConcurrentAccess(t *testing.T) {
	v := mustNew(t, map[string]string{
		"a/": "", "a/f": "lo", "a/g/": "", "a/g/x": "x",
		"b/": "", "b/g": "g", "c": "c",
	})
	var wg sync.WaitGroup
	names := []string{"a", "b", "c", "x", "y", "a/f", "a/g", "b/g", "nope"}
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 300; i++ {
				p := names[(i+id)%len(names)]
				_, _ = v.Lookup(p)
				_, _ = v.ReadDir(p)
				_ = v.Mkdir(fmt.Sprintf("w%d/d%d", id, i%4))
				_ = v.Write(fmt.Sprintf("w%d/f%d", id, i%4), "z")
				_ = v.Remove(fmt.Sprintf("w%d/f%d", id, i%4))
				_ = v.Rename(fmt.Sprintf("w%d/d%d", id, i%4), fmt.Sprintf("w%d/e%d", id, i%4))
				_ = v.Upper()
			}
		}(w)
	}
	wg.Wait()

	// Final upper layer satisfies structural invariants.
	up := v.Upper()
	for p := range up {
		for _, a := range ancestors(p) {
			if a == "" {
				continue
			}
			r, ok := up[a]
			if !ok {
				t.Fatalf("record %q lacks upper ancestor %q: %v", p, a, up)
			}
			if r.Kind != KindDir && r.Kind != KindOpaque {
				t.Fatalf("ancestor %q of %q is %s", a, p, r.Kind)
			}
		}
	}
	// Every ReadDir entry resolves and vice versa.
	dirs := []string{"", "a", "b"}
	for _, d := range dirs {
		names, err := v.ReadDir(d)
		if err != nil {
			t.Fatal(err)
		}
		prefix := d
		if d != "" {
			prefix += "/"
		}
		for _, n := range names {
			if _, err := v.Lookup(prefix + n); err != nil {
				t.Fatalf("listed %s: %v", prefix+n, err)
			}
		}
	}
}

func TestRejectedOpsLeaveNoTraceConcurrently(t *testing.T) {
	v := mustNew(t, map[string]string{"a/": "", "a/f": "x"})
	before := v.Upper()
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				if err := v.Mkdir("a/f"); !errors.Is(err, ErrExist) {
					t.Errorf("mkdir a/f: %v", err)
				}
				if err := v.Rename("a", "z"); !errors.Is(err, ErrCrossLayer) {
					t.Errorf("rename a: %v", err)
				}
			}
		}()
	}
	wg.Wait()
	if len(v.Upper()) != len(before) {
		t.Fatalf("rejected ops changed upper: %v", v.Upper())
	}
}
