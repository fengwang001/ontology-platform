package main

import (
	"fmt"

	"ontology/bitpack"
	"ontology/dict"
	"ontology/zone"
)

func report(name string, ok bool, pass *int) {
	if ok {
		fmt.Println("OK  ", name)
		*pass++
		return
	}
	fmt.Println("FAIL", name)
}

func checkBitpackWidths() bool {
	for _, width := range []int{1, 7, 8, 63, 64} {
		count := 65
		max := uint64(1)<<width - 1
		if width == 64 {
			max = ^uint64(0)
		}
		values := make([]uint64, count)
		for i := range values {
			values[i] = []uint64{0, max, uint64(i % (width + 1))}[i%3]
		}
		packed, err := bitpack.Encode(values, width)
		if err != nil {
			return false
		}
		got, err := bitpack.Decode(packed, count, width)
		if err != nil || len(got) != count {
			return false
		}
		for i := range values {
			if got[i] != values[i] {
				return false
			}
		}
	}
	return true
}

func checkRoundTrip() bool {
	ints := []int64{-1, 0, 1, -1, 1 << 62, -(1 << 62)}
	di := dict.New[int64]()
	decodedInts, err := di.Decode(di.Encode(ints))
	if err != nil || len(decodedInts) != len(ints) {
		return false
	}
	for i := range ints {
		if decodedInts[i] != ints[i] {
			return false
		}
	}
	strings := []string{"", "a", "", "b"}
	ds := dict.New[string]()
	decodedStrings, err := ds.Decode(ds.Encode(strings))
	if err != nil {
		return false
	}
	for i := range strings {
		if decodedStrings[i] != strings[i] {
			return false
		}
	}
	return true
}

func checkNullPredicate() bool {
	values := []int64{0, 1, 0}
	present := []bool{true, false, false}
	z := zone.Build(values, present)
	if z.Has || z.Min != 0 || z.Max != 0 || z.NullCount != 1 {
		return false
	}
	eqZero := zone.Comparison[int64]{Op: zone.Eq, Value: 0}
	isNull := zone.NullCheck[int64]{IsNull: true}
	notNull := zone.NullCheck[int64]{IsNull: false}
	return eqZero.Match(0, true) == false &&
		isNull.Match(0, true) && !isNull.Match(0, false) &&
		notNull.Match(0, false) && eqZero.CanMatch(z)
}

func main() {
	checks := []struct {
		name string
		ok   bool
	}{
		{"编码往返无损", checkRoundTrip()},
		{"空值谓词语义", checkNullPredicate()},
		{"位宽边界", checkBitpackWidths()},
	}
	pass := 0
	for _, check := range checks {
		report(check.name, check.ok, &pass)
	}
	fmt.Printf("总计 %d/%d\n", pass, len(checks))
	if pass != len(checks) {
		panic("demo checks failed")
	}
}
