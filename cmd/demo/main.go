package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/hamm"
	"ontology/hpack"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
		fmt.Println("FAIL", name)
		return
	}
	fmt.Println("OK  ", name)
}

func main() {
	// nibble 1011 codeword and all seven single-error corrections.
	w, err := hamm.EncodeNibble(11)
	ok := err == nil && w == 0b0110011
	for i := 1; i <= 7 && ok; i++ {
		ok = hamm.DecodeNibble(w^(1<<(7-i))) == 11
	}
	check("1011 codeword 0110011 + 7 single-error corrections", ok)

	// (甲) odd parity would give 1011011.
	d1, d2, d3, d4 := 1, 0, 1, 1
	odd := (1-(d1^d2^d4))<<6 | (1-(d1^d3^d4))<<5 | d1<<4 | (1-(d2^d3^d4))<<3 | d2<<2 | d3<<1 | d4
	check("odd-parity wrong codeword is 1011011", odd == 0b1011011)

	// (乙) reversed syndrome mapping: flipping d1 (pos 3) yields syndrome
	// bits (1,1,0), misread as position 6, corrupting data to 0001.
	corrupt := w ^ (1 << (7 - 3))
	s1 := (corrupt>>6 ^ corrupt>>4 ^ corrupt>>2 ^ corrupt) & 1
	s2 := (corrupt>>5 ^ corrupt>>4 ^ corrupt>>1 ^ corrupt) & 1
	s3 := (corrupt>>3 ^ corrupt>>2 ^ corrupt>>1 ^ corrupt) & 1
	wrongPos := int(s1)*4 + int(s2)*2 + int(s3)
	fixed := corrupt ^ (1 << (7 - wrongPos))
	data := int(fixed>>4&1)<<3 | int(fixed>>2&1)<<2 | int(fixed>>1&1)<<1 | int(fixed&1)
	check("reversed mapping flips pos 6, data becomes 0001", wrongPos == 6 && data == 1)

	// (丙) single nibble packs to 0x66; empty input packs to empty.
	packed := hpack.Pack([]uint8{w})
	check("pack(1011)=0x66, pack(empty)=empty", len(packed) == 1 && packed[0] == 0x66 && len(hpack.Pack(nil)) == 0)

	// Double error (flip p1,p2) is silently miscorrected to nibble 0011.
	double := w ^ 0b1100000
	check("double error silently decodes to 0011", hamm.DecodeNibble(double) == 3)

	// Roundtrip, determinism, chunk-boundary independence.
	ns := []int{11, 0, 15, 7, 8, 3}
	enc, err := api.Encode(ns)
	enc2, _ := api.Encode(ns)
	dec, derr := api.Decode(enc, len(ns))
	ok = err == nil && derr == nil && bytes.Equal(enc, enc2) && equalInts(dec, ns)
	for size := 1; size <= len(enc) && ok; size++ {
		var f hpack.Feeder
		for i := 0; i < len(enc); i += size {
			f.Feed(enc[i:min(i+size, len(enc))])
		}
		got, err := api.Decode(f.Bytes(), len(ns))
		ok = err == nil && equalInts(got, ns)
	}
	check("roundtrip + deterministic + chunk-independent", ok)

	// Three distinguishable errors; failures return nil, no partial output.
	_, e1 := api.Encode([]int{16})
	g1, e2 := api.Decode([]byte{0xff}, 2)
	g2, e3 := api.Decode([]byte{0x67}, 1)
	ok = errors.Is(e1, api.ErrInvalidNibble) && errors.Is(e2, api.ErrTruncated) &&
		errors.Is(e3, api.ErrIllegalPadding) && !errors.Is(e1, api.ErrTruncated) &&
		!errors.Is(e2, api.ErrIllegalPadding) && g1 == nil && g2 == nil
	check("3 sentinel errors distinct, failures return nil", ok)

	// Concurrency: 32 goroutines decode the same single-error stream.
	corruptStream := bytes.Clone(enc)
	corruptStream[2] ^= 0x10
	want, _ := api.Decode(corruptStream, len(ns))
	var wg sync.WaitGroup
	mismatch := make(chan bool, 32)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := api.Decode(corruptStream, len(ns))
			mismatch <- err != nil || !equalInts(got, want)
		}()
	}
	wg.Wait()
	ok = true
	for i := 0; i < 32; i++ {
		ok = ok && !<-mismatch
	}
	check("concurrent decode matches serial", ok)

	// Locate-start scan bits stay constant for large m (counter pinned by
	// hpack's TestLocateScanBound); decode still correct at m=10000.
	big := make([]int, 10000)
	for i := range big {
		big[i] = i % 16
	}
	benc, _ := api.Encode(big)
	bdec, berr := api.Decode(benc, len(big))
	check("locate scan O(1) at m=10000, decode correct", berr == nil && equalInts(bdec, big))

	check("api.SelfCheck", api.SelfCheck() == nil)

	if failed {
		os.Exit(1)
	}
}

func equalInts(a, b []int) bool {
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
