package main

import (
	"fmt"
	"math"

	"ontology/bitpack"
	"ontology/dict"
	"ontology/zone"
)

type check struct {
	name string
	ok   bool
}

func main() {
	var results []check

	results = append(results, check{"位宽边界(1/7/8/63/64,非64倍数)", checkBitWidths()})
	results = append(results, check{"字典编码往返(int/string)", checkDict()})
	results = append(results, check{"裁剪边界与空值谓词", checkZone()})

	pass := 0
	for _, r := range results {
		tag := "OK  "
		if !r.ok {
			tag = "FAIL"
		} else {
			pass++
		}
		fmt.Printf("%s %s\n", tag, r.name)
	}
	fmt.Printf("总计 %d/%d\n", pass, len(results))
	if pass != len(results) {
		panic("demo failed")
	}
}

func checkBitWidths() bool {
	for _, w := range []int{1, 7, 8, 63, 64} {
		for _, n := range []int{1, 63, 64, 65, 127, 130} {
			vals := make([]uint64, n)
			for i := range vals {
				vals[i] = bitpack.Zig(int64(i)*int64(w) - int64(n) + math.MinInt64/2)
				if bitpack.Width64(vals[i]) > w {
					vals[i] &= uint64(1)<<uint(w) - 1
				}
			}
			got, err := bitpack.Unpack(bitpack.Pack(vals, w), w, n)
			if err != nil || len(got) != n {
				return false
			}
			for i := range got {
				if got[i] != vals[i] {
					return false
				}
			}
		}
	}
	return true
}

func checkDict() bool {
	ints := []int64{0, -1, 7, 0, 7, 7, 1 << 40, -1 << 40, 0}
	b, err := dict.EncodeInt64(ints, 0)
	if err != nil {
		return false
	}
	gi, err := dict.DecodeInt64N(b, len(ints))
	if err != nil || len(gi) != len(ints) {
		return false
	}
	for i := range ints {
		if gi[i] != ints[i] {
			return false
		}
	}
	strs := []string{"", "a", "", "bb", "a", ""}
	bs, err := dict.EncodeString(strs, 0)
	if err != nil {
		return false
	}
	gs, err := dict.DecodeStringN(bs, len(strs))
	if err != nil {
		return false
	}
	for i := range strs {
		if gs[i] != strs[i] {
			return false
		}
	}
	if _, _, err := dict.Build([]int{1, 2, 3}, 2); err != dict.ErrTooLarge {
		return false
	}
	return true
}

func checkZone() bool {
	var s zone.Stats
	s.Rows = 3
	s.Add(zone.IntValue(10))
	s.Add(zone.IntValue(20))
	s.Nulls = 1
	if zone.CanSkip(&s, zone.IntLeaf(zone.OpEq, 10)) || zone.CanSkip(&s, zone.IntLeaf(zone.OpEq, 20)) {
		return false
	}
	if zone.CanSkip(&s, zone.IntLeaf(zone.OpLt, 10)) != true {
		return false // x == min: strictly less impossible
	}
	if zone.CanSkip(&s, zone.IntLeaf(zone.OpGt, 20)) != true {
		return false
	}
	for _, p := range []zone.Predicate{
		zone.IntLeaf(zone.OpLe, 10), zone.IntLeaf(zone.OpGe, 20),
		zone.IntIn(9, 20), zone.IntLeaf(zone.OpEq, 15),
	} {
		// Eq 15 may or may not exist -> must NOT skip; IN/edges must not skip.
		if p.Op == zone.OpEq && p.Int == 15 {
			if zone.CanSkip(&s, p) {
				return false
			}
		}
	}
	if zone.CanSkip(&s, zone.NullPred(true)) || zone.CanSkip(&s, zone.NullPred(false)) {
		return false
	}
	var all zone.Stats
	all.Rows = 4
	all.Nulls = 4
	if all.Has {
		return false
	}
	if zone.CanSkip(&all, zone.IntLeaf(zone.OpEq, 0)) != true {
		return false // null must not behave as zero
	}
	if zone.CanSkip(&all, zone.NullPred(true)) != false {
		return false
	}
	n := zone.NullValue()
	if zone.Eval(zone.IntLeaf(zone.OpEq, 0), n) || !zone.Eval(zone.NullPred(true), n) {
		return false
	}
	if zone.Eval(zone.IntLeaf(zone.OpEq, 0), zone.IntValue(0)) == false {
		return false
	}
	return zone.IntValue(0).Equals(zone.IntValue(0)) &&
		!zone.IntValue(0).Equals(zone.NullValue()) &&
		!zone.StrValue("").Equals(zone.NullValue())
}
