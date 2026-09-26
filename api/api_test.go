package api_test

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"sync"
	"testing"

	"ontology/api"
	"ontology/sha"
)

func writeAll(t *testing.T, h *api.Hasher, p []byte) {
	t.Helper()
	if err := h.Write(p); err != nil {
		t.Fatal(err)
	}
}

func TestVectorABC(t *testing.T) {
	h, err := api.New(32)
	if err != nil {
		t.Fatal(err)
	}
	writeAll(t, h, []byte("abc"))
	want := sha256.Sum256([]byte("abc"))
	if got := h.Finalize(); !bytes.Equal(got, want[:]) {
		t.Errorf("got %x, want %x", got, want)
	}
}

// TestSplitInvariance: Write(A);Write(B) equals Write(A|B) at every cut.
func TestSplitInvariance(t *testing.T) {
	for _, n := range []int{0, 1, 64, 200, 1024} {
		msg := make([]byte, n)
		for i := range msg {
			msg[i] = byte(i*17 + 1)
		}
		one, _ := api.New(32)
		writeAll(t, one, msg)
		ref := one.Finalize()
		for cut := 0; cut <= n; cut++ {
			two, _ := api.New(32)
			writeAll(t, two, msg[:cut])
			writeAll(t, two, msg[cut:])
			if got := two.Finalize(); !bytes.Equal(got, ref) {
				t.Fatalf("len %d cut %d: mismatch", n, cut)
			}
		}
	}
}

// TestTruncation: Finalize output is the digest prefix of length size.
func TestTruncation(t *testing.T) {
	want := sha256.Sum256([]byte("truncate me"))
	for size := 1; size <= 32; size++ {
		h, err := api.New(size)
		if err != nil {
			t.Fatal(err)
		}
		if h.Size() != size {
			t.Errorf("Size()=%d, want %d", h.Size(), size)
		}
		writeAll(t, h, []byte("truncate me"))
		if got := h.Finalize(); !bytes.Equal(got, want[:size]) {
			t.Errorf("size %d: got %x", size, got)
		}
	}
}

// TestRejectedNoTrace: three distinct sentinel error classes, and
// rejected operations leave the hasher unchanged and usable.
func TestRejectedNoTrace(t *testing.T) {
	for _, size := range []int{-1, 0, 33, 100} {
		if _, err := api.New(size); !errors.Is(err, api.ErrSize) {
			t.Errorf("size %d: want ErrSize, got %v", size, err)
		}
	}
	h, _ := api.New(32)
	writeAll(t, h, []byte("abc"))
	before := h.Finalize()
	if err := h.Write([]byte("junk")); !errors.Is(err, sha.ErrFinalized) {
		t.Fatalf("want ErrFinalized, got %v", err)
	}
	if !bytes.Equal(h.Finalize(), before) {
		t.Error("rejected write left a trace")
	}
	if err := sha.NewWithLimit(1).Write([]byte("ab")); !errors.Is(err, sha.ErrLength) {
		t.Fatalf("want ErrLength, got %v", err)
	}
	if api.ErrSize == sha.ErrFinalized || sha.ErrFinalized == sha.ErrLength || api.ErrSize == sha.ErrLength {
		t.Error("sentinel errors are not mutually distinct")
	}
	h.Reset()
	writeAll(t, h, []byte("abc"))
	if !bytes.Equal(h.Finalize(), before) {
		t.Error("Reset did not restore usability")
	}
}

// TestConcurrent: N goroutines hash independently, N more read one
// finalized instance, N more run SelfCheck; all must agree.
func TestConcurrent(t *testing.T) {
	msg := []byte("the quick brown fox jumps over the lazy dog")
	shared, _ := api.New(32)
	writeAll(t, shared, msg)
	want := shared.Finalize()
	const n = 32
	start, bad := make(chan struct{}), make(chan string, 3*n)
	var wg sync.WaitGroup
	run := func(f func() string) {
		defer wg.Done()
		<-start
		if s := f(); s != "" {
			bad <- s
		}
	}
	for i := 0; i < n; i++ {
		wg.Add(3)
		go run(func() string {
			h, _ := api.New(32)
			if err := h.Write(msg); err != nil || !bytes.Equal(h.Finalize(), want) {
				return "independent hash mismatch"
			}
			return ""
		})
		go run(func() string {
			if !bytes.Equal(shared.Finalize(), want) || shared.Size() != 32 {
				return "shared read mismatch"
			}
			return ""
		})
		go run(func() string {
			if err := api.SelfCheck(); err != nil {
				return err.Error()
			}
			return ""
		})
	}
	close(start)
	wg.Wait()
	close(bad)
	for s := range bad {
		t.Error(s)
	}
}
