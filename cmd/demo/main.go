// Command demo verifies the base32 codec invariants and prints OK/FAIL lines.
package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"

	"ontology/b32"
	"ontology/codec"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
	}
	fmt.Printf("%s: %s\n", map[bool]string{true: "OK", false: "FAIL"}[ok], name)
}

func input(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i*131 + n)
	}
	return b
}

func main() {
	c := b32.New()
	ok := codec.SelfCheck() == nil
	for n := 0; n <= 20; n++ {
		src := input(n)
		enc := codec.EncodeString(string(src))
		dec, err := codec.DecodeString(enc)
		if err != nil || dec != string(src) || len(enc) != 8*((n+4)/5) {
			ok = false
		}
	}
	check("roundtrip 0..20, length 8*ceil(n/5), SelfCheck", ok)

	ok = true
	for r, want := range map[int]int{1: 6, 2: 4, 3: 3, 4: 1} {
		enc := codec.EncodeString(string(input(r)))
		ok = ok && len(enc)-len(strings.TrimRight(enc, "=")) == want
	}
	check("padding table 1,2,3,4 -> 6,4,3,1", ok)

	_, e1 := codec.DecodeString("ABC!EFGH")
	_, e2 := codec.DecodeString("AB=CDEF=")
	_, e3 := codec.DecodeString("AAAAAA==")
	check("error: invalid char", errors.Is(e1, b32.ErrInvalidChar))
	check("error: padding position", errors.Is(e2, b32.ErrPaddingPosition))
	check("error: padding count", errors.Is(e3, b32.ErrPaddingCount))

	rejected := "ABC!EFGH"
	codec.DecodeString(rejected)
	ok = rejected == "ABC!EFGH" && codec.EncodeString("hi") == "NBUQ===="
	check("rejected input unmodified, codec still usable", ok)

	ok = true
	for _, n := range []int{1000, 100000} {
		before := c.Groups()
		c.Encode(input(n))
		ok = ok && c.Groups()-before == int64((n+4)/5)
	}
	check("group counts exactly ceil(n/5)", ok)

	src := string(input(64))
	want := codec.EncodeString(src)
	var bad atomic.Bool
	var wg sync.WaitGroup
	for g := 0; g < 32; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			dec, err := codec.DecodeString(codec.EncodeString(src))
			if err != nil || dec != src || codec.EncodeString(src) != want {
				bad.Store(true)
			}
		}()
	}
	wg.Wait()
	check("concurrent: 32 goroutines byte-identical", !bad.Load())

	if failed {
		os.Exit(1)
	}
}
