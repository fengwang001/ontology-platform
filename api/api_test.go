package api

import (
	"bytes"
	"errors"
	"math/rand"
	"sync"
	"testing"
)

const apiKey uint64 = 0x0123456789ABCDEF

func TestSessionRoundTrip(t *testing.T) {
	s, _ := New(apiKey, 0x0101)
	rng := rand.New(rand.NewSource(11))
	for _, n := range []int{0, 1, 7, 8, 9, 64, 1000} {
		p := make([]byte, n)
		rng.Read(p)
		if got := s.Decrypt(s.Encrypt(p)); !bytes.Equal(got, p) {
			t.Errorf("len %d round trip mismatch", n)
		}
	}
}

func TestSentinelErrorsDistinct(t *testing.T) {
	New(0x5151, 1) // register the pair exercised by the reuse case
	cases := []struct {
		name     string
		key, non uint64
		want     error
	}{
		{"zero key", 0, 9, ErrInvalidKey},
		{"zero nonce", 9, 0, ErrInvalidNonce},
		{"reuse", 0x5151, 1, ErrNonceReused},
	}
	seen := map[error]bool{}
	for _, tc := range cases {
		_, err := New(tc.key, tc.non)
		if !errors.Is(err, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, err, tc.want)
		}
		seen[err] = true
	}
	if len(seen) != 3 {
		t.Errorf("rejection errors not all distinct: %d unique", len(seen))
	}
}

func TestRejectedNewLeavesNoTrace(t *testing.T) {
	defaultRegistry.mu.Lock()
	before := len(defaultRegistry.pairs)
	defaultRegistry.mu.Unlock()

	reject := []struct {
		key, non uint64
		want     error
	}{
		{0, 0xABCD, ErrInvalidKey},
		{0xABCD, 0, ErrInvalidNonce},
	}
	for _, tc := range reject {
		if _, err := New(tc.key, tc.non); !errors.Is(err, tc.want) {
			t.Fatalf("New(%d,%d): got %v", tc.key, tc.non, err)
		}
	}
	s, err := New(0x6262, 0x71)
	if err != nil {
		t.Fatalf("first New: %v", err)
	}
	if _, err := New(0x6262, 0x71); !errors.Is(err, ErrNonceReused) {
		t.Fatalf("reuse: %v", err)
	}
	defaultRegistry.mu.Lock()
	after := len(defaultRegistry.pairs)
	defaultRegistry.mu.Unlock()
	if after != before+1 { // only the single success registered
		t.Fatalf("registry grew by %d, want 1", after-before)
	}
	p := []byte("usable after rejection")
	if got := s.Decrypt(s.Encrypt(p)); !bytes.Equal(got, p) {
		t.Fatal("established session broken after rejected New")
	}
}

func TestSelfCheck(t *testing.T) {
	if err := SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

func TestConcurrentDecryptAndRoundTrips(t *testing.T) {
	s, _ := New(apiKey, 0x0202)
	p := bytes.Repeat([]byte{0, 1, 2, 3, 4, 5, 6, 7, 0xfe, 0xff}, 50)
	ct := s.Encrypt(p)

	const n = 64
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make([][]byte, n)
	errs := make(chan error, 3*n)

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) { // same ciphertext, concurrent Decrypt
			defer wg.Done()
			<-start
			results[i] = s.Decrypt(ct)
		}(i)
	}
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) { // distinct nonce per goroutine, full round trip
			defer wg.Done()
			<-start
			ss, err := New(0x7000+uint64(i), uint64(i+1))
			if err != nil {
				errs <- err
				return
			}
			if got := ss.Decrypt(ss.Encrypt(p)); !bytes.Equal(got, p) {
				errs <- errors.New("per-nonce round trip mismatch")
			}
		}(i)
	}
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() { // concurrent SelfCheck, isolated registry
			defer wg.Done()
			<-start
			if err := SelfCheck(); err != nil {
				errs <- err
			}
		}()
	}

	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	for i := 1; i < n; i++ {
		if !bytes.Equal(results[i], results[0]) || !bytes.Equal(results[i], p) {
			t.Fatalf("concurrent Decrypt result %d differs", i)
		}
	}
}
