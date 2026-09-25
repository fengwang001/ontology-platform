// Package commit 的自检：对内置序列核验不变量。
package commit

import (
	"fmt"
	"strconv"
)

// SelfCheck 对内置序列核验不变量；全部通过返回 nil。
func (e *Engine) SelfCheck() error {
	g, _ := New(3) // 1) 六步确定交错（NOTES.md 第三节）
	n1 := map[string]int64{"a": 1, "c": 1}
	n2 := map[string]int64{"b": 5, "c": 10}
	s1, s2 := g.snapshotOf(n1), g.snapshotOf(n2) // 步1、步2
	steps := []struct {
		f       func() bool
		a, b, c int64
	}{
		{func() bool { return true }, 0, 0, 0},                           // 步1/2 只读
		{func() bool { return g.tryCommit(s1) }, 1, 0, 1},                // 步3
		{func() bool { return !g.tryCommit(s2) }, 1, 0, 1},               // 步4 冲突
		{func() bool { return g.tryCommit(g.snapshotOf(n2)) }, 1, 5, 11}, // 步5 重快照 + 步6
	}
	for i, st := range steps {
		if !st.f() {
			return fmt.Errorf("六步交错第 %d 段结果不符", i)
		}
		if v := g.View(); v["a"] != st.a || v["b"] != st.b || v["c"] != st.c {
			return fmt.Errorf("六步交错第 %d 段视图不符", i)
		}
	}
	if g.cells["a"].Ver != 1 || g.cells["b"].Ver != 1 || g.cells["c"].Ver != 2 { // 2) 版本恰 +1
		return fmt.Errorf("版本未恰 +1")
	}
	for _, m := range []int{100, 1000, 10000} { // 3) 校验个数不随 m 增长
		h, _ := New(1)
		for i := 0; i < m; i++ {
			h.Commit([]Op{{"k" + strconv.Itoa(i), 1}})
		}
		h.Commit([]Op{{"k0", 1}})
		if h.lastChecked != 1 {
			return fmt.Errorf("m=%d 校验个数 %d，应恒为 1", m, h.lastChecked)
		}
	}
	x, _ := New(1) // 4) 冲突失败不留痕
	x.Commit([]Op{{"x", 7}})
	stale := x.snapshotOf(map[string]int64{"x": 1})
	x.Commit([]Op{{"x", 1}}) // 使 stale 过期
	if x.tryCommit(stale) || x.View()["x"] != 8 || x.cells["x"].Ver != 2 || x.Retries() != 0 {
		return fmt.Errorf("冲突失败留下了痕迹")
	}
	return nil
}
