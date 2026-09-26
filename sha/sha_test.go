package sha

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"testing"
)

// TestVsStdlib cross-checks one-shot and byte-at-a-time feeding against
// crypto/sha256 over boundary-sensitive lengths.
func TestVsStdlib(t *testing.T) {
	lengths := []int{0, 1, 3, 55, 56, 57, 63, 64, 65, 119, 120, 127, 128, 129, 1000, 4096}
	for _, n := range lengths {
		msg := make([]byte, n)
		for i := range msg {
			msg[i] = byte(i*13 + 7)
		}
		want := sha256.Sum256(msg)
		one := New()
		if err := one.Write(msg); err != nil {
			t.Fatalf("len %d: %v", n, err)
		}
		if got := one.Finalize(); !bytes.Equal(got, want[:]) {
			t.Errorf("len %d: one-shot mismatch", n)
		}
		byt := New()
		for _, b := range msg {
			if err := byt.Write([]byte{b}); err != nil {
				t.Fatalf("len %d: %v", n, err)
			}
		}
		if got := byt.Finalize(); !bytes.Equal(got, want[:]) {
			t.Errorf("len %d: byte-at-a-time mismatch", n)
		}
	}
}

// TestCompressCount proves incremental caching: after m full blocks, a
// 1-byte Write compresses 0 blocks regardless of m.
func TestCompressCount(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		h := New()
		if err := h.Write(make([]byte, m*64)); err != nil {
			t.Fatal(err)
		}
		if h.blocks != m {
			t.Errorf("m=%d: Write of full blocks compressed %d, want %d", m, h.blocks, m)
		}
		if err := h.Write([]byte{1}); err != nil {
			t.Fatal(err)
		}
		if h.blocks != 0 {
			t.Errorf("m=%d: 1-byte Write compressed %d blocks, want 0", m, h.blocks)
		}
	}
}

// TestOverflowNoTrace rejects writes past the byte limit without
// touching any state, and the hasher keeps working.
func TestOverflowNoTrace(t *testing.T) {
	h := NewWithLimit(9)
	if err := h.Write([]byte("12345678")); err != nil {
		t.Fatal(err)
	}
	if err := h.Write([]byte("ab")); !errors.Is(err, ErrLength) {
		t.Fatalf("want ErrLength, got %v", err)
	}
	if h.total != 8 || h.buflen != 8 || h.blocks != 0 || h.done {
		t.Error("rejected write left a trace")
	}
	if err := h.Write([]byte("9")); err != nil {
		t.Fatalf("hasher unusable after rejection: %v", err)
	}
	want := sha256.Sum256([]byte("123456789"))
	if got := h.Finalize(); !bytes.Equal(got, want[:]) {
		t.Error("digest wrong after rejected write")
	}
}

// TestWriteAfterFinalize rejects writes after Finalize, keeps Finalize
// idempotent, and Reset restores usability.
func TestWriteAfterFinalize(t *testing.T) {
	h := New()
	if err := h.Write([]byte("abc")); err != nil {
		t.Fatal(err)
	}
	d1 := h.Finalize()
	if err := h.Write([]byte("x")); !errors.Is(err, ErrFinalized) {
		t.Fatalf("want ErrFinalized, got %v", err)
	}
	if d2 := h.Finalize(); !bytes.Equal(d1, d2) {
		t.Error("Finalize not idempotent")
	}
	h.Reset()
	if err := h.Write([]byte("abc")); err != nil {
		t.Fatal(err)
	}
	if got := h.Finalize(); !bytes.Equal(got, d1) {
		t.Error("Reset did not restore usability")
	}
}
