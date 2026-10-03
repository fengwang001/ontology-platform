package diff

import (
	"sync"
	"testing"

	"ontology/norm"
)

func i64p(v int64) *int64 { return &v }

func mustPut(t *testing.T, d *T, r norm.Row, side bool) {
	t.Helper()
	var err error
	if side {
		err = d.SrcPut(r)
	} else {
		err = d.TgtPut(r)
	}
	if err != nil {
		t.Fatalf("put id=%d: %v", r.ID, err)
	}
}

func exampleTable(t *testing.T, sd, rm int, ne bool) *T {
	cfg, err := norm.NewCfg(sd, rm, ne)
	if err != nil {
		t.Fatal(err)
	}
	d := New(cfg)
	mustPut(t, d, norm.Row{ID: 1, D: i64p(125000), C: []byte("ab")}, true)
	mustPut(t, d, norm.Row{ID: 2, D: i64p(-125000), C: []byte("")}, true)
	mustPut(t, d, norm.Row{ID: 4, D: i64p(5000), C: []byte("z")}, true)
	mustPut(t, d, norm.Row{ID: 1, D: i64p(13), C: []byte("ab  ")}, false)
	mustPut(t, d, norm.Row{ID: 2, D: i64p(-12), C: nil}, false)
	mustPut(t, d, norm.Row{ID: 9, D: i64p(5), C: []byte("x")}, false)
	return d
}

func TestCompareExample(t *testing.T) {
	d := exampleTable(t, 2, norm.RmHalfAway, false)
	rows, err := d.Compare(1, 10)
	if err != nil {
		t.Fatal(err)
	}
	want := []ResultRow{
		{1, ClsEqual, 0},
		{2, ClsChanged, ColD | ColC},
		{4, ClsMissing, 0},
		{9, ClsExtra, 0},
	}
	if len(rows) != len(want) {
		t.Fatalf("rows = %v, want %v", rows, want)
	}
	for i := range want {
		if rows[i] != want[i] {
			t.Errorf("[%d] = %+v, want %+v", i, rows[i], want[i])
		}
	}

	d2 := exampleTable(t, 2, norm.RmHalfEven, false)
	rows2, _ := d2.Compare(1, 10)
	if rows2[1].Changed != ColC {
		t.Errorf("half-even id2 changed = %d, want ColC", rows2[1].Changed)
	}

	d3 := exampleTable(t, 2, norm.RmHalfAway, true)
	rows3, _ := d3.Compare(1, 10)
	if rows3[1].Changed != ColD {
		t.Errorf("ne=true id2 changed = %d, want ColD", rows3[1].Changed)
	}

	cfg6, _ := norm.NewCfg(6, norm.RmHalfAway, false)
	d6 := New(cfg6)
	mustPut(t, d6, norm.Row{ID: 7, D: i64p(123456), C: []byte(" ab ")}, true)
	mustPut(t, d6, norm.Row{ID: 7, D: i64p(123456), C: []byte(" ab   ")}, false)
	rows6, _ := d6.Compare(1, 10)
	if rows6[0].Class != ClsEqual {
		t.Errorf("sd=6 trimmed c: class = %d, want Equal", rows6[0].Class)
	}
}

func TestRangeBoundaryAndInvalid(t *testing.T) {
	d := exampleTable(t, 2, norm.RmHalfAway, false)
	rows, err := d.Compare(2, 9)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].ID != 2 || rows[1].ID != 4 {
		t.Errorf("range [2,9) = %v", rows)
	}
	rows, err = d.Compare(4, 4)
	if err != nil || len(rows) != 0 {
		t.Errorf("empty range: rows=%v err=%v", rows, err)
	}
	for _, tc := range [][2]int64{{0, 10}, {1, norm.MaxID + 2}, {9, 3}} {
		if _, err := d.Compare(tc[0], tc[1]); err != ErrInvalid {
			t.Errorf("Compare(%d,%d) err=%v, want ErrInvalid", tc[0], tc[1], err)
		}
	}
	if _, err := d.Compare(1, norm.MaxID+1); err != nil {
		t.Errorf("hi=10^9+1: %v", err)
	}
	if err := d.SrcPut(norm.Row{ID: 0}); err != ErrInvalid {
		t.Errorf("SrcPut invalid id")
	}
	if err := d.TgtPut(norm.Row{ID: 1, D: i64p(norm.MaxMant + 1)}); err != ErrInvalid {
		t.Errorf("TgtPut invalid mantissa")
	}
	if err := d.TgtDel(0); err != ErrInvalid {
		t.Errorf("TgtDel invalid id")
	}
}

func TestVersionMonotonic(t *testing.T) {
	cfg, _ := norm.NewCfg(2, norm.RmHalfAway, false)
	d := New(cfg)
	if v, ex := d.Version(5); v != 0 || ex {
		t.Errorf("fresh: v=%d ex=%v", v, ex)
	}
	mustPut(t, d, norm.Row{ID: 5, D: i64p(1)}, false)
	if v, ex := d.Version(5); v != 1 || !ex {
		t.Errorf("after put: v=%d ex=%v", v, ex)
	}
	mustPut(t, d, norm.Row{ID: 5, D: i64p(2)}, false)
	if v, _ := d.Version(5); v != 2 {
		t.Errorf("second put v=%d", v)
	}
	if err := d.TgtDel(5); err != nil {
		t.Fatal(err)
	}
	if v, ex := d.Version(5); v != 3 || ex {
		t.Errorf("after del: v=%d ex=%v (tombstone keeps counting)", v, ex)
	}
	if err := d.TgtDel(6); err != nil {
		t.Fatal(err)
	}
	if v, ex := d.Version(6); v != 1 || ex {
		t.Errorf("del missing: v=%d ex=%v", v, ex)
	}
}

