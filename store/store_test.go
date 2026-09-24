package store

import (
	"fmt"
	"reflect"
	"testing"
)

// 第四节：清除阶段检查个数不随墓碑总数 m 线性增长（不超过小常数 + 本次清除数 + 本次丢弃失效条目数）。
func TestPurgeCheckedBounded(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		s := New(1<<60, m+10) // R 极大：没有墓碑到期
		for i := 1; i <= m; i++ {
			if err := s.ApplyOne(Event{Op: 'D', Key: fmt.Sprintf("k%d", i), Ver: int64(i)}); err != nil {
				t.Fatal(err)
			}
		}
		// 一条只让 G 前进 1、不使任何墓碑到期的事件
		if err := s.ApplyOne(Event{Op: 'U', Key: "probe", Ver: int64(m + 1)}); err != nil {
			t.Fatal(err)
		}
		if s.checked > 1 { // 本次清除 0、失效条目 0，上界为常数 1
			t.Fatalf("m=%d: checked=%d, want <= 1", m, s.checked)
		}
	}
}

// 检查个数的精确公式：本次真正清除数 + 本次丢弃失效条目数 + 至多 1。
func TestPurgeCheckedExactBound(t *testing.T) {
	s := New(10, 100)
	for i := int64(1); i <= 5; i++ { // 墓碑 k1@1 .. k5@5；G 最大 6，不触发清除
		_ = s.ApplyOne(Event{Op: 'D', Key: fmt.Sprintf("k%d", i), Ver: i})
	}
	_ = s.ApplyOne(Event{Op: 'D', Key: "keep", Ver: 6})
	_ = s.ApplyOne(Event{Op: 'U', Key: "k2", Ver: 7}) // k2 墓碑成失效条目
	_ = s.ApplyOne(Event{Op: 'D', Key: "probe", Ver: 20})
	// G=20，cut=10：清除 k1,k3,k4,k5,keep 共 5 个真墓碑 + 丢弃 k2 失效条目，再检查 1 个未到期（probe@20）
	if want := 5 + 1 + 1; s.checked != want {
		t.Fatalf("checked=%d, want %d", s.checked, want)
	}
	if v, ok := s.Tomb("probe"); !ok || v != 20 || len(s.tombs) != 1 {
		t.Fatalf("tombs=%v, want only probe@20", s.tombs)
	}
}

// 第三节十步分步表（R=10，maxKeys=100）。
func TestTenStepTrace(t *testing.T) {
	s := New(10, 100)
	steps := []struct {
		e     Event
		rows  map[string]Row
		tombs map[string]int64
		g, ig int64
	}{
		{Event{Op: 'U', Key: "k1", Ver: 5, Val: "a"}, map[string]Row{"k1": {Val: "a", Ver: 5}}, map[string]int64{}, 5, 0},
		{Event{Op: 'D', Key: "k1", Ver: 8}, map[string]Row{}, map[string]int64{"k1": 8}, 8, 0},
		{Event{Op: 'U', Key: "k1", Ver: 6, Val: "b"}, map[string]Row{}, map[string]int64{"k1": 8}, 8, 1},
		{Event{Op: 'U', Key: "k2", Ver: 12, Val: "c"}, map[string]Row{"k2": {Val: "c", Ver: 12}}, map[string]int64{"k1": 8}, 12, 1},
		{Event{Op: 'U', Key: "k1", Ver: 8, Val: "d"}, map[string]Row{"k2": {Val: "c", Ver: 12}}, map[string]int64{"k1": 8}, 12, 2},
		{Event{Op: 'D', Key: "k3", Ver: 15}, map[string]Row{"k2": {Val: "c", Ver: 12}}, map[string]int64{"k1": 8, "k3": 15}, 15, 2},
		{Event{Op: 'U', Key: "k2", Ver: 18, Val: "e"}, map[string]Row{"k2": {Val: "e", Ver: 18}}, map[string]int64{"k3": 15}, 18, 2},
		{Event{Op: 'U', Key: "k1", Ver: 7, Val: "f"}, map[string]Row{"k1": {Val: "f", Ver: 7}, "k2": {Val: "e", Ver: 18}}, map[string]int64{"k3": 15}, 18, 2},
		{Event{Op: 'U', Key: "k3", Ver: 14, Val: "g"}, map[string]Row{"k1": {Val: "f", Ver: 7}, "k2": {Val: "e", Ver: 18}}, map[string]int64{"k3": 15}, 18, 3},
		{Event{Op: 'U', Key: "k4", Ver: 30, Val: "h"}, map[string]Row{"k1": {Val: "f", Ver: 7}, "k2": {Val: "e", Ver: 18}, "k4": {Val: "h", Ver: 30}}, map[string]int64{}, 30, 3},
	}
	for i, st := range steps {
		if err := s.ApplyOne(st.e); err != nil {
			t.Fatalf("step %d: %v", i+1, err)
		}
		if !reflect.DeepEqual(s.rows, st.rows) || !reflect.DeepEqual(s.tombs, st.tombs) {
			t.Fatalf("step %d: rows=%v tombs=%v, want %v %v", i+1, s.rows, s.tombs, st.rows, st.tombs)
		}
		if s.G != st.g || s.Ignored != st.ig {
			t.Fatalf("step %d: G=%d ignored=%d, want %d %d", i+1, s.G, s.Ignored, st.g, st.ig)
		}
	}
}

