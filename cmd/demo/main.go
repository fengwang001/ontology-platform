package main

import (
	"errors"
	"fmt"
	"math"
	"sync"

	"ontology/codec"
	"ontology/vint"
)

func main() {
	c := codec.New()
	report("roundtrip boundary values", func() bool { return c.SelfCheck() == nil })
	report("len<=10 and exact byte count", func() bool {
		dec := vint.NewDecoder()
		for _, u := range []uint64{0, 127, 128, 16383, 16384, math.MaxUint64} {
			b := vint.PutUvarint(nil, u)
			if len(b) > 10 {
				return false
			}
			v, n, err := dec.Uvarint(b)
			if err != nil || v != u || n != len(b) {
				return false
			}
		}
		return true
	})
	vals := []int64{0, -1, 1, -2, 127, -128, math.MinInt64, math.MaxInt64}
	whole := c.EncodeSlice(vals)
	report("slice roundtrip", func() bool { got, e := c.DecodeSlice(whole); return e == nil && eq(got, vals) })
	report("cut at any byte", func() bool {
		for cut := 1; cut < len(whole); cut++ {
			s := c.NewStream()
			if _, e := s.Feed(whole[:cut]); !errors.Is(e, codec.ErrIncomplete) {
				return false
			}
			got, e := s.Feed(whole[cut:])
			if e != nil || !eq(got, vals) {
				return false
			}
			if _, e := c.DecodeSlice(whole[cut:]); e == nil {
				return false
			}
		}
		return true
	})
	report("three distinct decode errors", func() bool {
		_, ei := c.DecodeInt([]byte{0x80})
		_, en := c.DecodeInt([]byte{0x80, 0x00})
		_, eo := c.DecodeInt([]byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x02})
		return errors.Is(ei, codec.ErrIncomplete) && errors.Is(en, codec.ErrCorrupt) &&
			errors.Is(eo, codec.ErrOverflow) && ei != en && en != eo && ei != eo
	})
	report("rejected input untouched", func() bool {
		bad := append(append([]byte{}, whole...), 0x80)
		snap := append([]byte{}, bad...)
		_, _ = c.DecodeSlice(bad)
		return eq64(bad, snap)
	})
	report("concurrent identical bytes", func() bool {
		const n = 16
		var wg sync.WaitGroup
		ok := make(chan bool, n)
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				b := c.EncodeSlice(vals)
				got, err := c.DecodeSlice(b)
				ok <- eq64(b, whole) && err == nil && eq(got, vals)
			}()
		}
		wg.Wait()
		close(ok)
		for k := range ok {
			if !k {
				return false
			}
		}
		return true
	})
}

func report(name string, ok func() bool) {
	res := "OK  "
	if !ok() {
		res = "FAIL"
	}
	fmt.Println(res, name)
}

func eq(a, b []int64) bool {
	return len(a) == len(b) && (len(a) == 0 || a[0] == b[0] && eq(a[1:], b[1:]))
}

func eq64(a, b []byte) bool {
	return len(a) == len(b) && (len(a) == 0 || a[0] == b[0] && eq64(a[1:], b[1:]))
}
