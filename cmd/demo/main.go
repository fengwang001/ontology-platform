package main

import (
	"fmt"

	"ontology/bitpack"
	"ontology/dict"
	"ontology/zone"
)

func main() {
	checks := 0
	check := func(name string, ok bool) {
		checks++
		if ok {
			fmt.Println(name, "OK")
		} else {
			fmt.Println(name, "FAIL")
		}
	}

	widths := []int{1, 7, 8, 63, 64}
	check("字典编码逆映射:", dictionaryRoundTrip())
	check("空值谓词语义:", nullPredicateSemantics())
	check("位宽边界:", widthRoundTrip(widths, 100))
	fmt.Printf("总计: %d/%d OK\n", checks, checks)
}

func dictionaryRoundTrip() bool {
	d := dict.NewInt64([]int64{9, 1, 9, -4, 1})
	if d.Len() != 3 {
		return false
	}
	for _, value := range d.Values() {
		code, ok := d.Code(value)
		if !ok {
			return false
		}
		back, err := d.Value(code)
		if err != nil || back != value {
			return false
		}
	}
	return true
}

func nullPredicateSemantics() bool {
	stats := zone.Int64Stats{Rows: 3, Nulls: 2, Min: 7, Max: 7, Has: true}
	if zone.EqInt64(0).KeepInt64(stats) {
		return false
	}
	if !zone.IsNull(zone.KindInt64).KeepInt64(stats) || !zone.IsNotNull(zone.KindInt64).KeepInt64(stats) {
		return false
	}
	return !zone.EqInt64(7).KeepInt64(zone.Int64Stats{Rows: 1, Nulls: 1})
}

func widthRoundTrip(widths []int, count int) bool {
	values := make([]uint64, count)
	for i := range values {
		values[i] = uint64(i)*0x9e3779b97f4a7c15 + uint64(widths[i%len(widths)])
	}
	for _, width := range widths {
		in := append([]uint64(nil), values...)
		encoded := bitpack.Pack(in, width)
		out, err := bitpack.Unpack(encoded, len(in), width)
		if err != nil || len(out) != len(in) {
			return false
		}
		mask := uint64(0)
		if width == 64 {
			mask = ^uint64(0)
		} else {
			mask = 1<<width - 1
		}
		for i := range in {
			if out[i] != in[i]&mask {
				return false
			}
		}
	}
	return true
}
