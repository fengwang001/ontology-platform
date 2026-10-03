package diff

import (
	"errors"
	"testing"

	"ontology/norm"
)

func i64(v int64) *int64 { return &v }

func kindName(k int) string {
	return [...]string{"Equal", "Changed", "Missing", "Extra"}[k]
}

// 规格例一：sd=2 rm=0 ne=false
func TestCompareSpecExample(t *testing.T) {
	e, _ := New(2, norm.RMHalfUp, false)
	must(t, e.SrcPut(Row{ID: 1, D: i64(125000), C: []byte("ab")}))
	must(t, e.SrcPut(Row{ID: 2, D: i64(-125000), C: []byte("")}))
	must(t, e.SrcPut(Row{ID: 4, D: i64(5000), C: []byte("z")}))
	must(t, e.TgtPut(Row{ID: 1, D: i64(13), C: []byte("ab  ")}))
	must(t, e.TgtPut(Row{ID: 2, D: i64(-12), C: nil}))
	must(t, e.TgtPut(Row{ID: 9, D: i64(5), C: []byte("x")}))

	rs, err := e.Compare(1, 10)
	must(t, err)
	want := []struct {
		id   int64
		kind int
		cols []int
	}{
		{1, Equal, nil},
		{2, Changed, []int{ColD, ColC}},
		{4, Missing, nil},
		{9, Extra, nil},
	}
	if len(rs) != len(want) {
		t.Fatalf("got %d rows want %d", len(rs), len(want))
	}
	for i, w := range want {
		if rs[i].ID != w.id || rs[i].Kind != w.kind {
			t.Fatalf("row %d: got (%d,%s) want (%d,%s)", i, rs[i].ID, kindName(rs[i].Kind), w.id, kindName(w.kind))
		}
		if !sameCols(rs[i].Cols, w.cols) {
			t.Fatalf("id %d cols got %v want %v", w.id, rs[i].Cols, w.cols)
		}
	}
	if rs[2].SD == nil || *rs[2].SD != 1 { // id 4: 5000 -> 1
		t.Fatalf("id 4 Cvt d want 1 got %v", rs[2].SD)
	}
	if e.LastVisitCount() != 6 {
		t.Fatalf("visit count = %d want 6", e.LastVisitCount())
	}

	// rm=1 时 id 2 的 d 应相等，仅剩 c 不等
	e2, _ := New(2, norm.RMHalfEven, false)
	must(t, e2.SrcPut(Row{ID: 2, D: i64(-125000), C: []byte("")}))
	must(t, e2.TgtPut(Row{ID: 2, D: i64(-12), C: nil}))
	rs2, _ := e2.Compare(1, 10)
	if rs2[0].Kind != Changed || !sameCols(rs2[0].Cols, []int{ColC}) {
		t.Fatalf("half-even id2 want Changed{c}, got %s %v", kindName(rs2[0].Kind), rs2[0].Cols)
	}

	// sd=6 不舍入；" ab " 与 " ab   " 归一相等
	e3, _ := New(6, norm.RMHalfUp, false)
	must(t, e3.SrcPut(Row{ID: 1, D: i64(123456), C: []byte(" ab ")}))
	must(t, e3.TgtPut(Row{ID: 1, D: i64(123456), C: []byte(" ab   ")}))
	rs3, _ := e3.Compare(1, 2)
	if rs3[0].Kind != Equal {
		t.Fatalf("sd=6 trim example want Equal got %s", kindName(rs3[0].Kind))
	}
}

