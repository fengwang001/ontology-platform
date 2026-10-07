package migration

import (
	"errors"
	"reflect"
	"testing"
)

func TestAmendRejectsContradictionWithinBatch(t *testing.T) {
	d := NewDeclaration()
	err := d.Amend([]Mapping{
		{Property: "name", Action: ActionRetain},
		{Property: "name", Action: ActionDeprecate},
	})
	if !errors.Is(err, ErrInvalidMapping) {
		t.Fatalf("expected ErrInvalidMapping, got %v", err)
	}
	// 被拒绝的变更不得留下任何状态。
	if d.IsDeprecated("name") {
		t.Fatalf("rejected amend must not change declaration state")
	}
}

func TestAmendRejectsContradictionAgainstExisting(t *testing.T) {
	d := NewDeclaration()
	if err := d.Amend([]Mapping{{Property: "name", Action: ActionRetain}}); err != nil {
		t.Fatal(err)
	}
	// 对应关系尚未对任何已回填实例生效时,允许替换。
	if err := d.Amend([]Mapping{{Property: "name", Action: ActionDeprecate}}); err != nil {
		t.Fatalf("replacing non-effective mapping should be allowed, got %v", err)
	}
	// 一旦生效则拒绝修改。
	d.MarkEffective(map[string]any{"name": "a"})
	if err := d.Amend([]Mapping{{Property: "name", Action: ActionRetain}}); !errors.Is(err, ErrInvalidMapping) {
		t.Fatalf("expected ErrInvalidMapping, got %v", err)
	}
}

func TestAmendRejectsContradictionAfterEffective(t *testing.T) {
	d := NewDeclaration()
	if err := d.Amend([]Mapping{{Property: "name", Action: ActionRetain}}); err != nil {
		t.Fatal(err)
	}
	d.MarkEffective(map[string]any{"name": "a"})
	if err := d.Amend([]Mapping{{Property: "name", Action: ActionDeprecate}}); !errors.Is(err, ErrInvalidMapping) {
		t.Fatalf("expected ErrInvalidMapping, got %v", err)
	}
}

func TestAmendRejectsInvalidMapping(t *testing.T) {
	d := NewDeclaration()
	if err := d.Amend(nil); !errors.Is(err, ErrInvalidMapping) {
		t.Fatalf("empty batch: expected ErrInvalidMapping, got %v", err)
	}
	if err := d.Amend([]Mapping{{Property: "x", Action: ActionAddDefault}}); !errors.Is(err, ErrInvalidMapping) {
		t.Fatalf("nil default: expected ErrInvalidMapping, got %v", err)
	}
	if err := d.Amend([]Mapping{{Property: "x", Action: ActionRetain, Default: 1}}); !errors.Is(err, ErrInvalidMapping) {
		t.Fatalf("retain with default: expected ErrInvalidMapping, got %v", err)
	}
}

func TestAmendRejectsModifyingEffectiveMapping(t *testing.T) {
	d := NewDeclaration()
	if err := d.Amend([]Mapping{
		{Property: "name", Action: ActionRetain},
		{Property: "age", Action: ActionAddDefault, Default: 0},
		{Property: "nick", Action: ActionDeprecate},
	}); err != nil {
		t.Fatal(err)
	}
	// 模拟一次回填:旧数据含 name 与 nick,不含其他已声明旧属性。
	d.MarkEffective(map[string]any{"name": "a", "nick": "b", "other": 1})

	// 已生效:retain(name)、deprecate(nick)、add-default(age) 均不可再改。
	for _, m := range []Mapping{
		{Property: "name", Action: ActionDeprecate},
		{Property: "nick", Action: ActionRetain},
		{Property: "age", Action: ActionAddDefault, Default: 1},
	} {
		if err := d.Amend([]Mapping{m}); !errors.Is(err, ErrInvalidMapping) {
			t.Fatalf("modifying effective mapping %v: expected ErrInvalidMapping, got %v", m, err)
		}
	}
	// 未生效的对应关系仍可修改:声明一个回填时未出现的旧属性并修改之。
	if err := d.Amend([]Mapping{{Property: "email", Action: ActionRetain}}); err != nil {
		t.Fatal(err)
	}
	if err := d.Amend([]Mapping{{Property: "email", Action: ActionDeprecate}}); err != nil {
		t.Fatalf("modifying non-effective mapping should be allowed, got %v", err)
	}
	// 追加全新对应关系始终允许。
	if err := d.Amend([]Mapping{{Property: "score", Action: ActionAddDefault, Default: 100}}); err != nil {
		t.Fatalf("appending new mapping should be allowed, got %v", err)
	}
	// 重复声明相同对应关系是幂等 no-op。
	if err := d.Amend([]Mapping{{Property: "name", Action: ActionRetain}}); err != nil {
		t.Fatalf("re-declaring identical mapping should be a no-op, got %v", err)
	}
}

func TestForwardAndReverseView(t *testing.T) {
	d := NewDeclaration()
	if err := d.Amend([]Mapping{
		{Property: "name", Action: ActionRetain},
		{Property: "nick", Action: ActionDeprecate},
		{Property: "age", Action: ActionAddDefault, Default: 18},
	}); err != nil {
		t.Fatal(err)
	}
	old := map[string]any{"name": "ada", "nick": "a", "extra": true} // extra 未声明,默认保留
	got := d.ForwardView(old)
	want := map[string]any{"name": "ada", "extra": true, "age": 18}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("forward view = %v, want %v", got, want)
	}
	if _, ok := old["age"]; ok {
		t.Fatalf("forward view must not mutate input")
	}
	back := d.ReverseView(got)
	wantBack := map[string]any{"name": "ada", "extra": true}
	if !reflect.DeepEqual(back, wantBack) {
		t.Fatalf("reverse view = %v, want %v", back, wantBack)
	}
}

// TestForwardViewCostIndependentOfAmendments 以可验证的方式证明:
// 现算新版本视图的开销只与实例自身属性数量相关,
// 不随迁移声明累积的历史变更次数增长。
func TestForwardViewCostIndependentOfAmendments(t *testing.T) {
	d := NewDeclaration()
	if err := d.Amend([]Mapping{
		{Property: "name", Action: ActionRetain},
		{Property: "age", Action: ActionAddDefault, Default: 0},
	}); err != nil {
		t.Fatal(err)
	}
	instance := map[string]any{"name": "ada", "extra1": 1, "extra2": 2}

	_, baseline := d.ForwardViewStats(instance)
	t.Logf("依据: 合成映射为平坦表, 基线开销 ops=%d (扫描实例属性 %d + 新增默认值 %d)",
		baseline.Ops(), baseline.ScannedProps, baseline.AppliedDefaults)

	// 追加 2000 次历史变更,每次都引入一条与实例无关的废弃声明。
	const amendments = 2000
	for i := 0; i < amendments; i++ {
		p := "junk_" + string(rune('a'+i%26)) + "_" + string(rune('0'+i%10)) + "_" + itoa(i)
		if err := d.Amend([]Mapping{{Property: p, Action: ActionDeprecate}}); err != nil {
			t.Fatal(err)
		}
	}

	_, after := d.ForwardViewStats(instance)
	t.Logf("输入: 累积 %d 次历史变更后, 对同一实例现算新视图; 实际输出: ops=%d", amendments, after.Ops())
	if after != baseline {
		t.Fatalf("view computation cost grew with amendments: baseline=%+v after=%+v", baseline, after)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}
