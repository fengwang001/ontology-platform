package ontology

import (
	"errors"
	"testing"
)

func TestSchemaLimits(t *testing.T) {
	// 手工构造深度 7 的链
	chain := Field{Name: "l7", Rep: Required}
	for i := 0; i < 6; i++ {
		chain = Field{Name: "l", Rep: Required, Children: []Field{chain}}
	}
	if _, err := New([]Field{chain}, 1, 1); !errors.Is(err, ErrSchema) {
		t.Fatalf("depth>6: %v", err)
	}

	// 17 个叶子
	var many []Field
	for i := 0; i < 17; i++ {
		many = append(many, Field{Name: "f" + string(rune('a'+i)), Rep: Required})
	}
	if _, err := New(many, 1, 1); !errors.Is(err, ErrSchema) {
		t.Fatalf("17 leaves: %v", err)
	}
	// 16 个叶子合法
	if _, err := New(many[:16], 1, 1); err != nil {
		t.Fatalf("16 leaves should pass: %v", err)
	}

	// 非法 Rep 值
	if _, err := New([]Field{{Name: "x", Rep: Rep(99)}}, 1, 1); !errors.Is(err, ErrSchema) {
		t.Fatalf("bad rep: %v", err)
	}

	// 仅由分组组成（无叶子）：上面 New(nil) 已覆盖；再测嵌套分组
	if _, err := New([]Field{{Name: "g", Rep: Required, Children: []Field{
		{Name: "h", Rep: Optional, Children: []Field{}},
	}}}, 1, 1); err != nil {
		// h 无 Children 是叶子，实际有叶子，所以这里应成功
		t.Fatalf("h is a leaf: %v", err)
	}
}

func TestRequiredInsideOptional(t *testing.T) {
	schema := []Field{
		{Name: "id", Rep: Required},
		{Name: "a", Rep: Optional, Children: []Field{
			{Name: "q", Rep: Required},
		}},
	}
	s, _ := New(schema, 10, 100)
	// a 缺失：不报错（q 的必填只在 a 存在时才要求）
	if err := s.Shred(map[string]any{"id": int64(1)}); err != nil {
		t.Fatalf("optional group missing: %v", err)
	}
	// a 存在但 q 缺失：ErrMissingRequired，路径 a.q
	err := s.Shred(map[string]any{"id": int64(2), "a": map[string]any{}})
	if !errors.Is(err, ErrMissingRequired) || ErrorPath(err) != "a.q" {
		t.Fatalf("want missing a.q got %v", err)
	}
	// a 存在且 q 提供：成功
	if err := s.Shred(map[string]any{"id": int64(3), "a": map[string]any{"q": int64(9)}}); err != nil {
		t.Fatal(err)
	}
	// 记录 0 无 a，记录 1 被拒绝，记录 2 有 a.q
	if s.Records() != 2 {
		t.Fatalf("records=%d", s.Records())
	}
	es, _ := s.Entries("a.q")
	if len(es) != 2 || es[0].Def != 0 || es[1].Def != 1 || es[1].Value != 9 {
		t.Fatalf("a.q entries: %v", es)
	}
}
