// Command demo exercises the Hamming(7,4) packages and prints OK/FAIL lines.
package main

import (
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
	status := "OK"
	if !ok {
		status = "FAIL"
		failed = true
	}
	fmt.Printf("%s %s\n", status, name)
}

func main() {
	// hamm: codeword of nibble 1011 and correction of all 7 single-bit errors.
	cw := hamm.EncodeNibble(0b1011)
	ok := cw == 0b0110011
	for pos := 1; pos <= 7; pos++ {
		ok = ok && hamm.DecodeNibble(cw^(1<<(7-pos))) == 0b1011
	}
	check("hamm: 1011->0110011, 7 single errors corrected", ok)

	// (甲) odd parity would give the wrong codeword 1011011.
	odd := 1<<6 | 0<<5 | 1<<4 | 1<<3 | 0<<2 | 1<<1 | 1 // p1p2p3 = NOT(...) = 101
	check("odd-parity miscodeword 1011011 != 0110011", odd == 0b1011011 && odd != cw)

	// (乙) reversed syndrome mapping mis-corrects a flipped d1 to position 6.
	bad := cw ^ 1<<(7-3)       // flip d1 (position 3): syndrome bits (1,1,0)
	wrongSyn := 1*4 + 1*2 + 0  // reversed mapping: 6 instead of 3
	bad ^= 1 << (7 - wrongSyn) // "correct" the wrong bit
	got := bad>>4&1<<3 | bad>>2&1<<2 | bad>>1&1<<1 | bad&1
	check("reversed mapping miscorrects to 0001", got == 0b0001 && got != 0b1011)

	// hpack: single nibble 1011 packs to 0x66; empty input packs to empty.
	packed := hpack.Pack([]int{0b1011})
	check("hpack: [11]->0x66, []->empty", len(packed) == 1 && packed[0] == 0x66 && len(hpack.Pack(nil)) == 0)

	// api: built-in self check (reference, chunking, determinism, atomicity).
	check("api: SelfCheck", api.SelfCheck() == nil)

	// api: three sentinel errors are mutually distinguishable.
	_, eTrunc := api.Decode([]byte{0x66}, 2)
	_, ePad := api.Decode([]byte{0x67}, 1)
	eNib := api.CheckNibbles([]int{16})
	check("api: 3 sentinel errors distinguishable",
		errors.Is(eTrunc, api.ErrTruncated) && errors.Is(ePad, api.ErrIllegalPadding) &&
			errors.Is(eNib, api.ErrInvalidNibble) && !errors.Is(eTrunc, api.ErrIllegalPadding) &&
			!errors.Is(ePad, api.ErrTruncated) && api.Encode([]int{16}) == nil)

	// api: a rejected decode returns nil, no partial output.
	gotNil, err := api.Decode([]byte{0x66, 0x01}, 2)
	check("api: rejection leaves no partial output", gotNil == nil && err != nil)

	// (丙) double error (flip p1,p2) silently miscorrects 1011 to 0011.
	dbl, _ := api.Decode([]byte{0x66 ^ 0xC0}, 1)
	check("api: double error silently yields 0011", len(dbl) == 1 && dbl[0] == 0b0011)

	// hpack: decode correct at growing m; O(1) locate scans pinned by hpack test.
	ok = true
	for _, m := range []int{100, 1000, 10000} {
		ns := make([]int, m)
		for i := range ns {
			ns[i] = i % 16
		}
		got, _ := api.Decode(api.Encode(ns), m)
		ok = ok && len(got) == m && got[m-1] == (m-1)%16
	}
	check("hpack: large-m decode correct (locate scans O(1))", ok)

	// api: concurrent decodes of a single-error stream match the serial one.
	base := api.Encode([]int{11, 5, 9, 14, 3, 0, 8, 7})
	base[1] ^= 0x08 // inject one bit error
	want, _ := api.Decode(base, 8)
	var wg sync.WaitGroup
	mismatch := make(chan bool, 64)
	for g := 0; g < 64; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, _ := api.Decode(base, 8)
			for i := range want {
				if got[i] != want[i] {
					mismatch <- true
				}
			}
		}()
	}
	wg.Wait()
	check("api: 64 concurrent decodes match serial", len(mismatch) == 0)

	if failed {
		os.Exit(1)
	}
}