func TestCompareRangeAndNE(t *testing.T) {
	e, _ := New(2, norm.RMHalfUp, true)
	must(t, e.SrcPut(Row{ID: 2, D: i64(0), C: []byte("")}))
	must(t, e.TgtPut(Row{ID: 2, D: i64(0), C: nil}))
	rs, _ := e.Compare(2, 3)
	if rs[0].Kind != Equal {
		t.Fatalf("ne=true empty==NULL want Equal got %s", kindName(rs[0].Kind))
	}
	// 区间边界：[2,3) 只含 id 2；[1,2) 为空
	rs0, _ := e.Compare(1, 2)
	if len(rs0) != 0 {
		t.Fatalf("empty range must yield 0 rows")
	}
	if e.LastVisitCount() != 0 {
		t.Fatalf("empty range visit count must be 0")
	}
	for _, bad := range [][2]int64{{0, 2}, {2, 1}, {1, MaxID + 2}, {0, 0}} {
		if _, err := e.Compare(bad[0], bad[1]); !errors.Is(err, ErrParam) {
			t.Fatalf("Compare(%d,%d) must fail with ErrParam", bad[0], bad[1])
		}
	}
	// hi = MaxID+1 合法
	if _, err := e.Compare(1, MaxID+1); err != nil {
		t.Fatalf("hi=MaxID+1 must be legal: %v", err)
	}
}

func TestVersionMonotonic(t *testing.T) {
	e, _ := New(2, norm.RMHalfUp, false)
	checkVer := func(id int64, want int64, exist bool) {
		t.Helper()
		_, _, ver, ex := e.TgtSnapshot(id)
		if ver != want || ex != exist {
			t.Fatalf("id %d: ver=%d(exist=%v) want %d(exist=%v)", id, ver, ex, want, exist)
		}
	}
	checkVer(7, 0, false)
	must(t, e.TgtPut(Row{ID: 7, D: i64(1), C: nil}))
	checkVer(7, 1, true)
	must(t, e.TgtPut(Row{ID: 7, D: i64(2), C: nil}))
	checkVer(7, 2, true)
	must(t, e.TgtDel(7))
	checkVer(7, 3, false) // 删除不归零
	must(t, e.TgtPut(Row{ID: 7, D: i64(3), C: nil}))
	checkVer(7, 4, true)
	must(t, e.TgtDel(7))
	must(t, e.TgtDel(7)) // 删除不存在的行也累加版本
	checkVer(7, 6, false)
}

func TestVisitCount(t *testing.T) {
	for _, n := range []int{1000, 100000} {
		e, _ := New(2, norm.RMHalfUp, false)
		for id := 1; id <= n; id++ {
			must(t, e.SrcPut(Row{ID: int64(id), D: i64(int64(id)), C: nil}))
		}
		// 目标行放在范围之外（范围 [1, n+1)，目标 id 从 n+2 起）
		for id := n + 2; id <= 2*n+1; id++ {
			must(t, e.TgtPut(Row{ID: int64(id), D: i64(int64(id)), C: nil}))
		}
		rs, err := e.Compare(1, int64(n)+1)
		must(t, err)
		if len(rs) != n {
			t.Fatalf("n=%d rows got %d", n, len(rs))
		}
		if got := e.LastVisitCount(); got != n {
			t.Fatalf("n=%d visit count = %d, want exactly %d (out-of-range ignored)", n, got, n)
		}
	}
}

func TestRejectsDoNotChangeState(t *testing.T) {
	e, _ := New(2, norm.RMHalfUp, false)
	if err := e.SrcPut(Row{ID: 0, D: nil, C: nil}); !errors.Is(err, ErrParam) {
		t.Fatalf("bad SrcPut must fail")
	}
	if err := e.TgtPut(Row{ID: 1, D: i64(MaxM() + 1), C: nil}); !errors.Is(err, ErrParam) {
		t.Fatalf("bad TgtPut must fail")
	}
	if err := e.TgtDel(0); !errors.Is(err, ErrParam) {
		t.Fatalf("bad TgtDel must fail")
	}
	if _, _, _, exist := e.TgtSnapshot(1); exist {
		t.Fatalf("rejected writes must not create rows")
	}
	rs, _ := e.Compare(1, 10)
	if len(rs) != 0 {
		t.Fatalf("rejected writes must not be visible")
	}
}

func MaxM() int64 { return norm.MaxM }

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func sameCols(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
