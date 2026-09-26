package stream

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"ontology/enc"
)

func TestRejectNoAdvance(t *testing.T) {
	cases := []struct {
		name string
		buf  []byte
		kind func(error) bool
	}{
		{"empty", nil, func(e error) bool { return errors.Is(e, enc.ErrEmpty) }},
		{"overflow",
			[]byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80},
			func(e error) bool { return errors.Is(e, enc.ErrOverflow) }},
		{"noncanonical", []byte{0x80, 0x00, 0x05},
			func(e error) bool { return errors.Is(e, enc.ErrNonCanonical) }},
	}
	for _, c := range cases {
		r := NewReader(c.buf)
		if _, err := r.ReadUint(); !c.kind(err) {
			t.Fatalf("%s: got %v", c.name, err)
		}
		if r.Pos() != 0 || r.lastExtra != 0 {
			t.Fatalf("%s: cursor moved to %d (extra %d)", c.name, r.Pos(), r.lastExtra)
		}
		// The reader stays usable: after Reset to good input, reads work.
		r.Reset([]byte{0x2a})
		if v, err := r.ReadUint(); err != nil || v != 42 || r.Pos() != 1 {
			t.Fatalf("%s: reader unusable after reject: %d %v", c.name, v, err)
		}
	}
	// A bad varint after a good one does not move the cursor past itself.
	r := NewReader([]byte{0x05, 0x80, 0x00, 0x07})
	if v, err := r.ReadUint(); err != nil || v != 5 {
		t.Fatalf("lead value: %d %v", v, err)
	}
	if _, err := r.ReadUint(); !errors.Is(err, enc.ErrNonCanonical) || r.Pos() != 1 {
		t.Fatalf("bad tail: err %v pos %d", err, r.Pos())
	}
}

func TestSinglePassLinear(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		var buf []byte
		for i := 0; i < m; i++ {
			v := uint64(i)*131 + 17 // distinct, many become multi-byte
			buf = append(buf, enc.EncodeUint(v)...)
		}
		r := NewReader(buf)
		total := 0
		prev := 0
		for i := 0; i < m; i++ {
			v, err := r.ReadUint()
			if err != nil {
				t.Fatalf("m=%d i=%d: %v", m, i, err)
			}
			if want := uint64(i)*131 + 17; v != want {
				t.Fatalf("m=%d i=%d: got %d want %d", m, i, v, want)
			}
			total += r.Pos() - prev
			prev = r.Pos()
			if r.lastExtra != 0 {
				t.Fatalf("m=%d i=%d: extra prescan %d", m, i, r.lastExtra)
			}
		}
		if total != len(buf) || r.Pos() != len(buf) || r.Len() != len(buf) {
			t.Fatalf("m=%d: checked %d pos %d len %d", m, total, r.Pos(), len(buf))
		}
	}
}

func TestResetAndSigned(t *testing.T) {
	r := NewReader(enc.EncodeInt(-64))
	v, err := r.ReadInt()
	if err != nil || v != -64 {
		t.Fatalf("signed read: %d %v", v, err)
	}
	r.Reset(enc.EncodeUint(300))
	if u, err := r.ReadUint(); err != nil || u != 300 || r.Pos() != 2 {
		t.Fatalf("after reset: %d %v pos %d", u, err, r.Pos())
	}
}

func TestConcurrentWriter(t *testing.T) {
	for _, n := range []int{8, 64, 256} {
		w := NewWriter()
		var wg sync.WaitGroup
		want := make(map[uint64]int)
		var wantLen int
		for i := 0; i < n; i++ {
			v := uint64(i)*1_000_003 + 999999 // distinct multi-byte values
			want[v]++
			wantLen += len(enc.EncodeUint(v))
		}
		for i := 0; i < n; i++ {
			v := uint64(i)*1_000_003 + 999999
			wg.Add(1)
			go func(x uint64) {
				defer wg.Done()
				w.WriteUint(x)
			}(v)
		}
		wg.Wait()
		buf := w.Bytes()
		if len(buf) != wantLen {
			t.Fatalf("n=%d: len %d want %d", n, len(buf), wantLen)
		}
		r := NewReader(buf)
		got := make(map[uint64]int)
		count := 0
		for r.Pos() < r.Len() {
			v, err := r.ReadUint()
			if err != nil {
				t.Fatalf("n=%d: decode interrupted at %d: %v; buf %x", n, r.Pos(), err, buf)
			}
			got[v]++
			count++
		}
		if count != n || fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("n=%d: decoded %d values, multiset match=%v", n, count, got)
		}
	}
}
