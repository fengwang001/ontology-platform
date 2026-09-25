// Package api is the public facade over the Fibonacci (Zeckendorf) codec.
package api

import (
	"errors"

	"ontology/fcode"
)

// Re-exported sentinels: the three failure classes stay mutually distinct.
var (
	ErrTruncated   = fcode.ErrTruncated
	ErrOverflow    = fcode.ErrOverflow
	ErrNotPositive = fcode.ErrNotPositive
)

// Encode packs positive int64 values into a big-endian Fibonacci-code stream.
func Encode(vs []int64) ([]byte, error) { return fcode.Encode(vs) }

// Decode decodes one complete stream; any failure yields a nil slice.
func Decode(p []byte) ([]int64, error) { return fcode.Decode(p) }

// refTable is an independent table F(1)=1,F(2)=2,... up to int64.
func refTable() []int64 {
	t := []int64{1, 2}
	for t[len(t)-1] <= 1<<62 {
		t = append(t, t[len(t)-1]+t[len(t)-2])
	}
	return t
}

// naiveEncode is the independent "greedy Zeckendorf + concat + pack" reference.
func naiveEncode(vs, tab []int64) []byte {
	var bits []bool
	for _, n := range vs {
		var used []int
		for x, i := n, len(tab)-1; x > 0; i-- {
			if tab[i] <= x {
				x, used = x-tab[i], append(used, i)
			}
		}
		w := make([]bool, used[0]+2)
		for _, i := range used {
			w[i] = true
		}
		w[used[0]+1] = true
		bits = append(bits, w...)
	}
	out := make([]byte, (len(bits)+7)/8)
	for i, b := range bits {
		if b {
			out[i/8] |= 1 << (7 - uint(i%8))
		}
	}
	return out
}

func sample(tab []int64) []int64 {
	return []int64{1, 2, 3, 4, 5, 6, 7, 8, 100, 1000, tab[10], tab[30], tab[len(tab)-1]}
}

func same(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// SelfCheck verifies the four invariants over fixed built-in sequences.
func SelfCheck() error {
	tab := refTable()
	var vs []int64
	// 1) byte/element equality with the independent naive reference.
	for _, in := range [][]int64{{}, {1}, {1, 2, 4, 6, 8}, sample(tab)} {
		got, err := fcode.Encode(in)
		if err != nil || string(got) != string(naiveEncode(in, tab)) {
			return errors.New("api: invariant 1 encode failed")
		}
		if back, err := fcode.Decode(got); err != nil || !same(back, in) {
			return errors.New("api: invariant 1 decode failed")
		}
	}
	vs = sample(tab)
	packed, _ := fcode.Encode(vs)
	// 2) chunk boundaries do not affect the decoded sequence.
	var sd fcode.StreamDecoder
	for _, c := range packed {
		if _, err := sd.Feed([]byte{c}); err != nil {
			return err
		}
	}
	if inc, err := sd.End(); err != nil || !same(inc, vs) {
		return errors.New("api: invariant 2 failed")
	}
	// 3) determinism and unique terminating 11 (no earlier adjacent pair).
	a, _ := fcode.Encode(vs)
	b, _ := fcode.Encode(vs)
	if string(a) != string(b) {
		return errors.New("api: invariant 3 determinism failed")
	}
	for _, n := range vs {
		w, _ := fcode.EncodeOne(n)
		if !w[len(w)-1] || !w[len(w)-2] {
			return errors.New("api: invariant 3 ending failed")
		}
		for i := 1; i < len(w)-1; i++ {
			if w[i-1] && w[i] {
				return errors.New("api: invariant 3 unique 11 failed")
			}
		}
	}
	// 4) atomic failure: nil output, sentinel, decoder state stays usable.
	if v, err := fcode.Decode([]byte{0x01}); v != nil || !errors.Is(err, fcode.ErrTruncated) {
		return errors.New("api: invariant 4 truncation failed")
	}
	if _, err := fcode.Encode([]int64{0}); !errors.Is(err, fcode.ErrNotPositive) {
		return errors.New("api: invariant 4 not-positive failed")
	}
	ov := append(make([]byte, 11), 0x0C) // 92 zero bits then 11 at 93-94: k=93 > table
	if v, err := fcode.Decode(ov); v != nil || !errors.Is(err, fcode.ErrOverflow) {
		return errors.New("api: invariant 4 overflow failed")
	}
	var d2 fcode.StreamDecoder
	bad := append([]byte{0xFF}, ov...) // [1,1,1,1] then an overflowing word
	if _, err := d2.Feed(bad); !errors.Is(err, fcode.ErrOverflow) {
		return errors.New("api: invariant 4 stream overflow failed")
	}
	if _, err := d2.Feed([]byte{0xFF}); err != nil { // rejected chunk left no trace
		return err
	}
	if out, err := d2.End(); err != nil || !same(out, []int64{1, 1, 1, 1}) {
		return errors.New("api: invariant 4 state preserved failed")
	}
	return nil
}
