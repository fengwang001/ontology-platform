package ontology

import "testing"

// 手工构造只有两个虚拟节点的环（位置 100 与 200），
// 验证落在最小位置之前、两者之间、最大位置之后的 key 的归属，
// 其中最大位置之后的 key 必须回绕到最小位置的虚拟节点。
func TestWraparoundSemantics(t *testing.T) {
	hash := func(b []byte) uint64 {
		switch string(b) {
		case "n1#0":
			return 100
		case "n2#0":
			return 200
		case "before":
			return 50
		case "between":
			return 150
		case "after":
			return 300
		}
		return defaultHash(b)
	}
	r := New(WithHash(hash))
	if err := r.Add("n1", 1); err != nil {
		t.Fatal(err)
	}
	if err := r.Add("n2", 1); err != nil {
		t.Fatal(err)
	}

	cases := []struct{ key, want string }{
		{"before", "n1"},  // 50 -> 100
		{"between", "n2"}, // 150 -> 200
		{"after", "n1"},   // 300 -> 回绕到 100
	}
	for _, c := range cases {
		got, err := r.Locate(c.key)
		if err != nil {
			t.Fatalf("Locate(%q): %v", c.key, err)
		}
		if got != c.want {
			t.Fatalf("Locate(%q) = %q, want %q", c.key, got, c.want)
		}
	}
}

// 虚拟节点位置相同（哈希碰撞）时按节点 ID 字典序打破并列，
// 且结果与插入顺序无关。
func TestCollisionTieBreakByNodeID(t *testing.T) {
	constHash := func([]byte) uint64 { return 42 }

	r1 := New(WithHash(constHash))
	if err := r1.Add("beta", 1); err != nil {
		t.Fatal(err)
	}
	if err := r1.Add("alpha", 1); err != nil {
		t.Fatal(err)
	}

	r2 := New(WithHash(constHash))
	if err := r2.Add("alpha", 1); err != nil {
		t.Fatal(err)
	}
	if err := r2.Add("beta", 1); err != nil {
		t.Fatal(err)
	}

	if r1.points[0] != r2.points[0] || r1.points[1] != r2.points[1] {
		t.Fatalf("tie-break depends on insert order: %+v vs %+v", r1.points, r2.points)
	}
	for _, r := range []*Ring{r1, r2} {
		got, err := r.Locate("anything")
		if err != nil {
			t.Fatal(err)
		}
		if got != "alpha" {
			t.Fatalf("collision tie-break = %q, want lexicographically smaller %q", got, "alpha")
		}
	}
}
