// Command demo verifies the Golomb/Rice coding packages with OK/FAIL lines.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/golomb"
	"ontology/gstream"
)

var failed bool

func check(name string, ok bool) {
	status := "OK  "
	if !ok {
		status = "FAIL"
		failed = true
	}
	fmt.Println(status + name)
}

type byteBits struct { // MSB-first BitReader over a byte slice
	p   []byte
	pos int
}

func (r *byteBits) ReadBit() (int, error) {
	if r.pos >= 8*len(r.p) {
		return 0, golomb.ErrTruncated
	}
	b := int((r.p[r.pos>>3] >> (7 - uint(r.pos&7))) & 1)
	r.pos++
	return b, nil
}

// bitsForLast decodes k values from p and returns bits the last code examined.
func bitsForLast(c *golomb.Codec, p []byte, k int) int {
	r := &byteBits{p: p}
	n := 0
	for i := 0; i < k; i++ {
		start := r.pos
		c.DecodeOne(r)
		n = r.pos - start
	}
	return n
}

func main() {
	c5, err := golomb.New(5)
	rems := make([]string, 5)
	for r := int64(0); r < 5; r++ {
		w, _ := c5.EncodeOne(r) // q=0: word is "0" + remainder
		rems[r] = w[1:]
	}
	check("M=5 remainders 00 01 10 110 111", err == nil &&
		rems[0] == "00" && rems[1] == "01" && rems[2] == "10" && rems[3] == "110" && rems[4] == "111")
	check("(甲) fixed-3bit mistake: n=2 -> 0010 (correct 010)",
		"0"+"010" == "0010" && rems[2] == "10")
	// (乙) r=4 written raw as "0100" (padded to 0x40); decoder reads 2 bits 10=2<3.
	got, derr := c5.DecodeOne(&byteBits{p: []byte{0x40}})
	check("(乙) missing +lim: n=4 -> 0100, misdecodes to 2", derr == nil && got == 2)

	seq := []int64{0, 2, 4, 5, 9}
	wantWords := []string{"000", "010", "0111", "1000", "10111"}
	wordsOK := true
	for i, n := range seq {
		w, _ := c5.EncodeOne(n)
		wordsOK = wordsOK && w == wantWords[i]
	}
	stream, _ := gstream.Encode(5, seq)
	check("five codewords + bytes 09 E2 E0", wordsOK &&
		len(stream) == 3 && stream[0] == 0x09 && stream[1] == 0xE2 && stream[2] == 0xE0)

	empty, _ := gstream.Encode(5, nil)
	one, _ := gstream.Encode(5, []int64{0})
	back, _ := gstream.Decode(5, one)
	check("(丙) Encode([])=empty; Encode([0])=0x00", len(empty) == 0 &&
		len(one) == 1 && one[0] == 0x00 && len(back) == 1 && back[0] == 0)

	rt, _ := gstream.Decode(5, stream)
	chunkOK := true
	for size := 1; size <= 3; size++ {
		d, _ := gstream.NewDecoder(5)
		for i := 0; i < len(stream); i += size {
			d.Feed(stream[i:min(i+size, len(stream))])
		}
		g, _ := d.Decode()
		chunkOK = chunkOK && fmt.Sprint(g) == fmt.Sprint(seq)
	}
	check("roundtrip + chunk-independence", fmt.Sprint(rt) == fmt.Sprint(seq) && chunkOK)
	check("SelfCheck (4 invariants)", api.SelfCheck() == nil)

	_, e1 := api.New(0)
	c, _ := api.New(5)
	_, e2 := c.Encode([]int64{3, -1})
	trunc, e3 := c.Decode([]byte{0xFF})
	after, _ := c.Encode([]int64{9})
	check("3 distinct errors; nil on truncation; usable after",
		errors.Is(e1, api.ErrInvalidParam) && errors.Is(e2, api.ErrNegative) &&
			errors.Is(e3, api.ErrTruncated) && trunc == nil &&
			!errors.Is(e1, e2) && !errors.Is(e2, e3) && len(after) > 0)

	bitsOK, prev := true, -1
	for _, m := range []int{100, 1000, 10000} {
		vs := make([]int64, m+1)
		vs[m] = 1 << 20
		p, _ := gstream.Encode(5, vs)
		n := bitsForLast(c5, p, m+1)
		if prev >= 0 && n != prev {
			bitsOK = false
		}
		prev = n
	}
	check("bits for large N independent of m", bitsOK)

	want, _ := c.Encode(seq)
	var wg sync.WaitGroup
	raceOK := true
	var mu sync.Mutex
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			enc, err1 := c.Encode(seq)
			dec, err2 := c.Decode(enc)
			mu.Lock()
			raceOK = raceOK && err1 == nil && err2 == nil &&
				bytes.Equal(enc, want) && fmt.Sprint(dec) == fmt.Sprint(seq)
			mu.Unlock()
		}()
	}
	wg.Wait()
	check("concurrent encode/decode identical", raceOK)

	if failed {
		os.Exit(1)
	}
}