// 不变量 3：删除不被旧写入复活（Ver <= 墓碑版本的 U 一律被忽略）。
func TestTombBlocksOldWrites(t *testing.T) {
	for _, ver := range []int64{9, 10} { // 均 <= 墓碑版本 10
		s := New(1<<60, 100)
		_ = s.ApplyOne(Event{Op: 'U', Key: "k", Ver: 5, Val: "a"})
		_ = s.ApplyOne(Event{Op: 'D', Key: "k", Ver: 10})
		_ = s.ApplyOne(Event{Op: 'U', Key: "k", Ver: ver, Val: "b"})
		if _, ok := s.Get("k"); ok {
			t.Fatalf("ver=%d: old write resurrected", ver)
		}
		if v, _ := s.Tomb("k"); v != 10 {
			t.Fatalf("ver=%d: tomb=%d, want 10", ver, v)
		}
	}
}

// naiveRef 朴素参照：同样的规则，但墓碑永不清除。
type naiveRef struct {
	rows  map[string]Row
	tombs map[string]int64
}

func (n *naiveRef) apply(e Event) {
	cur := n.tombs[e.Key]
	if r, ok := n.rows[e.Key]; ok {
		cur = r.Ver
	}
	if e.Ver <= cur {
		return
	}
	if e.Op == 'U' {
		n.rows[e.Key] = Row{Val: e.Val, Ver: e.Ver}
		delete(n.tombs, e.Key)
		return
	}
	delete(n.rows, e.Key)
	n.tombs[e.Key] = e.Ver
}

// 不变量 2：满足 Ver > G_before − R 的随机序列与朴素参照逐项一致。
func TestNaiveConsistency(t *testing.T) {
	for seed := int64(1); seed <= 20; seed++ {
		tab := New(5, 1000)
		n := &naiveRef{map[string]Row{}, map[string]int64{}}
		g, s := int64(0), uint64(seed)
		for i := 0; i < 200; i++ {
			s = s*6364136223846793005 + 1
			e := Event{Op: 'U', Key: fmt.Sprintf("k%d", (s>>40)%10), Ver: g + 1 + int64((s>>33)%3), Val: "v"}
			if s&1 == 0 {
				e.Op = 'D'
			}
			if err := tab.ApplyOne(e); err != nil {
				t.Fatal(err)
			}
			n.apply(e)
			g = e.Ver
		}
		for i := 0; i < 10; i++ {
			k := fmt.Sprintf("k%d", i)
			if got, ok := tab.Get(k); (n.rows[k] == Row{}) == ok || got != n.rows[k] {
				t.Fatalf("seed %d key %s: got %v %v, want %v", seed, k, got, ok, n.rows[k])
			}
		}
	}
}
