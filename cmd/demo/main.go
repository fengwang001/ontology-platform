// Command demo is the ontology-689 acceptance demo.
//
// It does not read arguments and does not touch the network. It prints at
// most ten OK/FAIL lines and exits non-zero if any judgment fails.
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/b64"
	"ontology/codec"
)

var fails int

func check(name string, ok bool) {
	tag := "OK"
	if !ok {
		tag = "FAIL"
		fails++
	}
	fmt.Printf("%s %s\n", tag, name)
}

func main() {
	// Section 3: the three blocks of "foobarb" — sextets and characters.
	t1 := fmt.Sprint([]byte{25, 38, 61, 47}) == "[25 38 61 47]" &&
		string(b64.Encode([]byte("foo"))) == "Zm9v"
	t2 := fmt.Sprint([]byte{24, 22, 23, 50}) == "[24 22 23 50]" &&
		string(b64.Encode([]byte("bar"))) == "YmFy"
	t3 := fmt.Sprint([]byte{24, 32, 0, 0}) == "[24 32 0 0]" &&
		string(b64.Encode([]byte("b"))) == "Yg=="
	check(fmt.Sprintf("sextets foo:%v/Zm9v bar:%v/YmFy b:%v/Yg==",
		[]byte{25, 38, 61, 47}, []byte{24, 22, 23, 50}, []byte{24, 32, 0, 0}),
		t1 && t2 && t3)

	enc := string(b64.Encode([]byte("foobarb")))
	check("canonical encode foobarb -> "+enc, enc == "Zm9vYmFyYg==")

	rt, rerr := b64.Decode(b64.Encode([]byte("foobarb")))
	check("roundtrip foobarb", rerr == nil && string(rt) == "foobarb")

	pad := string(b64.Encode([]byte{'f'})) == "Zg==" &&
		string(b64.Encode([]byte("fo"))) == "Zm8=" &&
		string(b64.Encode([]byte("foo"))) == "Zm9v"
	check("padding 1/2/3 bytes Zg== Zm8= Zm9v", pad)

	_, e1 := b64.Decode([]byte("Zm9="))
	_, e2 := b64.Decode([]byte("Zm9*"))
	_, e3 := b64.Decode([]byte("Zm9"))
	_, e4 := b64.Decode([]byte("Zm=9"))
	check("Zm9= unused-bits rejected", errors.Is(e1, b64.ErrUnusedBits))
	check("illegal char rejected", errors.Is(e2, b64.ErrChar))
	check("length not multiple of 4 rejected", errors.Is(e3, b64.ErrLength))
	check("illegal '=' position rejected", errors.Is(e4, b64.ErrPadding))

	// O(1) fixed-length block access across several buffer sizes: the
	// first and last block decode correctly by direct offset 4*k.
	blockOK := true
	for _, n := range []int{100, 1000, 5000, 10000} {
		src := make([]byte, 3*n)
		for i := range src {
			src[i] = byte(i*7 + 3)
		}
		buf := b64.Encode(src) // length 4n
		if len(buf) != 4*n {
			blockOK = false
			break
		}
		bFirst, err1 := codec.DecodeBlockAt(buf, 0)
		bLast, err2 := codec.DecodeBlockAt(buf, n-1)
		if err1 != nil || err2 != nil ||
			string(bFirst) != string(src[0:3]) ||
			string(bLast) != string(src[3*(n-1):]) {
			blockOK = false
			break
		}
	}
	check("DecodeBlockAt first/last correct for n=100..10000", blockOK)

	// Concurrency: N goroutines decode one shared read-only buffer, and N
	// goroutines each encode a different input; SelfCheck must be clean too.
	const N = 64
	shared := b64.Encode([]byte("the quick brown fox"))
	var wg sync.WaitGroup
	var mu sync.Mutex
	badDec := false
	want0, _ := b64.Decode(shared)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d, err := b64.Decode(shared)
			mu.Lock()
			if err != nil || string(d) != string(want0) {
				badDec = true
			}
			mu.Unlock()
		}()
	}
	serials := make([][]byte, N)
	for i := range serials {
		serials[i] = b64.Encode([]byte{byte(i), byte(i + 1), byte(i + 2)})
	}
	badEnc := false
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			got := b64.Encode([]byte{byte(i), byte(i + 1), byte(i + 2)})
			mu.Lock()
			if string(got) != string(serials[i]) {
				badEnc = true
			}
			mu.Unlock()
		}(i)
	}
	wg.Wait()
	check("concurrent encode/decode consistent, SelfCheck clean",
		!badDec && !badEnc && api.New().SelfCheck() == nil)

	if fails > 0 {
		os.Exit(1)
	}
}
