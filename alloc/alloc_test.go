package alloc

import (
	"errors"
	"fmt"
	"testing"

	"ontology/align"
)

func TestAllocAlignment(t *testing.T) {
	cases := []struct {
		size, al int
	}{{1, 1}, {10, 16}, {4, 8}, {8, 8}, {1, 8}, {16, 16}, {33, 32}, {100, 64}}
	for _, c := range cases {
		f := New()
		p, err := f.Alloc(c.size, c.al)
		if err != nil {
			t.Fatalf("Alloc(%d,%d): %v", c.size, c.al, err)
		}
		if p%c.al != 0 {
			t.Fatalf("Alloc(%d,%d)=%d not aligned to %d", c.size, c.al, p, c.al)
		}
	}
}

func TestEightStepScript(t *testing.T) {
	f := New()
	for i, s := range script {
		if s[0] == 1 {
			p, err := f.Alloc(s[1], s[2])
			if err != nil || p != s[3] {
				t.Fatalf("step %d: ptr=%d want %d err=%v", i+1, p, s[3], err)
			}
		} else {
			b, err := f.Free(s[3])
			if err != nil || b != s[4] {
				t.Fatalf("step %d: base=%d want %d err=%v", i+1, b, s[4], err)
			}
		}
		if f.next != s[5] {
			t.Fatalf("step %d: next=%d want %d", i+1, f.next, s[5])
		}
	}
}

func TestFreeReadsHeaderBase(t *testing.T) {
	cases := []struct {
		size, al int
		wantBase int
	}{{10, 16, 0}, {4, 8, 33}, {8, 8, 52}}
	f := New()
	ptrs := make([]int, len(cases))
	for i, c := range cases {
		p, err := f.Alloc(c.size, c.al)
		if err != nil {
			t.Fatal(err)
		}
		ptrs[i] = p
	}
	for i, c := range cases {
		b, err := f.Free(ptrs[i])
		if err != nil {
			t.Fatalf("Free(%d): %v", ptrs[i], err)
		}
		if b != c.wantBase {
			t.Fatalf("Free(%d) read base=%d want %d", ptrs[i], b, c.wantBase)
		}
		if hb := f.headers[align.HeaderOffset(ptrs[i])]; hb != c.wantBase {
			t.Fatalf("header bytes for %d hold %d want %d", ptrs[i], hb, c.wantBase)
		}
	}
}

func TestFreeProbeCountConstant(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		f := New()
		for i := 0; i < m; i++ {
			if _, err := f.Alloc(1, 8); err != nil {
				t.Fatal(err)
			}
			if f.probes != 0 {
				t.Fatalf("m=%d: Alloc probed %d records", m, f.probes)
			}
		}
		before := f.Len()
		if _, err := f.Free(8); err != nil {
			t.Fatal(err)
		}
		if f.probes > 1 {
			t.Fatalf("m=%d: Free probed %d records, bound is 1", m, f.probes)
		}
		if f.Len() != before-1 {
			t.Fatalf("m=%d: Len=%d want %d", m, f.Len(), before-1)
		}
	}
}

func TestRejectionsAreDistinctAndTraceless(t *testing.T) {
	f := New()
	p, err := f.Alloc(10, 16)
	if err != nil {
		t.Fatal(err)
	}
	sx, sl := f.next, f.Len()
	cases := []struct {
		name string
		run  func() error
		want error
	}{
		{"bad size", func() error { _, e := f.Alloc(0, 8); return e }, align.ErrBadSize},
		{"bad align", func() error { _, e := f.Alloc(4, 6); return e }, align.ErrBadAlign},
		{"never allocated", func() error { _, e := f.Free(999); return e }, ErrInvalidFree},
		{"double free", func() error {
			if _, e := f.Free(p); e != nil {
				return fmt.Errorf("setup: %w", e)
			}
			_, e := f.Free(p)
			return e
		}, ErrInvalidFree},
	}
	for _, c := range cases {
		err := c.run()
		if !errors.Is(err, c.want) {
			t.Fatalf("%s: err=%v want %v", c.name, err, c.want)
		}
		// double-free case legitimately removed one record on its first Free.
		if c.name != "double free" && (f.next != sx || f.Len() != sl) {
			t.Fatalf("%s mutated state", c.name)
		}
	}
	if _, err := f.Alloc(1, 8); err != nil {
		t.Fatalf("allocator unusable after rejections: %v", err)
	}
}

func TestSelfCheckAndComplexity(t *testing.T) {
	var a *Allocator
	if err := a.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
	if err := CheckProbeComplexity(); err != nil {
		t.Fatalf("CheckProbeComplexity: %v", err)
	}
}
