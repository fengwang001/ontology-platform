package sha

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
)

// TestLastBlocksCounter proves incremental caching: after m full blocks, a
// trailing 1-byte Write compresses zero blocks, independent of m.
func TestLastBlocksCounter(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		h := New()
		if err := h.Write(make([]byte, m*64)); err != nil {
			t.Fatal(err)
		}
		if h.lastBlocks != m {
			t.Fatalf("m=%d: bulk Write compressed %d blocks, want %d", m, h.lastBlocks, m)
		}
		if err := h.Write([]byte{0x01}); err != nil {
			t.Fatal(err)
		}
		if h.lastBlocks != 0 {
			t.Fatalf("m=%d: trailing byte compressed %d blocks, want 0", m, h.lastBlocks)
		}
	}
}

// TestOverflowNoTrace rejects a write that would overflow the 64-bit bit
// length and verifies no state (buffer, cached state, counters, flag) moved.
func TestOverflowNoTrace(t *testing.T) {
	h := New()
	if err := h.Write([]byte("prefix")); err != nil {
		t.Fatal(err)
	}
	before := *h
	h.total = maxBytes // simulate a nearly-full stream
	if err := h.Write([]byte{0x01}); !errors.Is(err, ErrOverflow) {
		t.Fatalf("got %v, want ErrOverflow", err)
	}
	after := *h
	after.total = before.total // total was set by the test, not by Write
	if after != before {
		t.Fatal("rejected overflow write changed hasher state")
	}
	if err := h.Write([]byte("ok")); !errors.Is(err, ErrOverflow) {
		t.Fatalf("still-overflowing write: got %v, want ErrOverflow", err)
	}
}

// TestFinalizedNoTrace rejects Write/Finalize after Finalize without Reset.
func TestFinalizedNoTrace(t *testing.T) {
	h := New()
	_ = h.Write([]byte("abc"))
	if _, err := h.Finalize(); err != nil {
		t.Fatal(err)
	}
	before := *h
	if err := h.Write([]byte("x")); !errors.Is(err, ErrFinalized) {
		t.Fatalf("write after finalize: got %v, want ErrFinalized", err)
	}
	if _, err := h.Finalize(); !errors.Is(err, ErrFinalized) {
		t.Fatalf("double finalize: got %v, want ErrFinalized", err)
	}
	if *h != before {
		t.Fatal("rejected post-finalize ops changed hasher state")
	}
	h.Reset()
	_ = h.Write([]byte("abc"))
	d, err := h.Finalize()
	want, _ := hex.DecodeString("ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad")
	if err != nil || !bytes.Equal(d[:], want) {
		t.Fatal("hasher not reusable after Reset")
	}
}

// TestVectorsAgainstStdlib cross-checks the streaming hasher against
// crypto/sha256 over many lengths and random split points.
func TestVectorsAgainstStdlib(t *testing.T) {
	for _, n := range []int{0, 1, 3, 55, 56, 57, 63, 64, 65, 119, 120, 127, 128, 129, 1000} {
		msg := make([]byte, n)
		for i := range msg {
			msg[i] = byte(i*31 + 7)
		}
		want := sha256.Sum256(msg)
		for _, cut := range []int{0, 1, n / 3, n / 2, n - 1, n} {
			if cut < 0 || cut > n {
				continue
			}
			h := New()
			if err := h.Write(msg[:cut]); err != nil {
				t.Fatal(err)
			}
			if err := h.Write(msg[cut:]); err != nil {
				t.Fatal(err)
			}
			got, err := h.Finalize()
			if err != nil || got != want {
				t.Fatalf("n=%d cut=%d: got %x want %x", n, cut, got, want)
			}
		}
	}
}

// TestSelfCheck pins the built-in self-check to green.
func TestSelfCheck(t *testing.T) {
	if err := SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
