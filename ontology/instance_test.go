package ontology

import (
	"errors"
	"testing"
)

// TestHistoricalValuesSurviveTightening 规则收紧后，历史取值不做追溯性
// 重新校验或修改：读取始终成功且返回原值；只有新写入按当前生效规则校验。
func TestHistoricalValuesSurviveTightening(t *testing.T) {
	r := NewRegistry()
	mustCreateChain(t, r, "T0", "T1")
	if err := r.DeclareRule("T0", "status", NewRule([]string{"new", "open", "closed"})); err != nil {
		t.Fatal(err)
	}
	if err := r.CreateInstance("inst-1", "T1"); err != nil {
		t.Fatal(err)
	}
	if err := r.WriteInstance("inst-1", "status", "closed"); err != nil {
		t.Fatal(err)
	}

	// 链条上发生重新声明，收紧到不再允许 "closed"。
	if err := r.DeclareRule("T1", "status", NewRule([]string{"new", "open"})); err != nil {
		t.Fatal(err)
	}

	// 历史取值读取必须始终成功，且返回收紧前写入的原值。
	for i := 0; i < 10; i++ {
		v, written, err := r.ReadInstance("inst-1", "status")
		if err != nil || !written || v != "closed" {
			t.Fatalf("historical read must succeed with original value, got %q,%v,%v", v, written, err)
		}
	}

	// 新写入按当前生效规则校验。
	if err := r.WriteInstance("inst-1", "status", "closed"); !errors.Is(err, ErrValueNotAllowed) {
		t.Fatalf("want ErrValueNotAllowed, got %v", err)
	}
	if err := r.WriteInstance("inst-1", "status", "open"); err != nil {
		t.Fatal(err)
	}
	v, _, _ := r.ReadInstance("inst-1", "status")
	if v != "open" {
		t.Fatalf("new write must be stored, got %q", v)
	}
}

// TestWriteValidatesAgainstEffectiveRule 写入校验使用继承得到的生效规则。
func TestWriteValidatesAgainstEffectiveRule(t *testing.T) {
	r := NewRegistry()
	mustCreateChain(t, r, "T0", "T1")
	if err := r.DeclareRule("T0", "p", NewRule([]string{"a", "b"})); err != nil {
		t.Fatal(err)
	}
	if err := r.CreateInstance("i", "T1"); err != nil {
		t.Fatal(err)
	}
	if err := r.WriteInstance("i", "p", "a"); err != nil {
		t.Fatal(err)
	}
	if err := r.WriteInstance("i", "p", "zzz"); !errors.Is(err, ErrValueNotAllowed) {
		t.Fatalf("want ErrValueNotAllowed, got %v", err)
	}
	// 类型或属性不存在优先于取值校验。
	if err := r.WriteInstance("i", "ghost", "a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if err := r.WriteInstance("ghost", "p", "a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if err := r.CreateInstance("j", "ghost"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}
