package lagstat

import (
	"testing"

	"ontology/cursor"
)

func TestSlowest(t *testing.T) {
	// head 增长（模拟 Append）不动堆；最慢者始终是 want 最小的活跃游标。
	cases := []struct {
		name  string
		wants []int64
		head  int64
		slow  int64
		scans int
	}{
		{"empty", nil, 5, 0, 0},
		{"all-caught-up", []int64{6, 6, 6}, 5, 0, 1},
		{"mixed", []int64{2, 5, 6}, 5, 3, 1},
		{"slowest-first", []int64{1, 2, 3, 4}, 10, 9, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr := New(tc.head)
			var cs []*cursor.Cursor
			for _, w := range tc.wants {
				c := cursor.New(w)
				cs = append(cs, c)
				tr.Add(c)
			}
			if got := tr.Slowest(); got != tc.slow {
				t.Fatalf("Slowest=%d want %d", got, tc.slow)
			}
			if got := tr.scanRecords(); got != tc.scans {
				t.Fatalf("scanned=%d want %d", got, tc.scans)
			}
		})
	}
}

func TestUpdateRemoveBehind(t *testing.T) {
	cases := []struct {
		name     string
		act      func(tr *Tracker, cs []*cursor.Cursor)
		slow     int64
		behind   int
		scanWhen int // 空堆时为 0，否则为 1
	}{
		{
			"advance-slowest",
			func(tr *Tracker, cs []*cursor.Cursor) {
				cs[0].Advance(cs[0].Want()) // want 1->2
				tr.Update(cs[0])
			},
			8, 0, 1,
		},
		{
			"mark-slowest-behind",
			func(tr *Tracker, cs []*cursor.Cursor) {
				cs[0].MarkBehind()
				tr.MarkBehind(cs[0])
			},
			5, 1, 1,
		},
		{
			"remove-slowest",
			func(tr *Tracker, cs []*cursor.Cursor) {
				tr.Remove(cs[0], false)
			},
			5, 0, 1,
		},
		{
			"recover-behind-rejoins",
			func(tr *Tracker, cs []*cursor.Cursor) {
				cs[0].MarkBehind()
				tr.MarkBehind(cs[0])
				cs[0].Recover(9)
				tr.Reactivate(cs[0])
			},
			5, 0, 1,
		},
		{
			"remove-last-behind",
			func(tr *Tracker, cs []*cursor.Cursor) {
				cs[0].MarkBehind()
				tr.MarkBehind(cs[0])
				tr.Remove(cs[0], true)
			},
			5, 0, 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr := New(10)
			cs := []*cursor.Cursor{cursor.New(1), cursor.New(5), cursor.New(6)}
			for _, c := range cs {
				tr.Add(c)
			}
			tc.act(tr, cs)
			if got := tr.Slowest(); got != tc.slow {
				t.Fatalf("Slowest=%d want %d", got, tc.slow)
			}
			if got := tr.BehindCount(); got != tc.behind {
				t.Fatalf("BehindCount=%d want %d", got, tc.behind)
			}
			if got := tr.scanRecords(); got != tc.scanWhen {
				t.Fatalf("scanned=%d want %d", got, tc.scanWhen)
			}
		})
	}
}

func TestSlowestScanConstant(t *testing.T) {
	// 100 与 10000 读者各查询一次最慢落后量，访问记录数都必须为 1（上限取 8）。
	const scanCap = 8
	for _, n := range []int{100, 10000} {
		tr := New(int64(n) + 1)
		for i := 0; i < n; i++ {
			tr.Add(cursor.New(int64(i + 1)))
		}
		if got := tr.Slowest(); got != int64(n) {
			t.Fatalf("n=%d Slowest=%d want %d", n, got, n)
		}
		if got := tr.scanRecords(); got > scanCap {
			t.Fatalf("n=%d scanned %d records, cap %d (must not scale with readers)", n, got, scanCap)
		}
	}
}

func TestDistribution(t *testing.T) {
	tr := New(10)
	cs := []*cursor.Cursor{cursor.New(8), cursor.New(5)}
	for _, c := range cs {
		tr.Add(c)
	}
	got := tr.Distribution(func() []*cursor.Cursor { return cs })
	want := []int64{2, 5}
	if len(got) != len(want) {
		t.Fatalf("distribution len=%d want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("distribution[%d]=%d want %d", i, got[i], want[i])
		}
	}
}
