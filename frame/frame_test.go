package frame

import "testing"

// TestSinglePassByteCount feeds a valid m-byte stream one byte at a time
// (m Feed calls) and asserts the parser inspected exactly m bytes total —
// proving single-pass streaming, not rescanning an accumulated buffer
// (which would cost ~m*m/2). White-box: reads the unexported counter
// directly; no exported API exposes it.
func TestSinglePassByteCount(t *testing.T) {
	for _, m := range []int{100, 256, 1000, 5000, 10000} {
		stream := Encode(make([]byte, m-2)) // zero bytes need no escaping
		if len(stream) != m {
			t.Fatalf("m=%d: stream is %d bytes", m, len(stream))
		}
		p := New()
		for i := 0; i < m; i++ {
			if _, err := p.Feed(stream[i : i+1]); err != nil {
				t.Fatalf("m=%d feed %d: %v", m, i, err)
			}
		}
		if err := p.Flush(); err != nil {
			t.Fatalf("m=%d flush: %v", m, err)
		}
		if p.checked != m {
			t.Fatalf("m=%d: inspected %d bytes, want exactly %d", m, p.checked, m)
		}
		if len(p.frames) != 1 || len(p.frames[0]) != m-2 {
			t.Fatalf("m=%d: frames %v", m, p.frames)
		}
	}
}
