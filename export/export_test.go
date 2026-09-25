package export

import (
	"errors"
	"fmt"
	"testing"

	"ontology/snap"
)

func buildSnap(n int) *snap.Snapshot {
	m := map[string]int64{}
	for i := 0; i < n; i++ {
		m[fmt.Sprintf("k%06d", i)] = int64(i)
	}
	return snap.NewSnapshot(m)
}

// 定位位点后第一个键所检查的键个数不随 n 线性增长（二分定位）。
// 白盒：直接读非导出字段 checked，不经任何导出接口。
func TestLocateCheckedSublinear(t *testing.T) {
	const bound = 32 // 与 n 无关的小常数；log2(10000) ≈ 14
	for _, n := range []int{100, 1000, 10000} {
		e := NewExporter(buildSnap(n), 10)
		mid := fmt.Sprintf("k%06d", n/2)
		entries, cur, done, err := e.Next(mid)
		if err != nil {
			t.Fatalf("n=%d: %v", n, err)
		}
		if e.checked > bound {
			t.Fatalf("n=%d: checked=%d exceeds constant bound %d (linear scan?)", n, e.checked, bound)
		}
		wantFirst := fmt.Sprintf("k%06d", n/2+1)
		if entries[0].Key != wantFirst || done {
			t.Fatalf("n=%d: first=%s cur=%s done=%v", n, entries[0].Key, cur, done)
		}
	}
}

// done 判定：末块满块也 done=true，无需额外空块；空快照直接 done。
func TestNextDoneSemantics(t *testing.T) {
	cases := []struct {
		name    string
		keys    int
		size    int
		cursor  string
		wantLen int
		wantCur string
		wantDon bool
	}{
		{"full last block done", 6, 2, "k000003", 2, "k000005", true},
		{"short last block done", 5, 2, "k000003", 1, "k000004", true},
		{"middle block not done", 6, 2, "", 2, "k000001", false},
		{"cursor beyond end empty done", 6, 2, "zzz", 0, "", true},
		{"empty snapshot done", 0, 2, "", 0, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := NewExporter(buildSnap(tc.keys), tc.size)
			entries, cur, done, err := e.Next(tc.cursor)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != tc.wantLen || cur != tc.wantCur || done != tc.wantDon {
				t.Fatalf("got len=%d cur=%q done=%v, want %d %q %v",
					len(entries), cur, done, tc.wantLen, tc.wantCur, tc.wantDon)
			}
		})
	}
}

// done 之后再 Next/Resume 返回 ErrFinished，且可判定。
func TestFinishedRejected(t *testing.T) {
	e := NewExporter(buildSnap(2), 2)
	if _, _, done, _ := e.Next(""); !done {
		t.Fatal("want done")
	}
	for _, call := range []func() error{
		func() error { _, _, _, err := e.Next("k000001"); return err },
		func() error { _, _, _, err := e.Resume("k000001"); return err },
	} {
		if err := call(); !errors.Is(err, ErrFinished) {
			t.Fatalf("want ErrFinished, got %v", err)
		}
	}
}
