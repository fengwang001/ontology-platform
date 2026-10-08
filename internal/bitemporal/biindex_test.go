package bitemporal

import "testing"

func TestAsOfBasic(t *testing.T) {
	var idx Index
	idx = idx.Apply(Commit{SysVersion: 1, BizStart: 10})
	idx = idx.Apply(Commit{SysVersion: 2, BizStart: 20})
	idx = idx.Apply(Commit{SysVersion: 3, BizStart: 10}) // 同业务起点覆盖

	cases := []struct {
		sys, biz int64
		want     int64
		ok       bool
	}{
		{0, 10, 0, false}, // 系统时间早于一切
		{1, 9, 0, false},  // 业务时间早于任何起点
		{1, 10, 1, true},  // 最早记录
		{2, 15, 1, true},  // [10,20) 命中 s1
		{2, 20, 2, true},  // [20, ∞) 命中 s2
		{3, 10, 3, true},  // 同起点，系统更晚者覆盖
		{2, 10, 1, true},  // 回溯到覆盖发生之前仍见 s1
		{3, 19, 3, true},  // 覆盖后区间 [10,20) 见 s3
		{3, 20, 2, true},
		{3, 1 << 62, 2, true},
	}
	for _, tc := range cases {
		got, ok := idx.AsOf(tc.sys, tc.biz)
		if ok != tc.ok || got != tc.want {
			t.Fatalf("AsOf(sys=%d,biz=%d)=(%d,%v), want (%d,%v)", tc.sys, tc.biz, got, ok, tc.want, tc.ok)
		}
	}

	if err := checkTreap(idx.root); err != nil {
		t.Fatalf("treap invariant broken: %v", err)
	}
}

func TestHistoryAsOf(t *testing.T) {
	var idx Index
	idx = idx.Apply(Commit{SysVersion: 1, BizStart: 10})
	idx = idx.Apply(Commit{SysVersion: 2, BizStart: 20})
	idx = idx.Apply(Commit{SysVersion: 3, BizStart: 30})

	segs := idx.HistoryAsOf(2)
	want := []Segment{{10, 20, 1}, {20, OpenEnd, 2}}
	if len(segs) != len(want) {
		t.Fatalf("len=%d want %d (%+v)", len(segs), len(want), segs)
	}
	for i := range want {
		if segs[i] != want[i] {
			t.Fatalf("seg[%d]=%+v want %+v", i, segs[i], want[i])
		}
	}

	// 在 s=4 补一个更早的起点（追溯写入过去的业务时间）。
	idx = idx.Apply(Commit{SysVersion: 4, BizStart: 5})
	segs = idx.HistoryAsOf(4)
	want = []Segment{{5, 10, 4}, {10, 20, 1}, {20, 30, 2}, {30, OpenEnd, 3}}
	if len(segs) != 4 {
		t.Fatalf("len=%d, segs=%+v", len(segs), segs)
	}
	for i := range want {
		if segs[i] != want[i] {
			t.Fatalf("seg[%d]=%+v want %+v", i, segs[i], want[i])
		}
	}
	// s=3 时该追溯起点尚不可见。
	if got := idx.HistoryAsOf(3); len(got) != 3 {
		t.Fatalf("as-of s3 len=%d, segs=%+v", len(got), got)
	}
}

func TestPersistentSnapshots(t *testing.T) {
	var idx Index
	s1 := idx.Apply(Commit{SysVersion: 1, BizStart: 10})
	s2 := s1.Apply(Commit{SysVersion: 2, BizStart: 5})
	if _, ok := s1.AsOf(2, 5); ok {
		t.Fatal("old snapshot must not see newer commit")
	}
	if v, ok := s2.AsOf(2, 5); !ok || v != 2 {
		t.Fatalf("new snapshot lookup got (%d,%v)", v, ok)
	}
}

// 未来系统时间才提交的业务起点，不得影响更早系统时间的查询；
// 但它之后的查询必须能看到它，且旧起点仍提供区间兜底。
func TestRetroactiveStartVisibility(t *testing.T) {
	var idx Index
	idx = idx.Apply(Commit{SysVersion: 1, BizStart: 10})
	idx = idx.Apply(Commit{SysVersion: 2, BizStart: 30}) // 删除/复活一类的更大起点

	// sys=1：起点 30 尚不存在，biz=50 由起点 10 的开放区间覆盖。
	if v, ok := idx.AsOf(1, 50); !ok || v != 1 {
		t.Fatalf("sys=1 fallback got (%d,%v), want (1,true)", v, ok)
	}
	// sys=2：biz=50 命中起点 30。
	if v, ok := idx.AsOf(2, 50); !ok || v != 2 {
		t.Fatalf("sys=2 got (%d,%v), want (2,true)", v, ok)
	}

	// sys=3 追溯写入一个更小的起点 5。
	idx = idx.Apply(Commit{SysVersion: 3, BizStart: 5})
	if v, ok := idx.AsOf(2, 7); ok {
		t.Fatalf("retroactive start must be invisible at sys=2, got v%d", v)
	}
	if v, ok := idx.AsOf(3, 7); !ok || v != 3 {
		t.Fatalf("sys=3 retroactive lookup got (%d,%v)", v, ok)
	}
	if v, ok := idx.AsOf(3, 50); !ok || v != 2 {
		t.Fatalf("sys=3 later segment got (%d,%v), want v2", v, ok)
	}
	if err := checkTreap(idx.root); err != nil {
		t.Fatalf("treap invariant: %v", err)
	}
}

func checkTreap(n *node) error {
	if n == nil {
		return nil
	}
	if n.left != nil {
		if n.left.key >= n.key {
			return errInvariant("left key")
		}
		if n.left.priority > n.priority {
			return errInvariant("left priority")
		}
		if err := checkTreap(n.left); err != nil {
			return err
		}
	}
	if n.right != nil {
		if n.right.key <= n.key {
			return errInvariant("right key")
		}
		if n.right.priority > n.priority {
			return errInvariant("right priority")
		}
		if err := checkTreap(n.right); err != nil {
			return err
		}
	}
	return nil
}

type invErr string

func (e invErr) Error() string { return string(e) }

func errInvariant(where string) error { return invErr("heap/bst violated at " + where) }
