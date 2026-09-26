package api_test

import (
	"bytes"
	"encoding/hex"
	"errors"
	"math/rand"
	"sync"
	"testing"

	"ontology/aes"
	"ontology/api"
)

const (
	K  = "000102030405060708090a0b0c0d0e0f"
	P  = "00112233445566778899aabbccddeeff"
	CT = "69c4e0d86a7b0430d8cdb78070b4c55a"
)

func hb(s string) []byte { b, _ := hex.DecodeString(s); return b }

func TestNewCipherInvalidKey(t *testing.T) {
	for _, n := range []int{0, 1, 15, 17, 32} {
		c, err := api.NewCipher(make([]byte, n))
		if !errors.Is(err, api.ErrInvalidKey) || c != nil {
			t.Fatalf("len=%d c=%v err=%v want ErrInvalidKey", n, c, err)
		}
	}
}
func TestEncryptBlockInvalidLength(t *testing.T) {
	c, _ := api.NewCipher(hb(K))
	for _, n := range []int{0, 1, 15, 17, 32} {
		if out, err := c.EncryptBlock(make([]byte, n)); !errors.Is(err, api.ErrInvalidBlock) || out != nil {
			t.Fatalf("len=%d out=%v err=%v want ErrInvalidBlock", n, out, err)
		}
	}
}
func TestUninitializedCipher(t *testing.T) {
	var zero api.Cipher
	if _, err := zero.EncryptBlock(hb(P)); !errors.Is(err, api.ErrUninitialized) {
		t.Fatalf("zero cipher err=%v want ErrUninitialized", err)
	}
	if _, err := (*api.Cipher)(nil).EncryptBlock(hb(P)); !errors.Is(err, api.ErrUninitialized) {
		t.Fatalf("nil cipher err=%v want ErrUninitialized", err)
	}
	if err := zero.SelfCheck(); !errors.Is(err, api.ErrUninitialized) {
		t.Fatalf("zero SelfCheck err=%v want ErrUninitialized", err)
	}
}
func TestErrorsAreDistinct(t *testing.T) {
	c, _ := api.NewCipher(hb(K))
	_, eKey := api.NewCipher(make([]byte, 15))
	_, eBlock := c.EncryptBlock(make([]byte, 15))
	_, eUninit := new(api.Cipher).EncryptBlock(hb(P))
	if eKey == eBlock || eKey == eUninit || eBlock == eUninit {
		t.Fatalf("sentinel errors not distinct: %v %v %v", eKey, eBlock, eUninit)
	}
}
func TestRejectedOperationsLeaveNoTrace(t *testing.T) {
	c, _ := api.NewCipher(hb(K))
	before, _ := c.EncryptBlock(hb(P))
	for i := 0; i < 2; i++ {
		if _, e := api.NewCipher(make([]byte, 15)); !errors.Is(e, api.ErrInvalidKey) {
			t.Fatal("bad key call misbehaved")
		}
		if _, e := c.EncryptBlock(make([]byte, 15)); !errors.Is(e, api.ErrInvalidBlock) {
			t.Fatal("bad block call misbehaved")
		}
		var z api.Cipher
		if _, e := z.EncryptBlock(hb(P)); !errors.Is(e, api.ErrUninitialized) {
			t.Fatal("uninitialized call misbehaved")
		}
	}
	after, err := c.EncryptBlock(hb(P))
	if err != nil || !bytes.Equal(before, after) || !bytes.Equal(after, hb(CT)) {
		t.Fatalf("state changed after rejection: before=%x after=%x err=%v", before, after, err)
	}
}
func TestSelfCheck(t *testing.T) {
	c, err := api.NewCipher(hb(K))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}
func TestRandomVectorsAgainstNaive(t *testing.T) {
	r := rand.New(rand.NewSource(99))
	for i := 0; i < 100; i++ {
		k, p := make([]byte, 16), make([]byte, 16)
		r.Read(k)
		r.Read(p)
		c, err := api.NewCipher(k)
		if err != nil {
			t.Fatal(err)
		}
		got, e1 := c.EncryptBlock(p)
		want, e2 := aes.NaiveEncrypt(k, p)
		if e1 != nil || e2 != nil || !bytes.Equal(got, want) {
			t.Fatalf("case %d mismatch got=%x want=%x", i, got, want)
		}
	}
}
func TestConcurrentEncryptAndSelfCheck(t *testing.T) {
	c, err := api.NewCipher(hb(K))
	if err != nil {
		t.Fatal(err)
	}
	const n = 64
	var wg sync.WaitGroup
	results := make([][]byte, n)
	for g := 0; g < n; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			p := make([]byte, 16)
			for i := range p {
				p[i] = byte(g*17 + i*3)
			}
			got, err := c.EncryptBlock(p)
			want, _ := aes.NaiveEncrypt(hb(K), p)
			again, _ := c.EncryptBlock(p)
			if err != nil || !bytes.Equal(got, want) || !bytes.Equal(got, again) {
				t.Errorf("goroutine %d mismatch", g)
				return
			}
			results[g] = got
		}(g)
	}
	for h := 0; h < 4; h++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				if err := c.SelfCheck(); err != nil {
					t.Errorf("concurrent SelfCheck: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
	for g := 0; g < n; g++ {
		if len(results[g]) != 16 {
			t.Fatalf("missing/invalid result for goroutine %d", g)
		}
	}
}