func TestCommitAtomic(t *testing.T) {
	d := exampleTable(t, 2, norm.RmHalfAway, false)
	changes := []Change{
		{ID: 4, Kind: KindInsert, D: i64p(1), C: []byte("z"), Version: 0},
		{ID: 2, Kind: KindUpdate, D: i64p(-13), C: []byte(""), Mask: ColD | ColC, Version: 99},
	}
	err := d.Commit(changes)
	if se, ok := err.(*StaleError); !ok || se.ID != 2 {
		t.Fatalf("commit err=%v, want StaleError(2)", err)
	}
	rows, _ := d.Compare(1, 10)
	if rows[1].Class != ClsChanged || rows[2].Class != ClsMissing {
		t.Errorf("state changed after rejected commit: %v", rows)
	}

	if err := d.TgtDel(1); err != nil {
		t.Fatal(err)
	}
	changes = []Change{
		{ID: 2, Kind: KindUpdate, Version: 99, Mask: ColD},
		{ID: 1, Kind: KindDelete, Version: 1},
	}
	err = d.Commit(changes)
	if se, ok := err.(*StaleError); !ok || se.ID != 1 {
		t.Fatalf("want stale id 1, got %v", err)
	}

	if err := d.Commit([]Change{{ID: 9, Kind: KindInsert, Version: 0}}); err == nil {
		t.Errorf("insert existing should be stale")
	}

	ok2 := []Change{
		{ID: 4, Kind: KindInsert, D: i64p(1), C: []byte("z"), Version: 0},
		{ID: 2, Kind: KindUpdate, D: i64p(-13), C: []byte(""), Mask: ColD | ColC, Version: 1},
	}
	if err := d.Commit(ok2); err != nil {
		t.Fatalf("commit: %v", err)
	}
	rows, _ = d.Compare(1, 10)
	for _, r := range rows {
		if (r.ID == 2 || r.ID == 4) && r.Class != ClsEqual {
			t.Errorf("after commit id=%d class=%d", r.ID, r.Class)
		}
	}

	d2 := exampleTable(t, 2, norm.RmHalfAway, false)
	if err := d2.Commit([]Change{{ID: 2, Kind: KindUpdate, D: i64p(-13), Mask: ColD, Version: 1}}); err != nil {
		t.Fatal(err)
	}
	if got := *d2.tgt[2].D; got != -13 {
		t.Errorf("masked update D = %d", got)
	}
	if d2.tgt[2].C != nil {
		t.Errorf("masked update touched C: %v", d2.tgt[2].C)
	}
	if err := d2.Commit(nil); err != nil {
		t.Errorf("empty commit: %v", err)
	}
}

func TestVisitedCount(t *testing.T) {
	for _, n := range []int{1000, 100000} {
		cfg, _ := norm.NewCfg(6, norm.RmHalfAway, false)
		d := New(cfg)
		for id := int64(1); id <= int64(n); id++ {
			mustPut(t, d, norm.Row{ID: id, D: i64p(id), C: []byte("s")}, true)
			mustPut(t, d, norm.Row{ID: id, D: i64p(id), C: []byte("t  ")}, false)
		}
		mustPut(t, d, norm.Row{ID: norm.MaxID, D: i64p(1)}, true)
		mustPut(t, d, norm.Row{ID: norm.MaxID, D: i64p(2)}, false)
		rows, visited, err := d.CompareVisited(1, int64(n)+1)
		if err != nil {
			t.Fatal(err)
		}
		if visited != 2*n {
			t.Errorf("n=%d visited=%d, want %d", n, visited, 2*n)
		}
		if len(rows) != n {
			t.Errorf("n=%d result rows=%d", n, len(rows))
		}
		_, visited, _ = d.CompareVisited(100, int64(n)-99)
		if visited != 2*(n-199) {
			t.Errorf("partial range visited=%d, want %d", visited, 2*(n-199))
		}
	}
}

func TestSnapshotIsCopy(t *testing.T) {
	d := exampleTable(t, 2, norm.RmHalfAway, false)
	src, tgt, ver, err := d.Snapshot(1, 10)
	if err != nil {
		t.Fatal(err)
	}
	src[1].C[0] = 'Z'
	tgt[1].C[0] = 'Z'
	ver[1] = 999
	rows, _ := d.Compare(1, 10)
	if rows[0].Class != ClsEqual {
		t.Errorf("snapshot mutation leaked into table: %v", rows[0])
	}
	if _, _, _, err := d.Snapshot(0, 1); err != ErrInvalid {
		t.Errorf("snapshot invalid range")
	}
}

func TestConcurrent(t *testing.T) {
	cfg, _ := norm.NewCfg(3, norm.RmHalfEven, true)
	d := New(cfg)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for k := 0; k < 300; k++ {
				id := int64(g*300 + k + 1)
				_ = d.SrcPut(norm.Row{ID: id, D: i64p(id * 1000), C: []byte("a ")})
				_ = d.TgtPut(norm.Row{ID: id, D: i64p(id), C: []byte("a")})
				_, _, _ = d.CompareVisited(1, 10000)
				if k%7 == 0 {
					_ = d.TgtDel(id)
				}
			}
		}(g)
	}
	wg.Wait()
}
