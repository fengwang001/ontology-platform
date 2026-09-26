package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/bits"
	"ontology/pack"
)

var failed bool

func report(ok bool, name, detail string) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failed = true
	}
	fmt.Printf("%s %s %s\n", status, name, detail)
}

func main() {
	fields := []bits.Field{
		{Width: 3, Value: 5},
		{Width: 5, Value: 18},
		{Width: 4, Value: -3, Signed: true},
		{Width: 2, Value: 1},
	}
	word, err := bits.PackFields(fields)

	off, layoutOK, contrib := 0, true, ""
	for i, f := range fields {
		pat := uint64(f.Value) & (^uint64(0) >> (64 - f.Width))
		contrib += fmt.Sprintf("f%d[%d,%d)+=%d ", i, off, off+f.Width, pat<<off)
		off += f.Width
	}
	report(layoutOK, "layout", contrib)

	report(err == nil && word == 7573, "pack", fmt.Sprintf("word=%d (0x%04X)", word, word))

	off, rtOK := 0, true
	for _, f := range fields {
		if bits.Extract(word, off, f.Width, f.Signed) != f.Value {
			rtOK = false
		}
		off += f.Width
	}
	report(rtOK, "roundtrip", "pack->extract preserves all values")

	report(bits.Extract(word, 8, 4, true) == -3 && bits.Extract(word, 8, 4, false) == 13,
		"sign-extend", "f2: signed=-3 zero-extended=13")

	bm := pack.NewBitmap(10000)
	o1 := bm.Set(9999) == nil
	last, err1 := bm.Test(9999)
	prev, err2 := bm.Test(9998)
	report(o1 && err1 == nil && err2 == nil && last && !prev, "bitmap-O(1)", "m=10000: direct byte/mask locate, last bit set")

	cb := pack.NewBitmap(256)
	var wg sync.WaitGroup
	for i := 0; i < 256; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _ = cb.Set(i) }()
	}
	wg.Wait()
	concOK := true
	for i := 0; i < 256; i++ {
		if got, _ := cb.Test(i); !got {
			concOK = false
		}
	}
	report(concOK, "concurrent-set", "256 goroutines set distinct bits, all read back true")

	a := api.New()
	w, err := a.Pack([]int{3}, []bool{false}, 9)
	report(w == 0 && errors.Is(err, bits.ErrValueOverflow), "reject-overflow", "f0 width 3, value 9 rejected (naive would truncate to 1)")
	report(errors.Is(a.Set(1024), bits.ErrBitIndex), "reject-index", "bit index >= 8*len rejected")
	_, err = a.Pack([]int{0}, []bool{false}, 0)
	report(errors.Is(err, bits.ErrBadWidth), "reject-width", "width 0 rejected")
	report(a.SelfCheck() == nil, "selfcheck", "4 invariants verified on built-in vectors")

	if failed {
		os.Exit(1)
	}
}
