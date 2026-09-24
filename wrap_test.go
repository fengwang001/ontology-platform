package ontology

import "testing"

// mapHash 按查表返回哈希值，未命中时回退到默认哈希，
// 用于手工构造环绕与碰撞场景。
func mapHash(table map[string]uint64) HashFunc {
	return func(s string) uint64 {
		if v, ok := table[s]; ok {
			return v
		}
		return defaultHash(s)
	}
}

// TestWrapAround 手工构造只有两个虚拟节点的环（位置 100 与 200），
// 验证三类位置的 key：最小位置之前、两者之间、最大位置之后（回绕）。
func TestWrapAround(t *testing.T) {
	h := mapHash(map[string]uint64{
		"nodeA#0":     100,
		"nodeB#0":     200,
		"key-before":  50,  // 小于最小位置 -> 归 nodeA
		"key-between": 150, // 两者之间   -> 归 nodeB
		"key-after":   250, // 大于最大位置 -> 回绕到最小位置 nodeA
	})
	r := New(WithHash(h))
	if err := r.Add("nodeA", 1); err != nil {
		t.Fatal(err)
	}
	if err := r.Add("nodeB", 1); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		key  string
		want string
	}{
		{"key-before", "nodeA"},
		{"key-between", "nodeB"},
		{"key-after", "nodeA"}, // 回绕到最小位置，不是报错、不是归最大者
	}
	for _, c := range cases {
		got, err := r.Locate(c.key)
		if err != nil {
			t.Fatalf("Locate(%q): %v", c.key, err)
		}
		if got != c.want {
			t.Fatalf("Locate(%q) = %q, 期望 %q", c.key, got, c.want)
		}
	}
}

// TestCollisionTieBreak 两个节点的虚拟节点哈希到同一位置时，
// 由节点 ID 字典序小者胜出，且与插入顺序无关。
func TestCollisionTieBreak(t *testing.T) {
	h := mapHash(map[string]uint64{
		"aaa#0":  42,
		"bbb#0":  42, // 与 aaa 碰撞
		"probe":  1,  // 落在位置 42 之前 -> 归位置 42 的胜出者
		"probe2": 43, // 越过 42 后回绕，仍归位置 42 的胜出者
	})

	// 两种相反插入顺序，结果必须一致。
	for _, order := range [][]string{{"aaa", "bbb"}, {"bbb", "aaa"}} {
		r := New(WithHash(h))
		for _, id := range order {
			if err := r.Add(id, 1); err != nil {
				t.Fatal(err)
			}
		}
		if r.Size() != 1 {
			t.Fatalf("插入顺序 %v: 碰撞后环上应有 1 个位置，实际 %d", order, r.Size())
		}
		for _, key := range []string{"probe", "probe2"} {
			got, err := r.Locate(key)
			if err != nil {
				t.Fatalf("Locate(%q): %v", key, err)
			}
			if got != "aaa" {
				t.Fatalf("插入顺序 %v: Locate(%q) = %q, 期望字典序小者 aaa",
					order, key, got)
			}
		}
	}
}
