// Command demo runs end-to-end self-checks for the columnar segment.
package main

import (
	"fmt"

	"ontology/bitpack"
	"ontology/dict"
	"ontology/zone"
)

type check struct {
	name string
	ok   bool
}

func main() {
	checks := []check{
		{"编码往返无损-位打包(1/7/8/63/64)", bitpackOK()},
		{"编码往返无损-字典(空串为合法成员)", dictOK()},
		{"三态可判定(NULL/0/空串)", triStateOK()},
		{"空值谓词: 只命中IS NULL", nullPredOK()},
		{"穷举对照-裁剪不误杀", pruningOK()},
	}

	pass := 0
	for _, c := range checks {
		if c.ok {
			pass++
			fmt.Printf("OK   %s\n", c.name)
		} else {
			fmt.Printf("FAIL %s\n", c.name)
		}
	}
	fmt.Printf("TOTAL %d/%d\n", pass, len(checks))
	if pass != len(checks) {
		panic("demo checks failed")
	}
}

func bitpackOK() bool {
	for _, width := range []uint{1, 7, 8, 63, 64} {
		vals := make([]uint64, 130)
		for i := range vals {
			mask := ^uint64(0)
			if width < 64 {
				mask = (uint64(1) << width) - 1
			}
			vals[i] = (uint64(i)*0x123456789ABCDEF + 7) & mask
		}
		buf, err := bitpack.Pack(vals, width)
		if err != nil {
			return false
		}
		got, err := bitpack.Unpack(buf, len(vals), width)
		if err != nil {
			return false
		}
		for i := range vals {
			if got[i] != vals[i] {
				return false
			}
		}
	}
	if bitpack.UnZigZag(bitpack.ZigZag(-1<<63)) != -1<<63 {
		return false
	}
	return true
}

func dictOK() bool {
	vals := []int64{3, 3, -9, 3, -9, 1<<63 - 1, -1 << 63, 0}
	d := dict.NewInt(vals)
	back, err := d.Decode(d.Encode(vals))
	if err != nil || d.Cardinality() != 5 {
		return false
	}
	for i := range vals {
		if back[i] != vals[i] {
			return false
		}
	}
	strs := []string{"", "x", "", ""}
	ds := dict.NewStr(strs)
	sback, err := ds.Decode(ds.Encode(strs))
	if err != nil || ds.Cardinality() != 2 || ds.Code("") != 0 {
		return false
	}
	return sback[0] == "" && sback[1] == "x"
}

func triStateOK() bool {
	n, z, e := zone.NullVal(), zone.IntVal(0), zone.StrVal("")
	return n.IsNull() && !z.IsNull() && !e.IsNull() &&
		n.Kind != z.Kind && z.Kind != e.Kind && n.Kind != e.Kind
}

func nullPredOK() bool {
	numerics := []zone.Predicate{zone.Eq(0), zone.Lt(1), zone.Le(1), zone.Gt(-1),
		zone.Ge(-1), zone.In([]int64{0}), zone.IsNotNull()}
	for _, p := range numerics {
		if zone.And(p).Match(zone.NullVal()) {
			return false
		}
	}
	return zone.And(zone.IsNull()).Match(zone.NullVal()) &&
		!zone.And(zone.IsNull()).Match(zone.IntVal(0))
}

func pruningOK() bool {
	domain := []int64{-2, -1, 0, 1, 2}
	preds := []zone.Predicate{zone.Eq(0), zone.Eq(3), zone.Lt(-2), zone.Gt(2),
		zone.Le(-2), zone.Ge(2), zone.In([]int64{-2, 2}), zone.In([]int64{9})}
	for mask := 1; mask < 1<<len(domain); mask++ {
		var vals []zone.Val
		var raw []int64
		for i, x := range domain {
			if mask&(1<<i) != 0 {
				vals = append(vals, zone.IntVal(x))
				raw = append(raw, x)
			}
		}
		s := zone.Build(vals)
		for _, p := range preds {
			anyHit := false
			for _, x := range raw {
				anyHit = anyHit || zone.And(p).Match(zone.IntVal(x))
			}
			if anyHit && !p.CouldHit(s) {
				return false
			}
		}
	}
	return true
}
