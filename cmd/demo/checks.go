package main

import (
	"errors"
	"math/rand"
	"sync"

	"ontology/bitpack"
	"ontology/scan"
	"ontology/segment"
	"ontology/zone"
)

func demoNullSemantics() {
	vals := []int64{0, 5, 0, -5}
	nulls := []bool{true, false, false, true}
	s := build(4, vals, nulls)
	eq0, _ := scan.New(s, zone.And(zone.Eq(0))).ScanAll()
	isNull, _ := scan.New(s, zone.And(zone.IsNull())).ScanAll()
	notNull, _ := scan.New(s, zone.And(zone.IsNotNull())).ScanAll()
	ok := len(eq0) == 1 && len(isNull) == 2 && len(notNull) == 2
	for _, r := range eq0 {
		ok = ok && !r.Null
	}
	check("空值谓词语义(数值谓词不命中空值)", ok)
}

func demoBatchEquivalence(rng *rand.Rand) {
	const n = 200
	vals := make([]int64, n)
	for i := range vals {
		vals[i] = int64(rng.Intn(30))
	}
	s := build(16, vals, nil)
	pred := zone.And(zone.Ge(5), zone.Le(20))
	want, _ := scan.New(s, pred).ScanAll()
	ok := true
	for batch := 1; batch <= n && ok; batch++ {
		sc := scan.New(s, pred)
		var got []scan.Row
		cur := scan.Cursor{}
		for !sc.Done(cur) {
			rows, next, err := sc.ScanFrom(cur, batch)
			if err != nil {
				ok = false
				break
			}
			got = append(got, rows...)
			cur = next
		}
		if len(got) != len(want) {
			ok = false
			break
		}
		for i := range got {
			if got[i] != want[i] {
				ok = false
			}
		}
	}
	check("跨批等价(遍历所有批大小)", ok)
}

func demoTruncation() {
	vals := make([]int64, 103)
	for i := range vals {
		vals[i] = int64(i * 7)
	}
	s := build(16, vals, nil)
	full := len(s.Bytes())
	ok := true
	for cut := 0; cut < full && ok; cut++ {
		func() {
			defer func() {
				if recover() != nil {
					ok = false
				}
			}()
			s2, err := segment.Open(s.Truncated(cut))
			if err != nil {
				if !segment.IsCorrupt(err) {
					ok = false
				}
				return
			}
			detected := false
			for g := 0; g < s2.GroupCount(); g++ {
				if _, err := s2.DecodeGroup(g); err != nil {
					detected = segment.IsCorrupt(err)
					break
				}
			}
			if !detected {
				ok = false
			}
		}()
	}
	check("截断点全可判定(不panic)", ok)
}

func demoWidths(rng *rand.Rand) {
	ok := true
	for _, w := range []int{1, 7, 8, 63, 64} {
		vals := make([]uint64, 65) // 非 64 整数倍
		mask := uint64(1)<<w - 1
		if w == 64 {
			mask = ^uint64(0)
		}
		for i := range vals {
			vals[i] = uint64(rng.Int63()) & mask
		}
		packed, err := bitpack.Encode(vals, w)
		if err != nil {
			ok = false
			continue
		}
		got, err := bitpack.DecodeAll(packed, w, 65)
		if err != nil {
			ok = false
			continue
		}
		for i := range vals {
			if got[i] != vals[i] {
				ok = false
			}
		}
	}
	check("位宽边界(1/7/8/63/64,非64倍数)", ok)
}

func demoLimits() {
	b := segment.NewBuilder(segment.Options{RowGroupRows: 2, MaxRows: 4, MaxRowGroups: 100})
	for i := 0; i < 4; i++ {
		_ = b.Add(int64(i))
	}
	rowErr := b.Add(9)
	stateKept := b.Rows() == 4
	b2 := segment.NewBuilder(segment.Options{RowGroupRows: 2, MaxRows: 100, MaxRowGroups: 1})
	_ = b2.Add(1)
	_ = b2.Add(2)
	grpErr := b2.Add(3)
	// 字典超限降级：基数上限 2，写 5 个大值，应回退位打包。
	b3 := segment.NewBuilder(segment.Options{RowGroupRows: 5, MaxDictCard: 2})
	for i := 0; i < 5; i++ {
		_ = b3.Add(int64(i) << 40)
	}
	s3, err := b3.Finish()
	enc, _ := s3.GroupEncoding(0)
	ok := errors.Is(rowErr, segment.ErrTooManyRows) && stateKept &&
		errors.Is(grpErr, segment.ErrTooManyRowGroups) &&
		err == nil && enc == segment.EncBitpack
	check("超限拒绝与字典降级", ok)
}

func demoQueriesNoDecode(rng *rand.Rand) {
	vals := make([]int64, 500)
	for i := range vals {
		vals[i] = int64(rng.Intn(50))
	}
	s := build(50, vals, nil)
	sc := scan.New(s, zone.And(zone.Eq(7)))
	q := func() (int, int, string) {
		enc, _ := s.GroupEncoding(0)
		st, _ := s.GroupStats(0)
		return s.RowCount(), s.GroupCount(), enc.String() + string(rune(st.Rows))
	}
	a1, b1, c1 := q()
	a2, b2, c2 := q()
	ok := a1 == a2 && b1 == b2 && c1 == c2 &&
		sc.DecodedGroups() == 0 && sc.DecodedValues() == 0
	check("查询不触发解码", ok)
}

func demoConcurrency(rng *rand.Rand) {
	vals := make([]int64, 2000)
	for i := range vals {
		vals[i] = int64(rng.Intn(100))
	}
	s := build(32, vals, nil)
	pred := zone.And(zone.Ge(10), zone.Le(90))
	want, _ := scan.New(s, pred).ScanAll()
	sc := scan.New(s, pred)
	var wg sync.WaitGroup
	ok := true
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var got []scan.Row
			cur := scan.Cursor{}
			for !sc.Done(cur) {
				rows, next, err := sc.ScanFrom(cur, 13)
				if err != nil {
					ok = false
					return
				}
				got = append(got, rows...)
				cur = next
			}
			if len(got) != len(want) {
				ok = false
			}
		}()
	}
	wg.Wait()
	check("并发扫描一致(8 goroutine)", ok)
}
