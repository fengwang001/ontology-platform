package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"

	"ontology/api"
	"ontology/sfano"
	"ontology/sfstream"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
	}
	fmt.Println(map[bool]string{true: "OK", false: "FAIL"}[ok], name)
}

func eq(a, b map[byte]string) bool {
	if len(a) != len(b) {
		return false
	}
	for s, c := range a {
		if b[s] != c {
			return false
		}
	}
	return true
}

// altCodes rebuilds a table with a variant cut rule:
// mode 0 = min diff leftmost, 1 = min diff rightmost, 2 = first half.
func altCodes(freq map[byte]int, mode int) map[byte]string {
	type it struct {
		s byte
		f int
	}
	var items []it
	for s, f := range freq {
		items = append(items, it{s, f})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].f != items[j].f {
			return items[i].f > items[j].f
		}
		return items[i].s < items[j].s
	})
	codes := map[byte]string{}
	var rec func(g []it, pre string)
	rec = func(g []it, pre string) {
		if len(g) == 1 {
			codes[g[0].s] = pre
			return
		}
		tot, cut, run, best := 0, 1, 0, 0
		for _, x := range g {
			tot += x.f
		}
		best = tot
		for i := 0; i < len(g)-1; i++ {
			run += g[i].f
			d := 2*run - tot
			if d < 0 {
				d = -d
			}
			if mode == 2 && 2*run >= tot {
				cut = i + 1
				break
			}
			if mode != 2 && (d < best || (mode == 1 && d == best)) {
				best, cut = d, i+1
			}
		}
		rec(g[:cut], pre+"0")
		rec(g[cut:], pre+"1")
	}
	rec(items, "")
	return codes
}

func main() {
	freq := map[byte]int{'A': 5, 'B': 2, 'C': 1, 'D': 1, 'E': 1}
	codes, _ := sfano.Build(freq)
	want := map[byte]string{'A': "0", 'B': "10", 'C': "110", 'D': "1110", 'E': "1111"}
	check("splits+codewords A=0 B=10 C=110 D=1110 E=1111", eq(codes, want))
	c, _ := api.New(freq)
	enc, _ := c.Encode([]byte("AABCCD"))
	check("encode AABCCD -> 00101101101110 -> 0x2D 0xB8", bytes.Equal(enc, []byte{0x2D, 0xB8}))
	dec, _ := c.Decode(enc, 6)
	f := sfstream.New(codes).NewFeeder()
	f.Feed(enc[:1])
	f.Feed(enc[1:])
	fdec, ferr := f.Decode(6)
	check("roundtrip + chunk-agnostic feed", string(dec) == "AABCCD" && ferr == nil && string(fdec) == "AABCCD")
	check("(甲) tie-right: B=100 C=101 D=110 E=111", eq(altCodes(freq, 1),
		map[byte]string{'A': "0", 'B': "100", 'C': "101", 'D': "110", 'E': "111"}))
	check("(乙) first-half wrong: A=00 B=01 C=10 D=11", eq(altCodes(map[byte]int{'A': 6, 'B': 4, 'C': 3, 'D': 1}, 2),
		map[byte]string{'A': "00", 'B': "01", 'C': "10", 'D': "11"}))
	e0, _ := c.Encode([]byte{})
	c1, _ := api.New(map[byte]int{'A': 1})
	e1, _ := c1.Encode([]byte("AA"))
	d1, _ := c1.Decode(e1, 2)
	_, errF := api.New(map[byte]int{'A': 0, 'B': 0})
	check("(丙) empty/single-symbol/invalid-freq", len(e0) == 0 && len(e1) == 0 && string(d1) == "AA" && errors.Is(errF, api.ErrInvalidFreq))
	r1, e1r := c.Encode([]byte("AZ"))
	r2, e2r := c.Decode([]byte{0x2D}, 6)
	r3, e3r := c.Decode([]byte{0x2D, 0xB9}, 6)
	distinct := !errors.Is(api.ErrInvalidFreq, api.ErrTruncated) && !errors.Is(api.ErrUnknownSymbol, api.ErrIllegalPadding)
	check("4 sentinel errors distinct, nil output on reject", distinct && r1 == nil && r2 == nil && r3 == nil &&
		errors.Is(e1r, api.ErrUnknownSymbol) && errors.Is(e2r, api.ErrTruncated) && errors.Is(e3r, api.ErrIllegalPadding))
	check("SelfCheck (4 invariants)", api.SelfCheck() == nil)
	big := map[byte]int{}
	for i := 0; i < 256; i++ {
		big[byte(i)] = 256 - i
	}
	bc, _ := api.New(big)
	bm := bytes.Repeat([]byte{0, 100, 255}, 50)
	be, _ := bc.Encode(bm)
	bd, berr := bc.Decode(be, len(bm))
	check("large-m (256) roundtrip; node bound in sfstream test", berr == nil && bytes.Equal(bd, bm))
	msg := []byte("AABCCDEEDCBA")
	senc, _ := c.Encode(msg)
	res := make(chan bool, 16)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e, _ := c.Encode(msg)
			d, _ := c.Decode(e, len(msg))
			res <- bytes.Equal(e, senc) && string(d) == string(msg)
		}()
	}
	wg.Wait()
	ok := true
	for i := 0; i < 16; i++ {
		ok = ok && <-res
	}
	check("concurrent encode/decode identical", ok)
	if failed {
		os.Exit(1)
	}
}
