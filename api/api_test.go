package api_test

import (
	"bytes"
	"errors"
	"math/rand"
	"sync"
	"testing"

	"ontology/api"
)

func TestEncryptDecryptRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(20260926))
	lengths := []int{0, 1, 7, 8, 9, 15, 16, 17, 63, 100, 4097}
	for i, n := range lengths {
		p := make([]byte, n)
		rng.Read(p)                                         //nolint:gosec // test data
		s, err := api.New(uint64(0xABC0+i), uint64(5000+i)) // unique pair per case
		if err != nil {
			t.Fatalf("case %d: New: %v", i, err)
		}
		if got := s.Decrypt(s.Encrypt(p)); !bytes.Equal(got, p) {
			t.Fatalf("case %d n=%d: round-trip mismatch", i, n)
		}
		// EncryptAt at offset 0 must equal Encrypt.
		if got := s.EncryptAt(0, p); !bytes.Equal(got, s.Encrypt(p)) {
			t.Fatalf("case %d: EncryptAt(0) != Encrypt", i)
		}
	}
}

func TestSpecVector(t *testing.T) {
	// NOTES.md 第三节：nonce=1 的八字节密文。
	s, err := api.New(0x0123456789ABCDEF, 1)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	want := []byte{0x97, 0xe4, 0x08, 0x91, 0x03, 0x06, 0x44, 0xe2}
	if got := s.Encrypt([]byte("abcdefgh")); !bytes.Equal(got, want) {
		t.Fatalf("spec vector: got % x want % x", got, want)
	}
}

func TestInvalidInputsSentinels(t *testing.T) {
	_, eKey := api.New(0, 6001)
	_, eNonce := api.New(0xABC1, 0)
	if !errors.Is(eKey, api.ErrInvalidKey) || !errors.Is(eNonce, api.ErrInvalidNonce) {
		t.Fatalf("key/nonce errors wrong: %v %v", eKey, eNonce)
	}
	if _, err := api.New(0xABC2, 6002); err != nil {
		t.Fatalf("New: %v", err)
	}
	_, eReuse := api.New(0xABC2, 6002)
	if !errors.Is(eReuse, api.ErrNonceReuse) {
		t.Fatalf("reuse error: %v", eReuse)
	}
	if eKey == eNonce || eNonce == eReuse || eKey == eReuse {
		t.Fatal("the three sentinel errors must be pairwise distinct")
	}
}

func TestRejectionLeavesNoTrace(t *testing.T) {
	const k, n = uint64(0xABC3), uint64(7001)
	// Rejected calls before the pair ever exists...
	if _, err := api.New(0, n); !errors.Is(err, api.ErrInvalidKey) {
		t.Fatalf("key check: %v", err)
	}
	if _, err := api.New(k, 0); !errors.Is(err, api.ErrInvalidNonce) {
		t.Fatalf("nonce check: %v", err)
	}
	// ...must not have consumed the (key,nonce) pair.
	s, err := api.New(k, n)
	if err != nil {
		t.Fatalf("pair should still be free after rejections: %v", err)
	}
	// Now registered; reuse rejected repeatedly, registry never flips.
	for i := 0; i < 3; i++ {
		if _, err := api.New(k, n); !errors.Is(err, api.ErrNonceReuse) {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	if got := s.Decrypt(s.Encrypt([]byte("session intact"))); string(got) != "session intact" {
		t.Fatalf("existing session altered: %q", got)
	}
	fresh, err := api.New(k, n+1)
	if err != nil {
		t.Fatalf("fresh pair blocked: %v", err)
	}
	if got := fresh.Decrypt(fresh.Encrypt([]byte("x"))); string(got) != "x" {
		t.Fatal("fresh session broken")
	}
}

func TestConcurrentDecryptAndSessions(t *testing.T) {
	s, err := api.New(0xABC4, 8001)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ct := s.Encrypt([]byte("shared ciphertext decrypted by every goroutine"))
	const N = 64
	var wg sync.WaitGroup
	outs := make([][]byte, N)
	wg.Add(N)
	for g := 0; g < N; g++ {
		go func(g int) { defer wg.Done(); outs[g] = s.Decrypt(ct) }(g)
	}
	rounds := make([][]byte, N)
	wg.Add(N)
	for g := 0; g < N; g++ {
		go func(g int) {
			defer wg.Done()
			sg, err := api.New(uint64(0xABD0+g), uint64(9000+g))
			if err != nil {
				t.Errorf("goroutine %d New: %v", g, err)
				return
			}
			rounds[g] = sg.Decrypt(sg.Encrypt([]byte("same plaintext, own nonce")))
		}(g)
	}
	selfErrs := make(chan error, N)
	wg.Add(N)
	for g := 0; g < N; g++ {
		go func() { defer wg.Done(); selfErrs <- api.SelfCheck() }()
	}
	wg.Wait()
	close(selfErrs)
	for g := 1; g < N; g++ {
		if !bytes.Equal(outs[0], outs[g]) {
			t.Fatalf("decrypt divergence at goroutine %d", g)
		}
	}
	for g := 0; g < N; g++ {
		if string(rounds[g]) != "same plaintext, own nonce" {
			t.Fatalf("round-trip failure at goroutine %d", g)
		}
	}
	for err := range selfErrs {
		if err != nil {
			t.Fatalf("SelfCheck: %v", err)
		}
	}
}

func TestSelfCheck(t *testing.T) {
	if err := api.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}
