package regions

import (
	"errors"
	"testing"
)

func ids(rs []Region) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.ID
	}
	return out
}

func mustPut(t *testing.T, s *Set, r Region) {
	t.Helper()
	if err := s.Put(r); err != nil {
		t.Fatalf("Put(%s) unexpected error: %v", r.ID, err)
	}
}

func expectErr(t *testing.T, err, want error, ctx string) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("%s: want %v, got %v", ctx, want, err)
	}
	t.Logf("输入: %s -> 输出错误: %v（判定依据: 符合预期的拒绝原因）", ctx, err)
}

func TestBoundaryAndHoleMembership(t *testing.T) {
	// 逆时针外环 (0,0)(8,0)(8,8)(0,8)，洞 (3,3)(6,3)(6,6)(3,6)。
	outer := Ring{{0, 0}, {8, 0}, {8, 8}, {0, 8}}
	hole := Ring{{3, 3}, {6, 3}, {6, 6}, {3, 6}}
	s := NewSet()
	mustPut(t, s, Region{ID: "r1", Priority: 1, Outer: outer, Hole: hole})

	cases := []struct {
		x, y   int
		hit    bool
		reason string
	}{
		{0, 0, true, "外环顶点：闭区域"},
		{4, 0, true, "外环边上：闭区域"},
		{8, 4, true, "外环边上：闭区域"},
		{1, 1, true, "普通内部点"},
		{3, 3, true, "洞顶点：洞为开区域，仍属于区域"},
		{5, 3, true, "洞边上：洞为开区域，仍属于区域"},
		{6, 5, true, "洞边上：洞为开区域，仍属于区域"},
		{4, 4, false, "洞内部：不属于区域"},
		{5, 5, false, "洞内部：不属于区域"},
		{9, 4, false, "外环外部"},
	}
	for _, c := range cases {
		got, err := s.Locate(int64(c.x), int64(c.y))
		if err != nil {
			t.Fatalf("Locate(%d,%d): %v", c.x, c.y, err)
		}
		ok := len(got) == 1
		t.Logf("输入: Locate(%d,%d) -> 输出: %v；判定依据: %s", c.x, c.y, ids(got), c.reason)
		if ok != c.hit {
			t.Errorf("Locate(%d,%d) (%s): want hit=%v", c.x, c.y, c.reason, c.hit)
		}
	}
}

func TestClockwiseOuter(t *testing.T) {
	// 顺时针外环同样有效。
	outer := Ring{{0, 0}, {0, 8}, {8, 8}, {8, 0}}
	s := NewSet()
	mustPut(t, s, Region{ID: "cw", Priority: 0, Outer: outer})
	for _, p := range [][2]int{{4, 4}, {0, 0}, {8, 8}, {4, 0}, {9, 9}} {
		got, _ := s.Locate(int64(p[0]), int64(p[1]))
		t.Logf("输入: 顺时针区域 Locate(%d,%d) -> 输出: %v", p[0], p[1], ids(got))
	}
	if got, _ := s.Locate(4, 4); len(got) != 1 {
		t.Fatalf("clockwise inner point not located")
	}
	if got, _ := s.Locate(9, 9); len(got) != 0 {
		t.Fatalf("clockwise outer point incorrectly located")
	}
}

func TestOverlappingBoundariesAndOrdering(t *testing.T) {
	s := NewSet()
	// a 与 b 共享边界 x=4（a 的右边界、b 的左边界）。
	mustPut(t, s, Region{ID: "a", Priority: 1, Outer: Ring{{0, 0}, {4, 0}, {4, 4}, {0, 4}}})
	mustPut(t, s, Region{ID: "b", Priority: 2, Outer: Ring{{4, 0}, {8, 0}, {8, 4}, {4, 4}}})
	mustPut(t, s, Region{ID: "c", Priority: 1, Outer: Ring{{2, 0}, {6, 0}, {6, 6}, {2, 6}}})

	// 共享边上的点同时命中三个区域。
	got, err := s.Locate(4, 2)
	if err != nil || len(got) != 3 {
		t.Fatalf("shared edge Locate: hits=%v err=%v", ids(got), err)
	}
	// b 优先级 2 排第一；a、c 优先级并列 1，按 id 升序。
	want := []string{"b", "a", "c"}
	t.Logf("输入: Locate(4,2)（两区域边界重合点）-> 输出: %v；判定依据: %v", ids(got), want)
	for i := range want {
		if got[i].ID != want[i] {
			t.Fatalf("ordering: want %v got %v", want, ids(got))
		}
	}

	best, ok, err := s.Best(4, 2)
	if err != nil || !ok || best.ID != "b" {
		t.Fatalf("Best: id=%s ok=%v err=%v", best.ID, ok, err)
	}
	t.Logf("输入: Best(4,2) -> 输出: %s（优先级最高）", best.ID)

	got, _ = s.Locate(50, 50)
	if len(got) != 0 {
		t.Fatalf("outside point should hit nothing, got %v", ids(got))
	}
	_, ok, err = s.Best(50, 50)
	if ok || err != nil {
		t.Fatalf("Best outside: ok=%v err=%v", ok, err)
	}
}
