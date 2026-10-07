package ontology

import (
	"errors"
	"testing"
)

func mustCreateChain(t *testing.T, r *Registry, ids ...string) {
	t.Helper()
	for i, id := range ids {
		parent := ""
		if i > 0 {
			parent = ids[i-1]
		}
		if err := r.CreateType(id, parent, false); err != nil {
			t.Fatalf("CreateType(%q): %v", id, err)
		}
	}
}

func TestCreateTypeSealedParentRejected(t *testing.T) {
	r := NewRegistry()
	if err := r.CreateType("sealed", "", true); err != nil {
		t.Fatal(err)
	}
	if err := r.CreateType("child", "sealed", false); !errors.Is(err, ErrTypeSealed) {
		t.Fatalf("want ErrTypeSealed, got %v", err)
	}
	// 密封类型自身仍可定义规则并产生实例。
	if err := r.DeclareRule("sealed", "p", NewRule([]string{"a"})); err != nil {
		t.Fatal(err)
	}
	res, err := r.EffectiveRule("sealed", "p")
	if err != nil {
		t.Fatal(err)
	}
	if res.SourceType != "sealed" {
		t.Fatalf("sealed type rule must be final, got source %q", res.SourceType)
	}
}

func TestCreateTypeMissingParentNotFound(t *testing.T) {
	r := NewRegistry()
	if err := r.CreateType("child", "ghost", false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestDeleteTypeWithChildrenRejected(t *testing.T) {
	r := NewRegistry()
	mustCreateChain(t, r, "root", "mid", "leaf")
	if err := r.DeleteType("mid"); !errors.Is(err, ErrTypeHasChildren) {
		t.Fatalf("want ErrTypeHasChildren, got %v", err)
	}
	if err := r.DeleteType("ghost"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	// 末端类型可删除；删除后其父变为末端，也可删除。
	if err := r.DeleteType("leaf"); err != nil {
		t.Fatal(err)
	}
	if err := r.DeleteType("mid"); err != nil {
		t.Fatal(err)
	}
}

func TestDeclareRuleWideningRejected(t *testing.T) {
	r := NewRegistry()
	mustCreateChain(t, r, "root", "child")
	if err := r.DeclareRule("root", "p", NewRule([]string{"a", "b"})); err != nil {
		t.Fatal(err)
	}
	// 子集与相等均允许。
	if err := r.DeclareRule("child", "p", NewRule([]string{"a"})); err != nil {
		t.Fatal(err)
	}
	if err := r.DeclareRule("child", "p", NewRule([]string{"a"})); err != nil {
		t.Fatal(err)
	}
	// 扩大会被拒绝，且拒绝后原规则保持不变。
	if err := r.DeclareRule("child", "p", NewRule([]string{"a", "b", "c"})); !errors.Is(err, ErrRuleWidening) {
		t.Fatalf("want ErrRuleWidening, got %v", err)
	}
	res, err := r.EffectiveRule("child", "p")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Rule.Equal(NewRule([]string{"a"})) {
		t.Fatalf("rejected redeclaration must not take effect, got %v", res.Rule.Values())
	}
}

func TestNotFoundPrecedence(t *testing.T) {
	r := NewRegistry()
	mustCreateChain(t, r, "root")
	// 类型不存在优先于其余判定。
	if err := r.DeclareRule("ghost", "p", NewRule([]string{"a"})); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if _, err := r.EffectiveRule("ghost", "p"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	// 属性组合不存在。
	if _, err := r.EffectiveRule("root", "ghost-prop"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}
