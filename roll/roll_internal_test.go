package roll

import "testing"

func recompute(win []byte) uint64 {
	var v uint64
	for _, b := range win {
		v = v*base + uint64(b)
	}
	return v
}

func TestRoll(t *testing.T) {
	t.Run("advance_is_incremental_and_bounded", func(t *testing.T) {
		cases := []int{100_000, 1_000_000}
		for _, n := range cases {
			h, _ := New(16)
			last := uint64(0)
			for i := 0; i < n; i++ {
				b := byte(i*7 + i>>3)
				if v, full := h.Push(b); full {
					last = v
				}
			}
			if h.adv > uint64(n) {
				t.Fatalf("n=%d advances=%d, want <= n", n, h.adv)
			}
			if h.adv != uint64(n-h.w) {
				t.Fatalf("n=%d advances=%d, want %d (linear, coefficient 1)", n, h.adv, n-h.w)
			}
			// last window must equal a full recomputation => no byte skipped/repeated
			if got := recompute(h.win); got != last {
				t.Fatalf("n=%d window hash mismatch: inc=%d recompute=%d", n, last, got)
			}
			t.Logf("n=%d advances=%d", n, h.adv)
		}
	})

	t.Run("no_byte_advanced_twice", func(t *testing.T) {
		// Each filled Push overwrites exactly one slot; the evicted logical
		// byte must be exactly w positions behind the incoming one, proving
		// no byte is advanced twice or skipped.
		h, _ := New(8)
		holder := make([]int, h.w) // logical input index held by each slot
		for i := 0; i < 500; i++ {
			slot := -1
			if h.filled {
				slot = h.pos
			}
			h.Push(byte(i + 1))
			if slot == -1 {
				if h.filled {
					for j := 0; j < h.w; j++ {
						holder[j] = j
					}
				}
				continue
			}
			if holder[slot] != i-h.w {
				t.Fatalf("slot %d evicted index %d, want %d", slot, holder[slot], i-h.w)
			}
			holder[slot] = i
		}
		if h.adv != uint64(500-h.w) {
			t.Fatalf("advances=%d want %d", h.adv, 500-h.w)
		}
	})

	t.Run("bad_window", func(t *testing.T) {
		for _, w := range []int{0, -1} {
			if _, err := New(w); err != ErrBadWindow {
				t.Fatalf("w=%d err=%v want ErrBadWindow", w, err)
			}
		}
	})
}
