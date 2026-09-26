package main

import (
	"errors"
	"fmt"
	"ontology/api"
	"ontology/splitmix"
	"ontology/stream"
	"os"
	"sync"
)

var fails int

func ok(pass bool) string {
	if !pass {
		fails++
	}
	if pass {
		return "OK"
	}
	return "FAIL"
}

func main() {
	const key, nonce = uint64(0x0123456789ABCDEF), uint64(1)
	seed := splitmix.Seed(key, nonce)
	b0 := splitmix.SplitMix64(seed)

	var ks [8]byte
	for j := range ks {
		ks[j] = byte(b0 >> (8 * uint(j)))
	}
	wantKS := [8]byte{0xf6, 0x86, 0x6b, 0xf5, 0x66, 0x60, 0x23, 0x8a}
	wantCT := [8]byte{0x97, 0xe4, 0x08, 0x91, 0x03, 0x06, 0x44, 0xe2}
	c := stream.New(seed)
	ct := c.XORAt(0, []byte("abcdefgh"))
	fmt.Printf("%s 第三节: keystream % x  ciphertext % x\n", ok(ks == wantKS && [8]byte(ct) == wantCT), ks, ct)
	fmt.Printf("%s 加解密对称\n", ok(string(c.XORAt(0, ct)) == "abcdefgh"))

	naive := func(off int64, n int) []byte {
		out := make([]byte, n)
		for k := range out {
			pos := uint64(off) + uint64(k)
			b := splitmix.SplitMix64(seed ^ (pos / 8))
			out[k] = byte(b >> (8 * (pos % 8)))
		}
		return out
	}
	refOK := true
	for _, off := range []int64{0, 1, 7, 8, 63, 1000, 12345} {
		z := make([]byte, 31)
		if !equal(c.XORAt(off, z), naive(off, len(z))) {
			refOK = false
		}
	}
	fmt.Printf("%s 与朴素参照一致\n", ok(refOK))

	p := []byte("counter-mode random access")
	seq := make([]byte, 9001)
	for i := range seq {
		b := splitmix.SplitMix64(seed ^ (uint64(i) / 8))
		seq[i] = byte(b >> (8 * (uint64(i) % 8)))
	}
	ra := c.XORAt(8888, p)
	raOK := true
	for i := range p {
		if ra[i] != p[i]^seq[8888+i] {
			raOK = false
		}
	}
	fmt.Printf("%s 随机访问一致\n", ok(raOK))
	fmt.Printf("%s 大 m 随机访问计算块数恒为 1\n", ok(stream.RandomAccessCostsOneBlock(seed, []int64{100, 333, 1000, 5000, 10000})))

	s, err := api.New(key, 777)
	_, eKey := api.New(0, 777)
	_, eNonce := api.New(key, 0)
	_, eReuse := api.New(key, 777)
	sentinels := errors.Is(eKey, api.ErrInvalidKey) && errors.Is(eNonce, api.ErrInvalidNonce) &&
		errors.Is(eReuse, api.ErrNonceReuse) && eKey != eNonce && eNonce != eReuse && eKey != eReuse && err == nil
	fmt.Printf("%s 三类可判定错误（哨兵互不相同）\n", ok(sentinels))

	_, stillRejected := api.New(key, 777)
	fresh, eFresh := api.New(key, 778)
	stateOK := errors.Is(stillRejected, api.ErrNonceReuse) && eFresh == nil &&
		string(fresh.Decrypt(fresh.Encrypt([]byte("alive")))) == "alive" &&
		string(s.Decrypt(s.Encrypt([]byte("alive")))) == "alive"
	fmt.Printf("%s 被拒后状态不变、会话仍可用\n", ok(stateOK))

	const N = 32
	shared := s.Encrypt([]byte("concurrent decryption of one ciphertext"))
	var wg sync.WaitGroup
	results := make([][]byte, N)
	wg.Add(N)
	for g := 0; g < N; g++ {
		go func(g int) { defer wg.Done(); results[g] = s.Decrypt(shared) }(g)
	}
	rounds := make([][]byte, N)
	wg.Add(N)
	for g := 0; g < N; g++ {
		go func(g int) {
			defer wg.Done()
			sg, err := api.New(key, uint64(8000+g))
			if err != nil {
				return
			}
			rounds[g] = sg.Decrypt(sg.Encrypt([]byte("same plaintext, different nonce")))
		}(g)
	}
	wg.Wait()
	concOK := true
	for g := 1; g < N; g++ {
		if !equal(results[0], results[g]) {
			concOK = false
		}
	}
	for g := 0; g < N; g++ {
		if string(rounds[g]) != "same plaintext, different nonce" {
			concOK = false
		}
	}
	fmt.Printf("%s 并发一致（N=%d）\n", ok(concOK), N)
	fmt.Printf("%s SelfCheck\n", ok(api.SelfCheck() == nil))

	if fails > 0 {
		os.Exit(1)
	}
}

func equal(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
