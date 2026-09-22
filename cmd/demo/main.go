// demo 逐项演示列式存储段的关键语义，每步打印一行 OK/FAIL。
package main

import (
	"fmt"
	"math/rand"
	"os"

	"ontology/scan"
	"ontology/segment"
	"ontology/zone"
)

var passed, failed int

func check(name string, ok bool) {
	if ok {
		passed++
		fmt.Printf("OK   %s\n", name)
	} else {
		failed++
		fmt.Printf("FAIL %s\n", name)
	}
}

func build(rgRows int, vals []int64, nulls []bool) *segment.Segment {
	b := segment.NewBuilder(segment.Options{RowGroupRows: rgRows})
	for i, v := range vals {
		if nulls != nil && nulls[i] {
			_ = b.AddNull()
		} else {
			_ = b.Add(v)
		}
	}
	s, err := b.Finish()
	if err != nil {
		panic(err)
	}
	return s
}

func main() {
	rng := rand.New(rand.NewSource(1))
	demoRoundTrip(rng)
	demoThreeStates()
	demoPruning()
	demoExhaustive(rng)
	demoNullSemantics()
	demoBatchEquivalence(rng)
	demoTruncation()
	demoWidths(rng)
	demoLimits()
	demoQueriesNoDecode(rng)
	demoConcurrency(rng)
	fmt.Printf("TOTAL %d passed, %d failed\n", passed, failed)
	if failed > 0 {
		os.Exit(1)
	}
}

func demoRoundTrip(rng *rand.Rand) {
	vals := make([]int64, 1000)
	nulls := make([]bool, 1000)
	big := []int64{-1 << 60, 5, 1 << 60}
	for i := range vals {
		if rng.Intn(9) == 0 {
			nulls[i] = true
			continue
		}
		if i/100%2 == 0 {
			vals[i] = big[rng.Intn(3)] // 低基数大值 -> 字典编码
		} else {
			vals[i] = int64(rng.Intn(2000)) // 高基数 -> 位打包
		}
	}
	s := build(100, vals, nulls)
	encs := map[segment.Encoding]bool{}
	ok := s.RowCount() == 1000
	idx := 0
	for g := 0; g < s.GroupCount() && ok; g++ {
		enc, _ := s.GroupEncoding(g)
		encs[enc] = true
		rows, err := s.DecodeGroup(g)
		if err != nil {
			ok = false
			break
		}
		for _, r := range rows {
			if r.Null != nulls[idx] || (!r.Null && r.V != vals[idx]) {
				ok = false
			}
			idx++
		}
	}
	check("编码往返无损(两种编码)", ok && encs[segment.EncDict] && encs[segment.EncBitpack])
}

func demoThreeStates() {
	s := build(8, []int64{0, 0}, []bool{true, false})
	rows, _ := s.DecodeGroup(0)
	st, _ := s.GroupStats(0)
	ok := rows[0].Null && !rows[1].Null && rows[1].V == 0 &&
		st.HasValue && st.Min == 0 && st.Max == 0 && st.Nulls == 1
	check("三态可判定(空值/0/非空)", ok)
}

func demoPruning() {
	var vals []int64
	for g := 0; g < 1000; g++ {
		for i := 0; i < 8; i++ {
			vals = append(vals, int64(g*100+i))
		}
	}
	s := build(8, vals, nil)
	sc := scan.New(s, zone.And(zone.Eq(500*100+3)))
	rows, err := sc.ScanAll()
	ok := err == nil && len(rows) == 1 &&
		sc.DecodedGroups() == 1 && sc.DecodedValues() <= 8
	check(fmt.Sprintf("裁剪计数对照(1000->%d组)", sc.DecodedGroups()), ok)
}

func demoExhaustive(rng *rand.Rand) {
	vals := make([]int64, 3000)
	nulls := make([]bool, 3000)
	for i := range vals {
		if rng.Intn(6) == 0 {
			nulls[i] = true
			continue
		}
		vals[i] = int64(rng.Intn(100) - 50)
	}
	s := build(64, vals, nulls)
	mn, mx := int64(-50), int64(49)
	preds := []zone.Conjunction{
		zone.And(zone.Eq(mn)), zone.And(zone.Eq(mx)),
		zone.And(zone.Gt(mx)), zone.And(zone.Lt(mn)),
		zone.And(zone.Ge(mn), zone.Le(mx)),
		zone.And(zone.In(0, 7)), zone.And(zone.IsNull()),
	}
	ok := true
	for _, p := range preds {
		got, _ := scan.New(s, p).ScanAll()
		var want int
		for g := 0; g < s.GroupCount(); g++ {
			rows, _ := s.DecodeGroup(g)
			for _, r := range rows {
				if p.Match(r.V, r.Null) {
					want++
				}
			}
		}
		if len(got) != want {
			ok = false
		}
	}
	check("穷举对照无误杀(含边界谓词)", ok)
}
