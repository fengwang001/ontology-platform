package stream

import (
	"errors"
	"sync"
	"testing"

	"ontology/enc"
)

// A rejected read must not advance the cursor, must fail with the
// distinct sentinel for its cause, and the reader must stay usable.
func TestFailureNoAdvance(t *testing.T) {
	cases := []struct {
		name string
		tail []byte
		want error
	}{
		{"empty", nil, enc.ErrEmptyInput},
		{"overflow", []byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80}, enc.ErrOverflow},
		{"non-canonical", []byte{0x80, 0x00}, enc.ErrNonCanonical},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := NewReader(append([]byte{0x2a}, tc.tail...))
			if v, err := r.ReadUint(); err != nil || v != 42 {
				t.Fatalf("setup read: v=%d err=%v", v, err)
			}
			pos := r.Pos()
			for i := 0; i < 3; i++ { // repeated rejections never advance
				if _, err := r.ReadUint(); !errors.Is(err, tc.want) {
					t.Fatalf("err = %v, want %v", err, tc.want)
				}
				if r.Pos() != pos {
					t.Fatalf("cursor moved to %d after rejection", r.Pos())
				}
			}
			r.Reset([]byte{0x07})
			if v, err := r.ReadUint(); err != nil || v != 7 {
				t.Fatalf("reader unusable after rejection: v=%d err=%v", v, err)
			}
		})
	}
}

// Decoding m varints must examine each byte exactly once: total checked
// bytes equals the buffer length, and the prescan/lookback counter is a
// constant 0 regardless of m (single-pass linear, O(own bytes) per value).
func TestSinglePassLinear(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		w := NewWriter()
		var want []uint64
		for i := 0; i < m; i++ {
			v := uint64(i)*2654435761 + 127 // spread across 1..5-byte encodings
			want = append(want, v)
			w.WriteUint(v)
		}
		buf := w.Bytes()
		r := NewReader(buf)
		multi := 0
		for i := 0; i < m; i++ {
			if len(enc.EncodeUint(want[i])) > 1 {
				multi++
			}
			v, err := r.ReadUint()
			if err != nil || v != want[i] {
				t.Fatalf("m=%d i=%d: v=%d err=%v", m, i, v, err)
			}
		}
		if multi == 0 {
			t.Fatalf("m=%d: no multi-byte values in buffer", m)
		}
		if r.Pos() != len(buf) {
			t.Fatalf("m=%d: pos=%d len=%d", m, r.Pos(), len(buf))
		}
		if r.checked != len(buf) {
			t.Fatalf("m=%d: checked=%d, want exactly len(buf)=%d", m, r.checked, len(buf))
		}
		if r.prescan != 0 {
			t.Fatalf("m=%d: prescan=%d, want constant 0", m, r.prescan)
		}
	}
}

// N goroutines write distinct multi-byte values to one Writer; decoding
// must yield exactly those N values, proving writes were not interleaved.
func TestConcurrentWriter(t *testing.T) {
	const n = 256
	w := NewWriter()
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(v uint64) {
			defer wg.Done()
			w.WriteUint(v)
		}(uint64(1<<21 + i)) // all 3-byte varints
	}
	wg.Wait()
	r := NewReader(w.Bytes())
	seen := make(map[uint64]bool, n)
	for i := 0; i < n; i++ {
		v, err := r.ReadUint()
		if err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
		if v < 1<<21 || v >= 1<<21+n || seen[v] {
			t.Fatalf("read %d: unexpected/duplicate value %d", i, v)
		}
		seen[v] = true
	}
	if r.Pos() != r.Len() {
		t.Fatalf("trailing bytes: pos=%d len=%d", r.Pos(), r.Len())
	}
}
