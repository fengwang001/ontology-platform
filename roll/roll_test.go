package roll

import (
	"testing"
)

func TestInvalidWindow(t *testing.T) {
	for _, w := range []int{0, -1} {
		if _, err := New(w); err != ErrWindow {
			t.Fatalf("New(%d) err=%v, want ErrWindow", w, err)
		}
	}
}

// refHash recomputes the window hash from scratch.
func refHash(b []byte) uint32 {
	var h uint32
	for _, c := range b {
		h = h*base + uint32(c)
	}
	return h
}

func TestIncrementalHash(t *testing.T) {
	cases := []struct {
		name string
		w    int
		n    int
	}{
		{"w1", 1, 50}, {"w3", 3, 120}, {"w7", 7, 120}, {"w64", 64, 300},
	}
	data := make([]byte, 301)
	for i := range data {
		data[i] = byte(i*7 + 3)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, _ := New(tc.w)
			i := 0
			for ; i < tc.w; i++ {
				h.Write(data[i])
			}
			if h.Sum32() != refHash(data[:tc.w]) {
				t.Fatal("initial window mismatch")
			}
			for ; i < tc.n; i++ {
				// The byte at ring position pos is exactly the one about to
				// leave; it must be the byte fed w steps earlier, proving no
				// byte is ever advanced through the window twice.
				evict := h.buf[h.pos]
				if evict != data[i-tc.w] {
					t.Fatalf("step %d evicted %d, want %d", i, evict, data[i-tc.w])
				}
				h.Push(data[i])
				if h.Sum32() != refHash(data[i-tc.w+1:i+1]) {
					t.Fatalf("step %d hash mismatch", i)
				}
			}
			if h.pushes != int64(tc.n-tc.w) {
				t.Fatalf("pushes=%d want %d", h.pushes, tc.n-tc.w)
			}
		})
	}
}

// TestPushComplexity asserts, for two stream lengths, that the number of
// advances is proportional to stream length with coefficient <= 1, and that
// each byte enters/exits the window exactly once (no repeated advances).
func TestPushComplexity(t *testing.T) {
	const w = 16
	var prev int64
	for _, n := range []int{100_000, 1_000_000} {
		h, _ := New(w)
		seen := make([]int, 256) // eviction count per byte value
		i := 0
		for ; i < w; i++ {
			h.Write(byte(i))
		}
		for ; i < n; i++ {
			evict := h.buf[h.pos]
			seen[evict]++
			h.Push(byte(i))
		}
		if h.pushes > int64(n) {
			t.Fatalf("n=%d pushes=%d, coefficient must be <= 1", n, h.pushes)
		}
		if h.pushes != int64(n-w) {
			t.Fatalf("n=%d pushes=%d want %d", n, h.pushes, n-w)
		}
		// With byte(i) feeding, values 0..255 recur; within the window of
		// 16 they are distinct, so each eviction value count is bounded and
		// total evictions equal pushes: every byte leaves at most once.
		var total int
		for _, c := range seen {
			total += c
		}
		if int64(total) != h.pushes {
			t.Fatalf("evictions %d != pushes %d", total, h.pushes)
		}
		if prev != 0 {
			// pushes = n - w exactly, so pushes2 - 10*pushes1 = 9w (constant).
			if d := h.pushes - prev*10; d != int64(9*w) {
				t.Fatalf("not linear: %d -> %d (delta %d)", prev, h.pushes, d)
			}
		}
		prev = h.pushes
		t.Logf("stream length %d pushes %d ratio %.4f", n, h.pushes, float64(h.pushes)/float64(n))
	}
}
