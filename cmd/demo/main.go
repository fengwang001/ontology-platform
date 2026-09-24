// Command demo exercises the varint codec end to end.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"os"
	"slices"
	"sync"
	"sync/atomic"

	"ontology/codec"
	"ontology/vint"
	"ontology/zz"
)

func check(name string, ok bool) {
	if !ok {
		fmt.Println("FAIL", name)
		os.Exit(1)
	}
	fmt.Println("OK", name)
}

func main() {
	zig := zz.Encode(0) == 0 && zz.Encode(-1) == 1 && zz.Encode(1) == 2 &&
		zz.Encode(-2) == 3 && zz.Encode(math.MinInt64) == math.MaxUint64
	for _, v := range []int64{math.MinInt64, -1, 0, 1, math.MaxInt64} {
		zig = zig && zz.Decode(zz.Encode(v)) == v
	}
	check("zigzag", zig)

	var buf [10]byte
	over := true
	for _, u := range []uint64{0, 127, 128, 16383, 16384, math.MaxUint64} {
		n := vint.PutUvarint(buf[:], u)
		got, r, err := vint.Uvarint(buf[:n])
		over = over && n <= 10 && r == n && err == nil && got == u
	}
	check("vint-len-read", over)

	e1 := uerr([]byte{0x80})
	e2 := uerr([]byte{0x80, 0x00})
	e3 := uerr([]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x02})
	check("vint-errors", errors.Is(e1, vint.ErrIncomplete) &&
		errors.Is(e2, vint.ErrNonMinimal) && errors.Is(e3, vint.ErrOverflow) &&
		e1 != e2 && e2 != e3 && e1 != e3)

	round := true
	for _, v := range []int64{math.MinInt64, -1, 0, 1, math.MaxInt64} {
		got, err := codec.DecodeInt(codec.EncodeInt(v))
		round = round && err == nil && got == v
	}
	check("roundtrip", round)

	vals := []int64{math.MinInt64, -300, -1, 0, 1, 300, math.MaxInt64}
	enc := codec.EncodeSlice(vals)
	dec, err := codec.DecodeSlice(enc)
	check("slice-roundtrip", err == nil && slices.Equal(dec, vals))

	cut := true
	for i := 0; i < len(enc); i++ {
		part, err := codec.DecodeSlice(enc[:i])
		if err != nil {
			cut = cut && errors.Is(err, codec.ErrIncomplete)
		} else {
			cut = cut && slices.Equal(part, vals[:len(part)])
		}
	}
	check("cut-anywhere", cut)

	bad := append(append([]byte{}, enc...), 0x80)
	saved := bytes.Clone(bad)
	_, err = codec.DecodeSlice(bad)
	check("no-trace", err != nil && bytes.Equal(bad, saved))

	var diff atomic.Bool
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				if !bytes.Equal(codec.EncodeSlice(vals), enc) {
					diff.Store(true)
				}
			}
		}()
	}
	wg.Wait()
	check("concurrent", !diff.Load())
	check("selfcheck", codec.SelfCheck(vals) == nil)
}

func uerr(b []byte) error {
	_, _, err := vint.Uvarint(b)
	return err
}
