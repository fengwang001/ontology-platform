package api_test

import (
	"errors"
	"math/rand"
	"sort"
	"testing"

	"ontology/api"
	"ontology/sfano"
)

var demoFreq = map[byte]int{'A': 5, 'B': 2, 'C': 1, 'D': 1, 'E': 1}

// naiveCodes is the test's independent reference: per-level hand split,
// min |left-right|, ties leftmost.
func naiveCodes(freq map[byte]int) map[byte]string {
	type sym struct {
		b byte
		f int
	}
	var items []sym
	for b, f := range freq {
		items = append(items, sym{b, f})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].f != items[j].f {
			return items[i].f > items[j].f
		}
		return items[i].b < items[j].b
	})
	codes := map[byte]string{}
	var rec func(g []sym, pre string)
	rec = func(g []sym, pre string) {
		if len(g) == 1 {
			codes[g[0].b] = pre
			return
		}
		tot := 0
		for _, x := range g {
			tot += x.f
		}
		best, bestD, left := 1, tot, 0
		for i := 0; i < len(g)-1; i++ {
			left += g[i].f
			d := 2*left - tot
			if d < 0 {
				d = -d
			}
			if d < bestD {
				bestD, best = d, i+1
			}
		}
		rec(g[:best], pre+"0")
		rec(g[best:], pre+"1")
	}
	rec(items, "")
	return codes
}

func freqTables() []map[byte]int {
	tabs := []map[byte]int{demoFreq, {'A': 6, 'B': 4, 'C': 3, 'D': 1}, {'A': 1}, {'x': 7, 'y': 7, 'z': 7}}
	r := rand.New(rand.NewSource(1))
	for i := 0; i < 30; i++ {
		m := map[byte]int{}
		for j := 0; j < 1+r.Intn(40); j++ {
			m[byte(r.Intn(256))] = 1 + r.Intn(100)
		}
		tabs = append(tabs, m)
	}
	return tabs
}

func TestTableMatchesNaive(t *testing.T) {
	for _, freq := range freqTables() {
		got, _ := sfano.Build(freq) // freqTables are all valid
		for s, c := range naiveCodes(freq) {
			if got[s] != c {
				t.Errorf("freq %v sym %q: got %q want %q", freq, s, got[s], c)
			}
		}
	}
}

func TestEncodeMatchesNaive(t *testing.T) {
	r := rand.New(rand.NewSource(2))
	for _, freq := range freqTables() {
		c, _ := api.New(freq)
		codes := naiveCodes(freq)
		syms := make([]byte, 0, len(codes))
		for s := range codes {
			syms = append(syms, s)
		}
		msg := make([]byte, 1+r.Intn(50))
		bits := ""
		for i := range msg {
			msg[i] = syms[r.Intn(len(syms))]
			bits += codes[msg[i]]
		}
		got, err := c.Encode(msg)
		if err != nil {
			t.Fatal(err)
		}
		want := make([]byte, (len(bits)+7)/8) // naive: concat + big-endian pack
		for i := 0; i < len(bits); i++ {
			if bits[i] == '1' {
				want[i/8] |= 1 << (7 - i%8)
			}
		}
		if string(got) != string(want) {
			t.Errorf("freq %v msg %q: got %x want %x", freq, msg, got, want)
		}
		if dec, err := c.Decode(got, len(msg)); err != nil || string(dec) != string(msg) {
			t.Errorf("roundtrip failed: %q err %v", dec, err)
		}
	}
}

func TestFailureAtomic(t *testing.T) {
	for _, f := range []map[byte]int{{}, {'A': 0, 'B': 0}, {'A': -1}} {
		if c, err := api.New(f); c != nil || !errors.Is(err, api.ErrInvalidFreq) {
			t.Errorf("freq %v: got (%v,%v)", f, c, err)
		}
	}
	sents := []error{api.ErrInvalidFreq, api.ErrUnknownSymbol, api.ErrTruncated, api.ErrIllegalPadding}
	for i, a := range sents {
		for j, b := range sents {
			if i != j && errors.Is(a, b) {
				t.Errorf("sentinels %v and %v not distinct", a, b)
			}
		}
	}
	c, _ := api.New(demoFreq)
	good, _ := c.Encode([]byte("AABCCD"))
	runs := []func() ([]byte, error){
		func() ([]byte, error) { return c.Encode([]byte("Z")) },
		func() ([]byte, error) { return c.Decode(good[:1], 6) },
		func() ([]byte, error) { return c.Decode([]byte{0x2D, 0xB9}, 6) },
	}
	wants := []error{api.ErrUnknownSymbol, api.ErrTruncated, api.ErrIllegalPadding}
	for i, run := range runs {
		if out, err := run(); out != nil || !errors.Is(err, wants[i]) {
			t.Errorf("case %d: got (%v,%v), want nil,%v", i, out, err, wants[i])
		}
	}
	if dec, err := c.Decode(good, 6); err != nil || string(dec) != "AABCCD" {
		t.Error("codec state corrupted after failures")
	}
}
