package fill

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	"ontology/grid"
)

// 定位已知键的上一个点时，检查的键个数不随已存在键数 m 线性增长（哈希定位而非扫描）。
func TestProbesIndependentOfSize(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		f := NewFiller(10)
		for i := 0; i < m; i++ {
			p := grid.Point{Key: fmt.Sprintf("k%d", i), TS: 0, Val: 1}
			if err := f.Feed([]grid.Point{p}); err != nil {
				t.Fatal(err)
			}
		}
		if err := f.Feed([]grid.Point{{Key: "k0", TS: 10, Val: 1}}); err != nil {
			t.Fatal(err)
		}
		if f.probes > 2 {
			t.Fatalf("m=%d: probes=%d, want <=2 (hash lookup, not a scan)", m, f.probes)
		}
	}
}

// 任何被拒的点（空键/不在网格/不递增）都不得改变任何键的序列，且之后可继续正常使用。
func TestRejectedFeedNoTrace(t *testing.T) {
	good := []grid.Point{{Key: "k", TS: 0, Val: 5}, {Key: "k", TS: 30, Val: 8}}
	bads := [][]grid.Point{
		{{Key: "", TS: 40, Val: 1}},
		{{Key: "k", TS: 35, Val: 1}},
		{{Key: "k", TS: 20, Val: 1}},
		{{Key: "k", TS: 40, Val: 9}, {Key: "k", TS: 45, Val: 9}},
		{{Key: "z", TS: 0, Val: 1}, {Key: "z", TS: 3, Val: 1}},
	}
	wants := []error{ErrEmptyKey, grid.ErrOffGrid, grid.ErrNotIncreasing, grid.ErrOffGrid, grid.ErrOffGrid}
	f := NewFiller(10)
	if err := f.Feed(good); err != nil {
		t.Fatal(err)
	}
	before := f.View("k")
	for i := range bads {
		if err := f.Feed(bads[i]); !errors.Is(err, wants[i]) {
			t.Fatalf("case %d: err %v, want %v", i, err, wants[i])
		}
	}
	if got := f.View("k"); !reflect.DeepEqual(got, before) || f.View("z") != nil {
		t.Fatal("rejected feeds left trace")
	}
	if err := f.Feed([]grid.Point{{Key: "k", TS: 40, Val: 12}}); err != nil {
		t.Fatalf("cannot continue after rejection: %v", err)
	}
}
