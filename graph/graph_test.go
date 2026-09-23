package graph

import "testing"

func TestGraph(t *testing.T) {
	t.Run("层与宽度", func(t *testing.T) {
		g, err := New([]string{"a", "b", "c", "d", "e"},
			map[string][]string{"c": {"a", "b"}, "d": {"c"}, "e": {"c"}})
		if err != nil {
			t.Fatal(err)
		}
		ls := g.Layers()
		want := [][]string{{"a", "b"}, {"c"}, {"d", "e"}}
		if len(ls) != 3 {
			t.Fatalf("layers=%v", ls)
		}
		for i := range want {
			if len(ls[i]) != len(want[i]) {
				t.Fatalf("layer %d = %v, want %v", i, ls[i], want[i])
			}
			for j := range want[i] {
				if ls[i][j] != want[i][j] {
					t.Fatalf("layer %d = %v, want %v", i, ls[i], want[i])
				}
			}
		}
		if g.Width() != 2 {
			t.Fatalf("width=%d", g.Width())
		}
		ro := g.ReverseOrder()
		// 逆拓扑：d,e 在 c 前，c 在 a,b 前
		pos := map[string]int{}
		for i, n := range ro {
			pos[n] = i
		}
		if !(pos["d"] < pos["c"] && pos["e"] < pos["c"] && pos["c"] < pos["a"] && pos["c"] < pos["b"]) {
			t.Fatalf("reverse order=%v", ro)
		}
	})

	// 环路径必须真实闭合：路径相邻节点在输入中确有边，首尾相同。
	t.Run("环路径真实闭合", func(t *testing.T) {
		_, err := New([]string{"a", "b", "c", "d"},
			map[string][]string{"b": {"a"}, "c": {"b"}, "a": {"c"}, "d": {"a"}})
		ce, ok := err.(*CycleError)
		if !ok {
			t.Fatalf("want CycleError, got %v", err)
		}
		p := ce.Path
		if len(p) < 2 || p[0] != p[len(p)-1] {
			t.Fatalf("path not closed: %v", p)
		}
		edge := map[[2]string]bool{{"a", "c"}: true, {"c", "b"}: true, {"b", "a"}: true}
		for i := 0; i+1 < len(p); i++ {
			if !edge[[2]string{p[i], p[i+1]}] {
				t.Fatalf("edge %s->%s not in input; path=%v", p[i], p[i+1], p)
			}
		}
	})

	// 自环也必须被识别并闭合。
	t.Run("自环", func(t *testing.T) {
		_, err := New([]string{"x"}, map[string][]string{"x": {"x"}})
		if _, ok := err.(*CycleError); !ok {
			t.Fatalf("want CycleError, got %v", err)
		}
	})

	t.Run("重复与未知依赖", func(t *testing.T) {
		if _, err := New([]string{"a", "a"}, nil); err == nil {
			t.Fatal("dup accepted")
		}
		if _, err := New([]string{"a"}, map[string][]string{"a": {"z"}}); err == nil {
			t.Fatal("unknown dep accepted")
		}
	})
}
