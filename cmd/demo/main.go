package main

import (
	"fmt"
	"math"

	"ontology/bitpack"
	"ontology/dict"
	"ontology/zone"
)

// check 表驱动：每实现一个包，就往 checks 里补一行判定。
var checks []checkItem

type checkItem struct {
	name string
	fn   func() error
}

func init() {
	checks = append(checks, checkItem{
		name: "位宽边界 1/7/8/63/64 往返",
		fn: func() error {
			for _, w := range []uint8{1, 7, 8, 63, 64} {
				var max uint64 = math.MaxUint64
				if w < 64 {
					max = 1<<w - 1
				}
				vals := []uint64{0, max, max >> 1, 0}
				data, err := bitpack.Pack(vals, w)
				if err != nil {
					return err
				}
				back, err := bitpack.Unpack(data, len(vals), w)
				if err != nil {
					return err
				}
				for i, v := range vals {
					if back[i] != v {
						return fmt.Errorf("w=%d idx=%d", w, i)
					}
				}
			}
			return nil
		},
	})
	checks = append(checks, checkItem{
		name: "三态可判定：NULL/0/空串",
		fn: func() error {
			m, codes, err := dict.Build([]int64{0, 0, 5}, 0)
			if err != nil {
				return err
			}
			back, err := m.Decode(codes)
			if err != nil || back[0] != 0 {
				return fmt.Errorf("0 value: %v", err)
			}
			if _, ok := m.Code(999); ok {
				return fmt.Errorf("absent looks present")
			}
			sm, _, err := dict.Build([]string{"", "x"}, 0)
			if err != nil {
				return err
			}
			if c, ok := sm.Code(""); !ok || c != 0 {
				return fmt.Errorf("empty string: %d,%v", c, ok)
			}
			return nil
		},
	})
	checks = append(checks, checkItem{
		name: "空值谓词语义：NULL 只被 IS NULL 命中",
		fn: func() error {
			f := zone.Filter{{Op: zone.OpEq, V: 0}}
			if f.Match(0, false) || !f.Match(0, true) {
				return fmt.Errorf("eq null/present wrong")
			}
			isNull := zone.Filter{{Op: zone.OpIsNull}}
			if !isNull.Match(0, false) || isNull.Match(0, true) {
				return fmt.Errorf("is null wrong")
			}
			s := zone.FromValues([]int64{5, 6}, []uint64{0b10})
			if s.Min != 6 || s.Max != 6 || s.NullCount != 1 {
				return fmt.Errorf("null excluded from stats: %+v", s)
			}
			allNull := zone.FromValues([]int64{1}, []uint64{0})
			if allNull.HasMin || (zone.Filter{{Op: zone.OpEq, V: 1}}).Keeps(allNull) {
				return fmt.Errorf("all-null group handling wrong")
			}
			return nil
		},
	})
}

func main() {
	ok := 0
	for _, c := range checks {
		if err := c.fn(); err != nil {
			fmt.Printf("FAIL %s: %v\n", c.name, err)
				continue
		}
		fmt.Printf("OK   %s\n", c.name)
		ok++
	}
	fmt.Printf("总计 %d/%d\n", ok, len(checks))
	if ok != len(checks) {
		panic("demo failed")
	}
}
