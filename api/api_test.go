package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sync"
	"testing"
)

// TestKnownVectors pins abc/empty/01..40 vectors and truncation for size 1..32.
func TestKnownVectors(t *testing.T) {
	block := make([]byte, 64)
	for i := range block {
		block[i] = byte(i + 1)
	}
	vectors := [][2]string{
		{"", "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"},
		{"abc", "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"},
		{string(block), "20a7ec84684f7fe124cb3727d049734ab0b7da2f52fcafbcef989ecfd91e870b"},
	}
	for _, v := range vectors {
		full, _ := hex.DecodeString(v[1])
		for size := 1; size <= 32; size++ {
			h, err := New(size)
			if err == nil {
				err = h.Write([]byte(v[0]))
			}
			var got []byte
			if err == nil {
				got, err = h.Finalize()
			}
			if err != nil || !bytes.Equal(got, full[:size]) {
				t.Fatalf("size=%d: got %x want %x (err %v)", size, got, full[:size], err)
			}
		}
	}
}

// TestChunkEquivalence verifies Write(A);Write(B) == Write(A‖B) over every
// split point of a multi-block message, and Size reports the configured size.
func TestChunkEquivalence(t *testing.T) {
	msg := make([]byte, 300)
	for i := range msg {
		msg[i] = byte(i*13 + 5)
	}
	ref := sha256.Sum256(msg)
	for cut := 0; cut <= len(msg); cut++ {
		h, _ := New(32)
		if err := h.Write(msg[:cut]); err != nil || h.Write(msg[cut:]) != nil {
			t.Fatal("write failed")
		}
		got, err := h.Finalize()
		if err != nil || !bytes.Equal(got, ref[:]) || h.Size() != 32 {
			t.Fatalf("cut=%d: got %x want %x", cut, got, ref)
		}
	}
}

// TestSentinelErrors verifies the three failure modes are decidable and
// pairwise distinct (overflow itself is exercised inside package sha).
func TestSentinelErrors(t *testing.T) {
	for _, size := range []int{0, -1, 33, 1 << 30} {
		if h, err := New(size); !errors.Is(err, ErrInvalidSize) || h != nil {
			t.Fatalf("New(%d): got (%v,%v), want nil,ErrInvalidSize", size, h, err)
		}
	}
	errs := []error{ErrInvalidSize, ErrOverflow, ErrFinalized}
	for i := range errs {
		for j := i + 1; j < len(errs); j++ {
			if errors.Is(errs[i], errs[j]) {
				t.Fatalf("sentinel errors %d and %d are not distinct", i, j)
			}
		}
	}
}

// TestRejectedStateUntouched verifies rejected writes/finalizes leave the
// stream usable: post-rejection Reset then re-feed reproduces the abc vector.
func TestRejectedStateUntouched(t *testing.T) {
	abc, _ := hex.DecodeString("ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad")
	h, _ := New(32)
	if h.Write([]byte("abc")) != nil {
		t.Fatal("write failed")
	}
	first, err := h.Finalize()
	if err != nil || !bytes.Equal(first, abc) {
		t.Fatal("abc vector mismatch")
	}
	for _, p := range [][]byte{[]byte("x"), []byte("yyy")} {
		if err := h.Write(p); !errors.Is(err, ErrFinalized) {
			t.Fatalf("got %v, want ErrFinalized", err)
		}
	}
	if got, err := h.Finalize(); !errors.Is(err, ErrFinalized) || !bytes.Equal(got, abc) {
		t.Fatal("double finalize must return cached digest + ErrFinalized")
	}
	h.Reset()
	_ = h.Write([]byte("abc"))
	if again, err := h.Finalize(); err != nil || !bytes.Equal(again, abc) {
		t.Fatal("hasher not reusable after Reset")
	}
}

// TestConcurrent runs N independent finalizers and N concurrent readers of one
// finalized instance; all outputs match the reference byte for byte.
func TestConcurrent(t *testing.T) {
	const N = 64
	msg := make([]byte, 500)
	for i := range msg {
		msg[i] = byte(i*17 + 3)
	}
	ref := sha256.Sum256(msg)
	var wg sync.WaitGroup
	outs, reads := make([][]byte, N), make([][]byte, N)
	start := make(chan struct{})
	shared, _ := New(32)
	_ = shared.Write(msg)
	sharedRef, _ := shared.Finalize() // one finalized instance read by all
	for i := 0; i < N; i++ {
		wg.Add(2)
		go func(i int) { // independent hasher per goroutine
			defer wg.Done()
			<-start
			h, _ := New(32)
			_ = h.Write(msg[:len(msg)/2])
			_ = h.Write(msg[len(msg)/2:])
			outs[i], _ = h.Finalize()
		}(i)
		go func(i int) { // concurrent read-only on the same finalized instance
			defer wg.Done()
			<-start
			reads[i], _ = shared.Finalize()
			if shared.Size() != 32 {
				t.Errorf("concurrent Size mismatch")
			}
		}(i)
	}
	close(start)
	wg.Wait()
	for i := 0; i < N; i++ {
		if !bytes.Equal(outs[i], ref[:]) || !bytes.Equal(reads[i], sharedRef) {
			t.Fatalf("goroutine %d produced a divergent digest", i)
		}
	}
	if SelfCheck() != nil {
		t.Fatal("self-check failed")
	}
}
