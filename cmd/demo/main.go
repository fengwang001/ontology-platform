// Command demo 自校验 Elias gamma 流编码。退出码 0 表示全部判定通过。
package main

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"sync"

	"ontology/api"
	"ontology/bits"
	"ontology/gcode"
)

func ok(name string, pass bool) bool {
	tag := "OK  "
	if !pass {
		tag = "FAIL"
	}
	fmt.Println(tag, name)
	return pass
}

func sameB(a, b []byte) bool  { return string(a) == string(b) }
func sameI(a, b []int64) bool { return fmt.Sprint(a) == fmt.Sprint(b) }

func main() {
	all := true

	enc, _ := gcode.Encode([]int64{1, 2, 3, 5, 8})
	w := bits.NewWriter()
	for _, b := range "1010011001010001000" {
		w.WriteBit(int(b - '0'))
	}
	all = ok("five gamma codes -> A6 51 00", sameB(enc, []byte{0xA6, 0x51, 0x00}) && sameB(w.Bytes(), enc)) && all

	d80, e80 := gcode.Decode([]byte{0x80})
	_, e01 := gcode.Decode([]byte{0x01})
	all = ok("0x80->[1] pad; 0x01->ErrTruncated",
		e80 == nil && sameI(d80, []int64{1}) && errors.Is(e01, gcode.ErrTruncated)) && all

	ovf := []byte{0, 0, 0, 0, 0, 0, 0, 0x01} // 63 个前导 0 后起始 1 → k=63
	_, eOv := gcode.Decode(ovf)
	all = ok("k=63 -> ErrOverflow", errors.Is(eOv, gcode.ErrOverflow)) && all

	dBad, _ := gcode.Decode([]byte{0x48}) // 010010 + 补 0
	all = ok("bad 010010 -> [2,2]", sameI(dBad, []int64{2, 2})) && all

	eEmpty, _ := gcode.Encode([]int64{})
	dEmpty, errEmpty := gcode.Decode([]byte{})
	all = ok("empty -> empty", len(eEmpty) == 0 && errEmpty == nil && len(dEmpty) == 0) && all

	rnd := rand.New(rand.NewSource(1))
	cases := [][]int64{{}, {1}, {1 << 62}, {1, 2, 3, 5, 8}}
	for t := 0; t < 200; t++ {
		v := make([]int64, 1+rnd.Intn(64))
		for i := range v {
			v[i] = 1 + rnd.Int63n(1<<40)
		}
		cases = append(cases, v)
	}
	rtOK := true
	for _, v := range cases {
		b1, err := gcode.Encode(v)
		b2, _ := gcode.Encode(v)
		d, err2 := gcode.Decode(b1)
		if err != nil || err2 != nil || !sameB(b1, b2) || !sameI(d, v) {
			rtOK = false
		}
	}
	all = ok("roundtrip + determinism", rtOK) && all

	chunkOK := true
	for _, v := range cases[1:] {
		b, _ := gcode.Encode(v)
		want, _ := gcode.Decode(b)
		for cut := 0; cut <= len(b) && chunkOK; cut++ {
			sd := gcode.NewStreamDecoder()
			o1, e1 := sd.Feed(b[:cut])
			o2, e2 := sd.Feed(b[cut:])
			if e1 != nil || e2 != nil || sd.End() != nil || !sameI(append(o1, o2...), want) {
				chunkOK = false
			}
		}
	}
	all = ok("chunk-independent streaming", chunkOK) && all

	_, eNP := gcode.Encode([]int64{1, 0})
	_, eNeg := gcode.Encode([]int64{-3})
	dTrunc, eTr := gcode.Decode([]byte{0x01})
	dPref, ePrefTr := gcode.Decode([]byte{0x81}) // [1] 后接未竟码
	dOvf, _ := gcode.Decode(ovf)
	distinct := errors.Is(eNP, gcode.ErrNonPositive) && errors.Is(eNeg, gcode.ErrNonPositive) &&
		errors.Is(eTr, gcode.ErrTruncated) && errors.Is(ePrefTr, gcode.ErrTruncated) &&
		errors.Is(eOv, gcode.ErrOverflow)
	all = ok("3 distinct errors; nil on failure",
		distinct && dTrunc == nil && dPref == nil && dOvf == nil) && all

	bigOK := true
	for _, m := range []int{100, 1000, 10000} {
		v := make([]int64, m+1)
		for i := 0; i < m; i++ {
			v[i] = 1
		}
		v[m] = 1 << 40
		b, _ := gcode.Encode(v)
		d, err := gcode.Decode(b)
		if err != nil || len(d) != m+1 || d[m] != 1<<40 {
			bigOK = false
		}
	}
	all = ok("big-m tail (81-bit, m-independent)", bigOK) && all

	in := []int64{1, 2, 3, 5, 8, 1 << 40, 1 << 62}
	sb, _ := gcode.Encode(in)
	var wg sync.WaitGroup
	var mu sync.Mutex
	concOK := true
	for g := 0; g < 32; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b, e1 := gcode.Encode(in)
			d, e2 := gcode.Decode(b)
			mu.Lock()
			if e1 != nil || e2 != nil || !sameB(b, sb) || !sameI(d, in) {
				concOK = false
			}
			mu.Unlock()
		}()
	}
	wg.Wait()
	all = ok("concurrency; api.SelfCheck", concOK && api.SelfCheck() == nil) && all

	if !all {
		os.Exit(1)
	}
}
