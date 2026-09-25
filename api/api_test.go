package api_test

import (
	"bytes"
	"errors"
	"maps"
	"math/rand"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/api"
)

var specFreq = map[byte]int{'A': 5, 'B': 2, 'C': 1, 'D': 1, 'E': 1}

func freqTables() []map[byte]int {
	tabs := []map[byte]int{specFreq, {'A': 1}, {'A': 1, 'B': 1}}
	r := rand.New(rand.NewSource(3))
	for i := 0; i < 3; i++ {
		m := map[byte]int{}
		for j := 0; j < 2+r.Intn(30); j++ {
			m[byte(r.Intn(256))] = 1 + r.Intn(100)
		}
		tabs = append(tabs, m)
	}
	return tabs
}
func TestRoundTrip(t *testing.T) {
	r := rand.New(rand.NewSource(4))
	for _, f := range freqTables() {
		c, _ := api.New(f)
		alphabet := slices.Collect(maps.Keys(f))
		for k := 0; k < 10; k++ {
			m := make([]byte, r.Intn(60))
			for i := range m {
				m[i] = alphabet[r.Intn(len(alphabet))]
			}
			enc, _ := c.Encode(m)
			if dec, err := c.Decode(enc, len(m)); err != nil || !bytes.Equal(dec, m) {
				t.Fatalf("freq %v: roundtrip mismatch", f)
			}
		}
	}
	if err := api.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
func TestFeedChunking(t *testing.T) {
	c, _ := api.New(specFreq)
	msg := []byte("AABCCDEABCDE")
	enc, _ := c.Encode(msg)
	for size := 1; size <= len(enc); size++ { // 任意切块喂入，结果与一次性 Decode 一致
		st := c.NewStream()
		for i := 0; i < len(enc); i += size {
			st.Feed(enc[i:min(i+size, len(enc))])
		}
		if got, err := st.Decode(len(msg)); err != nil || !bytes.Equal(got, msg) {
			t.Fatalf("chunk %d: mismatch", size)
		}
	}
}
func TestFaultInjection(t *testing.T) {
	c, _ := api.New(specFreq)
	ign := func(_ any, err error) error { return err }
	cases := []struct {
		name string
		got  error
		want error
	}{
		{"未知符号", ign(c.Encode([]byte("Z"))), api.ErrUnknownSymbol},
		{"空频次表", ign(api.New(map[byte]int{})), api.ErrInvalidFreq},
		{"负频次", ign(api.New(map[byte]int{'A': -1})), api.ErrInvalidFreq},
		{"位流不足", ign(c.Decode([]byte{0x25}, 6)), api.ErrTruncated},
		{"填充非零", ign(c.Decode([]byte{0x25, 0xB9}, 6)), api.ErrIllegalPadding},
		{"多出整字节", ign(c.Decode([]byte{0x25, 0xB8, 0}, 6)), api.ErrIllegalPadding},
	}
	for _, tc := range cases {
		if !errors.Is(tc.got, tc.want) {
			t.Fatalf("%s: got %v want %v", tc.name, tc.got, tc.want)
		}
	}
	sents := []error{api.ErrInvalidFreq, api.ErrUnknownSymbol, api.ErrTruncated, api.ErrIllegalPadding}
	for i, a := range sents {
		for _, b := range sents[i+1:] {
			if errors.Is(a, b) || errors.Is(b, a) {
				t.Fatalf("sentinels not distinct: %v / %v", a, b)
			}
		}
	}
}
func TestFailureAtomicity(t *testing.T) {
	c, _ := api.New(specFreq)
	before, _ := c.Encode([]byte("AABCCD"))
	out1, e1 := c.Encode([]byte("Z"))
	out2, e2 := c.Decode([]byte{0x25}, 6)
	bad, e3 := api.New(map[byte]int{})
	if out1 != nil || e1 == nil || out2 != nil || e2 == nil || bad != nil || e3 == nil {
		t.Fatal("failure returned partial output")
	}
	if after, _ := c.Encode([]byte("AABCCD")); !bytes.Equal(before, after) {
		t.Fatal("state changed after failures")
	}
}
func TestDeterministicPrefixFree(t *testing.T) {
	for _, f := range freqTables() {
		c1, _ := api.New(f)
		c2, _ := api.New(f)
		var codes []string
		for s := range f {
			a, _ := c1.CodeOf(s)
			if b, _ := c2.CodeOf(s); a != b {
				t.Fatalf("nondeterministic code for %q", s)
			}
			codes = append(codes, a)
		}
		for i, a := range codes {
			for _, b := range codes[i+1:] {
				if strings.HasPrefix(a, b) || strings.HasPrefix(b, a) {
					t.Fatalf("prefix conflict: %q vs %q", a, b)
				}
			}
		}
	}
}
func TestConcurrentRoundTrip(t *testing.T) {
	c, _ := api.New(specFreq)
	msg := []byte("AABCCDEABCDE")
	enc, _ := c.Encode(msg)
	var bad atomic.Bool
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				e, _ := c.Encode(msg)
				if d, _ := c.Decode(e, len(msg)); !bytes.Equal(e, enc) || !bytes.Equal(d, msg) {
					bad.Store(true)
				}
			}
		}()
	}
	wg.Wait()
	if bad.Load() {
		t.Fatal("concurrent results differ from serial")
	}
}
