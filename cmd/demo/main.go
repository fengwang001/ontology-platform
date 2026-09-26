// Command demo exercises the counter-mode stream cipher and prints OK/FAIL
// for each required property. It takes no arguments and performs no network
// access; exit status is 0 only when every check passes.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"sync"

	"ontology/api"
	"ontology/splitmix"
	"ontology/stream"
)

func main() {
	ok := true
	check := func(name string, pass bool) {
		if pass {
			fmt.Println("OK   " + name)
		} else {
			ok = false
			fmt.Println("FAIL " + name)
		}
	}

	const key, nonce uint64 = 0x0123456789ABCDEF, 0x0000000000000001
	seed := splitmix.Seed(key, nonce)

	check("splitmix vectors",
		seed == 0xe821eebbc0778421 && splitmix.SplitMix64(0) == 0xE220A8397B1DCDAF)

	c := stream.New(seed)
	taskS, _ := api.New(key, nonce)
	wantCT := []byte{0x97, 0xe4, 0x08, 0x91, 0x03, 0x06, 0x44, 0xe2}
	check("eight keystream/cipher bytes", bytes.Equal(c.Encrypt([]byte("abcdefgh")), wantCT))

	p := []byte{0x00, 0x01, 0x7f, 0x80, 0xfe, 0xff, 0x10, 0x20, 0x30, 0x40, 0x55, 0xaa, 0x00, 0xff, 0x01}
	ct := c.Encrypt(p)
	off := int64(4999)
	check("symmetry/reference/random-access",
		bytes.Equal(c.Decrypt(ct), p) &&
			bytes.Equal(ct, xor(p, naiveKeystream(seed, int64(len(p))))) &&
			bytes.Equal(c.EncryptAt(off, p), naiveXor(seed, off, p)))

	check("random access computes one block at large m", stream.RandomAccessCostInvariant() == nil)
	check("api SelfCheck", api.SelfCheck() == nil)

	_, eKey := api.New(0, 5)
	_, eNonce := api.New(5, 0)
	s1, _ := api.New(777, 1)
	_, eReuse := api.New(777, 1)
	distinct := errors.Is(eKey, api.ErrInvalidKey) && errors.Is(eNonce, api.ErrInvalidNonce) &&
		errors.Is(eReuse, api.ErrNonceReused) && eKey != eNonce && eKey != eReuse && eNonce != eReuse
	check("three distinct sentinel errors", distinct)

	plain := []byte("still usable after rejection")
	s2, errFresh := api.New(888, 2)
	noTrace := errFresh == nil && bytes.Equal(s2.Decrypt(s2.Encrypt(plain)), plain) &&
		bytes.Equal(s1.Decrypt(s1.Encrypt(plain)), plain)
	check("rejected ops leave no trace", noTrace)

	check("concurrent decrypt/selfcheck/per-nonce round trips", concurrentOK(taskS, ct, p))

	if !ok {
		panic("demo: one or more checks failed")
	}
}

func concurrentOK(s *api.Session, ct, p []byte) bool {
	const n = 32
	var wg sync.WaitGroup
	fail := make(chan bool, 3*n)
	base := make(chan struct{})

	for i := 0; i < n; i++ {
		wg.Add(3)
		go func() { // many concurrent Decrypt of one ciphertext
			defer wg.Done()
			<-base
			fail <- !bytes.Equal(s.Decrypt(ct), p)
		}()
		go func() { // concurrent SelfCheck
			defer wg.Done()
			<-base
			fail <- api.SelfCheck() != nil
		}()
		go func(i int) { // distinct nonce per goroutine round trip
			defer wg.Done()
			<-base
			ss, err := api.New(1000+uint64(i), uint64(i+1))
			if err != nil || !bytes.Equal(ss.Decrypt(ss.Encrypt(p)), p) {
				fail <- true
			} else {
				fail <- false
			}
		}(i)
	}
	close(base)
	wg.Wait()
	close(fail)
	for f := range fail {
		if f {
			return false
		}
	}
	return true
}

// naiveKeystream builds n keystream bytes by generating blocks from block 0.
func naiveKeystream(seed uint64, n int64) []byte {
	out := make([]byte, n)
	for j := range out {
		b := splitmix.SplitMix64(seed ^ uint64(j/8))
		out[j] = byte(b >> (8 * (j % 8)))
	}
	return out
}

// naiveXor builds the keystream slice starting at offset and XORs p.
func naiveXor(seed uint64, offset int64, p []byte) []byte {
	out := make([]byte, len(p))
	for k := range out {
		abs := uint64(offset) + uint64(k)
		b := splitmix.SplitMix64(seed ^ abs/8)
		out[k] = p[k] ^ byte(b>>(8*(abs%8)))
	}
	return out
}

func xor(a, b []byte) []byte {
	out := make([]byte, len(a))
	for i := range a {
		out[i] = a[i] ^ b[i]
	}
	return out
}
