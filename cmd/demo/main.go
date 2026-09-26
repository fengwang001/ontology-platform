// Command demo runs the built-in AES-128 judgments and exits non-zero on FAIL.
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/aes"
	"ontology/api"
)

var failed bool

func check(name string, ok bool) {
	tag := "OK"
	if !ok {
		tag, failed = "FAIL", true
	}
	fmt.Printf("%s %s\n", tag, name)
}

func main() {
	key := []byte{0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f}
	pt := []byte{0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff}
	wantCT := []byte{0x69, 0xc4, 0xe0, 0xd8, 0x6a, 0x7b, 0x04, 0x30, 0xd8, 0xcd, 0xb7, 0x80, 0x70, 0xb4, 0xc5, 0x5a}

	w, _ := aes.ExpandKey(key)
	check(fmt.Sprintf("w[4..7]=%08x %08x %08x %08x", w[4], w[5], w[6], w[7]),
		w[4] == 0xd6aa74fd && w[5] == 0xd2af72fa && w[6] == 0xdaa678f1 && w[7] == 0xd6ab76fe)

	c, _ := api.NewCipher(key)
	ct, err := c.EncryptBlock(pt)
	check("FIPS-197 vector", err == nil && eq(ct, wantCT))

	naiveOK := true
	for i := 0; i < 16; i++ {
		k, p := make([]byte, 16), make([]byte, 16)
		for j := range k {
			k[j], p[j] = byte(i*7+j*3), byte(i*13+j*5)
		}
		cc, _ := api.NewCipher(k)
		g, _ := cc.EncryptBlock(p)
		z, _ := aes.NaiveEncrypt(k, p)
		naiveOK = naiveOK && eq(g, z)
	}
	check("matches naive round-by-round reference", naiveOK)
	sched, _ := aes.New(key)
	check("key schedule cached (single expansion)", sched.ScheduleReused(100))

	_, eKey := api.NewCipher(make([]byte, 15))
	_, eBlock := c.EncryptBlock(make([]byte, 15))
	var zero api.Cipher
	_, eUninit := zero.EncryptBlock(pt)
	distinct := errors.Is(eKey, api.ErrInvalidKey) && errors.Is(eBlock, api.ErrInvalidBlock) &&
		errors.Is(eUninit, api.ErrUninitialized) && eKey != eBlock && eKey != eUninit && eBlock != eUninit
	check("three distinct decidable errors", distinct)

	after, e := c.EncryptBlock(pt)
	check("cipher still valid after rejection", e == nil && eq(after, wantCT))
	check("recomputed round-key words = 0 at m=10000", sched.ScheduleReused(10000))
	check("concurrent encryptions consistent", concurrentOK(key))
	check("SelfCheck passes", c.SelfCheck() == nil)

	if failed {
		os.Exit(1)
	}
}

func eq(a, b []byte) bool {
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

func concurrentOK(key []byte) bool {
	const n = 32
	type pair struct{ a, b []byte }
	res := make([]pair, n)
	var wg sync.WaitGroup
	for g := 0; g < n; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			c, _ := api.NewCipher(key)
			p := make([]byte, 16)
			for i := range p {
				p[i] = byte(g*17 + i*3)
			}
			res[g].a, _ = c.EncryptBlock(p)
			res[g].b, _ = aes.NaiveEncrypt(key, p)
		}(g)
	}
	wg.Wait()
	for _, r := range res {
		if !eq(r.a, r.b) {
			return false
		}
	}
	c, _ := api.NewCipher(key)
	p0 := make([]byte, 16)
	x, _ := c.EncryptBlock(p0)
	y, _ := c.EncryptBlock(p0)
	return eq(x, y)
}
