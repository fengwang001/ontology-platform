package scan

import (
	"math/rand"
	"testing"

	"ontology/zone"
)

// 游标序列化往返。
func TestCursorEncodeDecode(t *testing.T) {
	for _, c := range []Cursor{{0, 0}, {3, 17}, {1000, 511}} {
		got, err := DecodeCursor(c.Encode())
		if err != nil {
			t.Fatal(err)
		}
		if got != c {
			t.Fatalf("got %+v want %+v", got, c)
		}
	}
	if _, err := DecodeCursor([]byte{0xff}); err == nil {
		t.Fatal("expected error for malformed cursor")
	}
}

// 遍历所有可能的批大小（1 到总行数），
// 跨批结果必须与一次性扫描完全相同。
func TestAllBatchSizes(t *testing.T) {
	rng := rand.New(rand.NewSource(23))
	const n = 300
	vals := make([]int64, n)
	nulls := make([]bool, n)
	for i := range vals {
		if rng.Intn(5) == 0 {
			nulls[i] = true
			continue
		}
		vals[i] = int64(rng.Intn(40) - 20)
	}
	s := buildSeg(t, 16, vals, nulls)
	pred := zone.And(zone.Ge(-10), zone.Le(10))
	sc := New(s, pred)
	want, err := sc.ScanAll()
	if err != nil {
		t.Fatal(err)
	}
	for batch := 1; batch <= n; batch++ {
		var got []Row
		cur := Cursor{}
		for !sc.Done(cur) {
			rows, next, err := sc.ScanFrom(cur, batch)
			if err != nil {
				t.Fatal(err)
			}
			// 游标经序列化往返再继续，模拟跨调用续扫。
			if next, err = DecodeCursor(next.Encode()); err != nil {
				t.Fatal(err)
			}
			if len(rows) == 0 && next == cur {
				t.Fatalf("batch=%d: no progress", batch)
			}
			got = append(got, rows...)
			cur = next
		}
		if len(got) != len(want) {
			t.Fatalf("batch=%d: got %d rows want %d", batch, len(got), len(want))
		}
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("batch=%d row %d: got %+v want %+v", batch, i, got[i], want[i])
			}
		}
	}
}

// 游标必须能穿过被裁剪的行组，不重复、不遗漏。
func TestCursorCrossesPrunedGroups(t *testing.T) {
	// 只有第 0 组和最后一组有命中，中间全部被裁剪。
	const groups, per = 50, 8
	var vals []int64
	for g := 0; g < groups; g++ {
		for i := 0; i < per; i++ {
			v := int64(g * 1000)
			if g == 0 || g == groups-1 {
				v = 7 // 命中值
			}
			vals = append(vals, v)
		}
	}
	s := buildSeg(t, per, vals, nil)
	sc := New(s, zone.And(zone.Eq(7)))
	want, err := sc.ScanAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(want) != 2*per {
		t.Fatalf("want %d hits got %d", 2*per, len(want))
	}
	if d := sc.DecodedGroups(); d != 2 {
		t.Fatalf("ScanAll decoded groups=%d want 2", d)
	}
	// 分批续扫（新扫描器，批大小对齐行组）：结果一致且解码行组数只有 2。
	sc2 := New(s, zone.And(zone.Eq(7)))
	var got []Row
	cur := Cursor{}
	for !sc2.Done(cur) {
		rows, next, err := sc2.ScanFrom(cur, per)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, rows...)
		cur = next
	}
	sameRows(t, got, want)
	if d := sc2.DecodedGroups(); d != 2 {
		t.Fatalf("decoded groups=%d want 2", d)
	}
}
