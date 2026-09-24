package mrg

import (
	"fmt"
	"testing"

	"ontology/seg"
)

// TestProbeCountConstant 第四节：已存在会话 Append 的检查数（非导出 probe）不随 m 线性增长。
func TestProbeCountConstant(t *testing.T) {
	base := -1
	for _, m := range []int{100, 316, 1000, 3162, 10000} {
		g := New()
		for i := 0; i < m; i++ {
			if err := g.Append(Event{fmt.Sprintf("s%d", i), 1, i % 10}); err != nil {
				t.Fatal(err)
			}
		}
		if err := g.Append(Event{"s0", 2, 9}); err != nil {
			t.Fatalf("m=%d 已存在会话 Append: %v", m, err)
		}
		if g.probe > 2 {
			t.Fatalf("m=%d probe=%d，疑似整表扫描", m, g.probe)
		}
		if base < 0 {
			base = g.probe
		} else if g.probe != base {
			t.Fatalf("probe 随 m 变化: %d -> %d", base, g.probe)
		}
	}
}

// TestNewSessionRejectNoTrace 新键首条事件被拒时，不得在表中留下空会话。
func TestNewSessionRejectNoTrace(t *testing.T) {
	for _, c := range []struct {
		ev   Event
		want error
	}{
		{Event{"", 1, 1}, ErrBadKey},
		{Event{"z", 0, 1}, ErrInvalid},
		{Event{"z", 1, 10}, ErrInvalid},
	} {
		g := New()
		if err := g.Append(c.ev); err != c.want {
			t.Fatalf("err=%v want %v", err, c.want)
		}
		if _, ok := g.sessions["z"]; ok {
			t.Fatal("被拒事件在表中留下空会话")
		}
	}
}

// TestSegTable 表驱动：乱序折叠、缺口、越界、幂等、冲突与关闭语义。
func TestSegTable(t *testing.T) {
	for _, c := range []struct {
		ops     [][2]int
		n, want int
		ok      bool
	}{
		{[][2]int{{1, 1}, {2, 2}, {3, 3}}, 3, 123, true},
		{[][2]int{{2, 1}, {1, 5}, {4, 8}, {3, 2}}, 4, 5128, true},
		{[][2]int{{1, 0}, {2, 7}}, 2, 7, true}, // 前导零：07 -> 7
		{[][2]int{{1, 1}, {3, 3}}, 3, 0, false},
		{[][2]int{{1, 1}, {2, 2}, {3, 3}}, 2, 0, false}, // 越界项
	} {
		s := seg.New()
		for _, op := range c.ops {
			if err := s.Append(op[0], op[1]); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.Close(c.n); (err == nil) != c.ok {
			t.Fatalf("Close(%d)=%v want ok=%v", c.n, err, c.ok)
		}
		if c.ok {
			if v, closed := s.Result(); !closed || v != c.want {
				t.Fatalf("结果=(%d,%v) want %d", v, closed, c.want)
			}
		}
	}
	s := seg.New()
	must := func(err, want error, what string) {
		if err != want {
			t.Fatalf("%s: %v want %v", what, err, want)
		}
	}
	must(s.Append(1, 4), nil, "首次")
	must(s.Append(1, 4), nil, "同值幂等")
	must(s.Append(1, 5), seg.ErrConflict, "异值冲突")
	must(s.Close(1), nil, "Close")
	must(s.Append(2, 9), seg.ErrClosed, "关闭后 Append")
	must(s.Close(1), nil, "同 N 幂等")
	must(s.Close(2), seg.ErrClosed, "异 N 拒绝")
	if v, _ := s.Result(); v != 4 {
		t.Fatalf("冻结值被污染: %d", v)
	}
	// 非法输入逐条拒绝；再在全新会话上验证失败不留痕（补合法事件后 Close 成功）。
	bad := [][2]int{{0, 1}, {-3, 1}, {1, -1}, {1, 10}}
	s = seg.New()
	for _, op := range bad {
		must(s.Append(op[0], op[1]), seg.ErrInvalid, "非法输入")
	}
	must(s.Append(1, 6), nil, "非法后合法补入")
	must(s.Close(1), nil, "Close 应成功（失败不留痕）")
}

// TestEightSteps 钉住第三节八步：s1=5128、s2=3；步 7 已关闭追加 ErrClosed 且不留痕。
func TestEightSteps(t *testing.T) {
	g := New()
	type op struct {
		sid       string
		seq, v, n int
		close     bool
	}
	ops := []op{
		{"s1", 2, 1, 0, false}, {"s2", 1, 3, 0, false}, {"s1", 1, 5, 0, false},
		{"s1", 4, 8, 0, false}, {"s1", 3, 2, 0, false}, {"s1", 0, 0, 4, true},
		{"s1", 5, 9, 0, false}, {"s2", 0, 0, 1, true},
	}
	for i, o := range ops {
		var err error
		if o.close {
			err = g.Close(o.sid, o.n)
		} else {
			err = g.Append(Event{o.sid, o.seq, o.v})
		}
		if i == 6 {
			if err != ErrClosed {
				t.Fatalf("步7 err=%v want ErrClosed", err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("步%d: %v", i+1, err)
		}
	}
	res := func(sid string) int {
		if v, ok, e := g.Result(sid); e == nil && ok {
			return v
		}
		return -1
	}
	if v1, v2 := res("s1"), res("s2"); v1 != 5128 || v2 != 3 {
		t.Fatalf("s1=%d s2=%d，want 5128/3", v1, v2)
	}
	if got := g.Seen("s1"); len(got) != 4 || got[3] != 4 {
		t.Fatalf("步7 留痕: s1 seen=%v", got)
	}
}
