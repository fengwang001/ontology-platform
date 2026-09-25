// Command demo exercises the Fibonacci (Zeckendorf) codec and prints OK/FAIL.
package main

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	"ontology/api"
	"ontology/fcode"
	"ontology/fibseq"
)

var fails int

func report(tag string, ok bool) {
	if !ok {
		fails++
	}
	fmt.Println(map[bool]string{true: "OK ", false: "FAIL "}[ok] + tag)
}

func bitsOf(p []byte) []bool {
	bits := make([]bool, 0, len(p)*8)
	for _, b := range p {
		for j := 7; j >= 0; j-- {
			bits = append(bits, b&(1<<j) != 0)
		}
	}
	return bits
}

func bitString(bs []bool) string {
	var b strings.Builder
	for _, x := range bs {
		if x {
			b.WriteByte('1')
		} else {
			b.WriteByte('0')
		}
	}
	return b.String()
}

func main() {
	// 1) five words of [1,2,4,6,8] and the packed stream DD CC 30.
	ns := []int64{1, 2, 4, 6, 8}
	wantBits := []string{"11", "011", "1011", "10011", "000011"}
	ok := true
	for i, n := range ns {
		bs, err := fcode.EncodeOne(n)
		ok = ok && err == nil && bitString(bs) == wantBits[i]
	}
	packed, err := fcode.Encode(ns)
	report("five codes + bytes DD CC 30", ok && err == nil && fmt.Sprintf("% X", packed) == "DD CC 30")

	// 2) 0x03 -> 21; 0x01 -> ErrTruncated.
	v3, e3 := fcode.Decode([]byte{0x03})
	_, e1 := fcode.Decode([]byte{0x01})
	report("0x03 -> 21, 0x01 -> truncated", len(v3) == 1 && v3[0] == 21 && errors.Is(e1, api.ErrTruncated) && e3 == nil)

	// 3) wrong F-table code of 4 (01011) is read by a correct decoder as 7.
	wv, _, werr := fcode.DecodeOne([]bool{false, true, false, true, true})
	report("wrong-table code of 4 decodes to 7", werr == nil && wv == 7)

	// 4) empty input -> empty non-nil bytes.
	enc, err := fcode.Encode(nil)
	report("empty input -> empty bytes", err == nil && len(enc) == 0)

	// 5) round trip across a range.
	vs := []int64{1, 2, 3, 4, 5, 8, 13, 21, 100, 1000, 1 << 40}
	b, err := fcode.Encode(vs)
	back, err2 := fcode.Decode(b)
	report("round trip", err == nil && err2 == nil && fmt.Sprint(back) == fmt.Sprint(vs))

	// 6) arbitrary chunk boundaries give the same sequence.
	var sd fcode.StreamDecoder
	segOK := true
	for _, c := range b {
		_, e := sd.Feed([]byte{c})
		segOK = segOK && e == nil
	}
	inc, errS := sd.End()
	report("segmentation independent", segOK && errS == nil && fmt.Sprint(inc) == fmt.Sprint(vs))

	// 7) SelfCheck; three sentinels pairwise distinct; failure yields nil.
	_, ePos := api.Encode([]int64{0})
	distinct := errors.Is(ePos, api.ErrNotPositive) && errors.Is(e1, api.ErrTruncated) &&
		api.ErrNotPositive != api.ErrTruncated && api.ErrTruncated != api.ErrOverflow &&
		api.ErrNotPositive != api.ErrOverflow
	partial, _ := api.Decode([]byte{0x01})
	report("self-check, 3 distinct sentinels, no partial", api.SelfCheck() == nil && distinct && partial == nil)

	// 8) last word's inspected length is its code length, independent of m.
	ms := []int{100, 1000, 10000}
	lens := make([]int, len(ms))
	for i, m := range ms {
		in := make([]int64, m+1)
		for j := range in {
			in[j] = 1
		}
		in[m] = fibseq.Table(1 << 62)[46] // F(47)
		p, _ := fcode.Encode(in)
		bb, off := bitsOf(p), 0
		for j := 0; j < m; j++ {
			_, used, e := fcode.DecodeOne(bb[off:])
			if e != nil {
				lens[i] = -1
				break
			}
			off += used
		}
		_, used, e := fcode.DecodeOne(bb[off:])
		if e == nil {
			lens[i] = used
		}
	}
	report("tail check bits independent of m", lens[0] > 0 && lens[0] == lens[1] && lens[1] == lens[2])

	// 9) N goroutines encode+decode; results match the serial run byte-wise.
	const n = 32
	var wg sync.WaitGroup
	results := make([][]int64, n)
	for g := 0; g < n; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			p, e := api.Encode(vs)
			if e != nil || string(p) != string(b) {
				return
			}
			results[g], _ = api.Decode(p)
		}(g)
	}
	wg.Wait()
	concOK := true
	for _, r := range results {
		concOK = concOK && fmt.Sprint(r) == fmt.Sprint(vs)
	}
	report("concurrent round trip matches serial", concOK)

	if fails > 0 {
		fmt.Println("FAIL")
	}
}
