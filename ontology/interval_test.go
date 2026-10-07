package ontology

import "testing"

func TestIntervalUnionAndContains(t *testing.T) {
	u := unionIntervals([]interval{
		{from: 1, to: 2},
		{from: 3, to: 4},   // 相邻 => 合并
		{from: 10, to: 12}, // 有缺口 => 独立段
		{from: 20, to: 0, open: true},
		{from: 25, to: 30}, // 被开放区间吞掉
	})
	set := intervalSet(u)
	cases := map[int]bool{
		1: true, 2: true, 3: true, 4: true, 5: false, 9: false,
		10: true, 12: true, 13: false, 19: false,
		20: true, 50: true, 1000: true, // 开放区间随最新版本延伸
	}
	for v, want := range cases {
		if got := set.contains(v); got != want {
			t.Fatalf("v=%d want %v got %v (union=%+v)", v, want, got, u)
		}
	}
	if len(u) != 3 {
		t.Fatalf("expected 3 merged segments, got %d: %+v", len(u), u)
	}
	if !u[2].open {
		t.Fatal("last segment must remain open-ended")
	}

	// 闭区间在前、开放区间紧随且相接 => 合并为单一开放段。
	u2 := unionIntervals([]interval{{from: 31, to: 31}, {from: 32, open: true}})
	if len(u2) != 1 || !u2[0].open || u2[0].from != 31 {
		t.Fatalf("closed+open adjacency merge wrong: %+v", u2)
	}
	if !(intervalSet(u2).contains(53)) {
		t.Fatal("open union must contain far-future version")
	}
}

func TestRevocationPunchHole(t *testing.T) {
	p := attrPerm{
		grants:  unionIntervals([]interval{{from: 1, open: true}}),
		revokes: unionIntervals([]interval{{from: 3, to: 5}}),
	}
	want := map[int]bool{1: true, 2: true, 3: false, 4: false, 5: false, 6: true, 99: true}
	for v, ok := range want {
		if p.allowedAt(v) != ok {
			t.Fatalf("v=%d want %v", v, ok)
		}
	}
}
