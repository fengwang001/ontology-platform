package fcode

import "errors"
import "reflect"
import "slices"
import "sync"
import "testing"

var ovBytes = func() []byte { b := make([]byte, 12); b[11] = 0x0C; return b }()

func chk(t *testing.T, ok bool, f string, a ...any) {
	if !ok {
		t.Fatalf(f, a...)
	}
}
func bitAt(p []byte, i int) bool { return p[i/8]&(1<<(7-uint(i%8))) != 0 }
func refTab() []int64 {
	t := []int64{1, 2}
	for t[len(t)-1] <= 1<<62 {
		t = append(t, t[len(t)-1]+t[len(t)-2])
	}
	return t
}

// naivePack is an independent hand-written greedy-Zeckendorf bit packer.
func naivePack(vs, f []int64) []byte {
	var bits []bool
	for _, n := range vs {
		k := len(f) - 1
		for f[k] > n {
			k--
		}
		x, w := n, make([]bool, k+2)
		for i := k; i >= 0; i-- {
			if f[i] <= x {
				x, w[i] = x-f[i], true
			}
		}
		bits = append(append(bits, w[:k+1]...), true)
	}
	o := make([]byte, (len(bits)+7)/8)
	for i, x := range bits {
		if x {
			o[i/8] |= 1 << (7 - uint(i%8))
		}
	}
	return o
}

// naiveVals hand-scans each word's first 11 and sums its F values.
func naiveVals(p []byte, f []int64) []int64 {
	v := []int64{}
	for i, s := 1, 0; i < len(p)*8; i++ {
		if bitAt(p, i-1) && bitAt(p, i) {
			n := int64(0)
			for j := s; j < i; j++ {
				if bitAt(p, j) {
					n += f[j-s]
				}
			}
			v, s, i = append(v, n), i+1, i+1
		}
	}
	return v
}
func TestNaiveReference(t *testing.T) {
	f := refTab()
	v1 := []int64{1, 2, 3, 4, 5, 6, 7, 8, 21, 100, 1000, f[46], f[len(f)-1]}
	cases := [][]int64{{}, {1}, {1, 2, 4, 6, 8}, {1, 1, 1, 1}, v1}
	for _, in := range cases {
		got, e1 := Encode(in)
		back, e2 := Decode(got)
		chk(t, e1 == nil && e2 == nil && reflect.DeepEqual(got, naivePack(in, f)) && reflect.DeepEqual(back, in) && reflect.DeepEqual(back, naiveVals(got, f)), "mismatch %v", in)
	}
	b, _ := Encode([]int64{1, 2, 4, 6, 8})
	chk(t, b[0] == 0xDD && b[1] == 0xCC && b[2] == 0x30, "DD CC 30 % X", b)
}
func TestFeedSegmentation(t *testing.T) {
	vs := append([]int64{1, 2, 4, 6, 8, 21, 1000}, refTab()[30], refTab()[46])
	in, _ := Encode(vs)
	for _, sz := range []int{1, 2, 3, 7, len(in)} {
		sd := StreamDecoder{}
		for off := 0; off < len(in); off += sz {
			sd.Feed(in[off:min(off+sz, len(in))])
		}
		got, e := sd.End()
		chk(t, e == nil && reflect.DeepEqual(got, vs), "sz %d %v", sz, got)
	}
}
func TestDeterminismAndCodes(t *testing.T) {
	vs := append(refTab(), 4, 6, 7, 100, 1000)
	a, _ := Encode(vs)
	b, _ := Encode(vs)
	chk(t, string(a) == string(b), "not deterministic")
	for _, n := range vs {
		w, _ := EncodeOne(n)
		ok := w[len(w)-1] && w[len(w)-2]
		for i := 1; i < len(w)-1; i++ {
			ok = ok && !(w[i-1] && w[i])
		}
		chk(t, ok, "%d code %v", n, w)
	}
}
func TestFailureLeavesNoState(t *testing.T) {
	p1, e1 := Decode([]byte{0x01})
	p2, e2 := Decode(ovBytes)
	chk(t, p1 == nil && e1 != nil && p2 == nil && e2 != nil, "partial output")
	sd := StreamDecoder{}
	_, e0 := sd.Feed([]byte{0xFF})
	_, e1b := sd.Feed(ovBytes)
	_, e2b := sd.Feed([]byte{0xFF})
	out, _ := sd.End()
	want := slices.Repeat([]int64{1}, 8)
	chk(t, e0 == nil && errors.Is(e1b, ErrOverflow) && e2b == nil && reflect.DeepEqual(out, want), "state %v %v", out, e1b)
}
func TestSentinelErrors(t *testing.T) {
	_, en := Encode([]int64{-1})
	_, ez := Encode([]int64{0})
	_, et := Decode([]byte{0x01})
	_, eo := Decode(ovBytes)
	chk(t, errors.Is(en, ErrNotPositive) && errors.Is(ez, ErrNotPositive) && errors.Is(et, ErrTruncated) && errors.Is(eo, ErrOverflow) &&
		ErrTruncated != ErrOverflow && ErrTruncated != ErrNotPositive && ErrOverflow != ErrNotPositive, "sentinels")
}
func TestTailCheckBitsIndependentOfM(t *testing.T) {
	big := refTab()[46]
	w, _ := EncodeOne(big)
	for _, m := range []int{100, 1000, 10000} {
		in := append(slices.Repeat([]int64{1}, m), big)
		p, _ := Encode(in)
		sd := StreamDecoder{}
		_, e := sd.Feed(p)
		chk(t, e == nil && sd.lastCheckBits == len(w), "m=%d %d!=%d", m, sd.lastCheckBits, len(w))
	}
}
func TestConcurrentRoundTrip(t *testing.T) {
	vs := append([]int64{1, 2, 4, 6, 8, 21, 1000}, refTab()[30], refTab()[46])
	serial, _ := Encode(vs)
	var wg sync.WaitGroup
	for g := 0; g < 32; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p, e := Encode(vs)
			back, e2 := Decode(p)
			chk(t, e == nil && e2 == nil && string(p) == string(serial) && reflect.DeepEqual(back, vs), "race")
		}()
	}
	wg.Wait()
}
