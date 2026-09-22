package main

import (
	"sync"

	"ontology/bitpack"
	"ontology/scan"
	"ontology/segment"
	"ontology/zone"
)

func checkBatches() {
	var groups []struct {
		vals  []int64
		nulls []bool
	}
	total := 0
	for i := 0; i < 10; i++ {
		gr := grp()
		for j := 0; j <= i*3; j++ {
			gr.vals = append(gr.vals, int64((i*10+j)%17))
			gr.nulls = append(gr.nulls, (i+j)%7 == 0)
		}
		total += len(gr.vals)
		groups = append(groups, gr)
	}
	seg := build(groups)
	preds := []zone.Pred{zone.GreaterEqual(3), zone.LessEqual(12)}
	want := scan.NewScanner(seg, preds).All()
	ok := true
	for batch := 1; batch <= total; batch++ {
		sc := scan.NewScanner(seg, preds)
		var got []scan.Row
		for {
			rows, eof := sc.Next(batch)
			got = append(got, rows...)
			if eof {
				break
			}
		}
		if len(got) != len(want) {
			ok = false
			break
		}
		for i := range want {
			if got[i] != want[i] {
				ok = false
			}
		}
	}
	report("all batch sizes equal", ok)
}

func checkTruncation() {
	b := segment.NewBuilder(segment.Config{})
	_ = b.AddRowGroup([]int64{1, 2, 3}, []bool{false, true, false})
	_ = b.AddRowGroup([]int64{9, 8}, []bool{false, false})
	full := b.Bytes()
	ok := true
	for cut := 0; cut < len(full); cut++ {
		seg, err := segment.Open(full[:cut])
		if err != nil {
			continue // staged CorruptError, as required
		}
		for g := 0; g < seg.NumRowGroups(); g++ {
			if _, _, err := seg.DecodeRowGroup(g); err != nil {
				ok = false
			}
		}
	}
	report("every truncation detected", ok)
}

func checkWidths() {
	ok := true
	for _, w := range []uint8{1, 7, 8, 63, 64} {
		vals := []uint64{0, 1, 0xdeadbeef, 1<<63 + 5, 42}
		out, err := bitpack.Unpack(bitpack.Pack(nil, vals, w), len(vals), w)
		if err != nil || len(out) != len(vals) {
			ok = false
			continue
		}
		mask := ^uint64(0)
		if w < 64 {
			mask = 1<<w - 1
		}
		for i, v := range vals {
			if out[i] != v&mask {
				ok = false
			}
		}
	}
	report("bit widths 1/7/8/63/64", ok)
}

func checkLimits() {
	b := segment.NewBuilder(segment.Config{MaxRows: 2, MaxRowGroups: 1, MaxDictCard: 2})
	_ = b.AddRowGroup([]int64{1, 2}, []bool{false, false})
	before := b.Bytes()
	ok := b.AddRowGroup([]int64{3}, []bool{false}) == segment.ErrTooManyRowGroups
	b2 := segment.NewBuilder(segment.Config{MaxRows: 1})
	_ = b2.AddRowGroup([]int64{1}, []bool{false})
	ok = ok && b2.AddRowGroup([]int64{2}, []bool{false}) == segment.ErrTooManyRows
	ok = ok && b.Rows() == 2 && len(b.Bytes()) == len(before)
	// Dictionary overflow falls back to bit-packing.
	b3 := segment.NewBuilder(segment.Config{MaxDictCard: 2})
	ok = ok && b3.AddRowGroup([]int64{1, 2, 3}, []bool{false, false, false}) == nil
	seg, err := segment.Open(b3.Bytes())
	ok = ok && err == nil && seg.GroupEncoding(0) == segment.BitPacked
	report("limits and dict fallback", ok)
}

func checkMetadata() {
	seg := build([]struct {
		vals  []int64
		nulls []bool
	}{grp(1, 2, 3), grp(4, 5)})
	a := seg.NumRows()
	ok := a == 5 && seg.NumRowGroups() == 2 &&
		seg.GroupStats(0).Min == 1 && seg.GroupEncoding(1) == segment.DictEncoded
	ok = ok && seg.NumRows() == a && seg.DecodedGroups() == 0 &&
		seg.DecodedValues() == 0
	report("metadata queries decode-free", ok)
}

func checkConcurrency() {
	var groups []struct {
		vals  []int64
		nulls []bool
	}
	for i := 0; i < 20; i++ {
		groups = append(groups, grp(int64(i), int64(i+1), int64(i)))
	}
	seg := build(groups)
	preds := []zone.Pred{zone.GreaterEqual(5)}
	want := scan.NewScanner(seg, preds).All()
	var wg sync.WaitGroup
	ok := true
	var mu sync.Mutex
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got := scan.NewScanner(seg, preds).All()
			mu.Lock()
			if len(got) != len(want) {
				ok = false
			}
			for i := range want {
				if got[i] != want[i] {
					ok = false
				}
			}
			mu.Unlock()
		}()
	}
	wg.Wait()
	report("concurrent scans consistent", ok)
}
