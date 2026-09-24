// Command demo exercises the varint codec checks and prints OK/FAIL per item.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"os"
	"slices"
	"sync"

	"ontology/codec"
	"ontology/vint"
)

var vals = []int64{math.MinInt64, -1, 0, 1, math.MaxInt64}

func main() {
	ok := true
	ok = report("roundtrip", codec.SelfCheck(vals) == nil) && ok

	lr := true
	for _, v := range vals {
		enc := codec.EncodeInt(v)
		_, n, err := codec.DecodeInt(enc)
		lr = lr && len(enc) <= 10 && n == len(enc) && err == nil
	}
	ok = report("len<=10 & exact read", lr) && ok

	got, err := codec.DecodeSlice(codec.EncodeSlice(vals))
	ok = report("slice roundtrip", err == nil && slices.Equal(got, vals)) && ok

	enc := codec.EncodeSlice(vals)
	sp := true
	for cut := 0; cut <= len(enc); cut++ {
		g, e := codec.DecodeSlice(enc[:cut])
		sp = sp && (e == nil || errors.Is(e, vint.ErrIncomplete))
		sp = sp && (e != nil || slices.Equal(g, vals[:len(g)]))
	}
	ok = report("split at any byte", sp) && ok

	e1 := decErr([]byte{0x80})
	e2 := decErr([]byte{0x80, 0x00})
	e3 := decErr(bytes.Repeat([]byte{0x80}, 10))
	ok = report("3 distinct decode errors",
		errors.Is(e1, vint.ErrIncomplete) &&
			errors.Is(e2, vint.ErrNonMinimal) &&
			errors.Is(e3, vint.ErrOverflow) && e1 != e2 && e2 != e3) && ok

	bad := []byte{0x01, 0x80, 0x00}
	orig := append([]byte(nil), bad...)
	rej, rerr := codec.DecodeSlice(bad)
	ok = report("reject leaves input intact",
		rerr != nil && rej == nil && bytes.Equal(bad, orig)) && ok

	want := codec.EncodeSlice(vals)
	var wg sync.WaitGroup
	same := make(chan bool, 32)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b := codec.EncodeSlice(vals)
			g, e := codec.DecodeSlice(b)
			same <- bytes.Equal(b, want) && e == nil && slices.Equal(g, vals)
		}()
	}
	wg.Wait()
	close(same)
	cc := true
	for s := range same {
		cc = cc && s
	}
	ok = report("concurrent identical", cc) && ok

	if !ok {
		os.Exit(1)
	}
}

func report(name string, ok bool) bool {
	if ok {
		fmt.Println("OK   " + name)
	} else {
		fmt.Println("FAIL " + name)
	}
	return ok
}

func decErr(b []byte) error {
	_, _, err := codec.DecodeInt(b)
	return err
}
