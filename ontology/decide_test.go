package ontology

import (
	"errors"
	"reflect"
	"testing"
)

// 多条传播路径到达同一目标时取并集；目标声明覆盖后，覆盖结果
// 替换掉全部路径的并集，而不是与并集再合并。
func TestUnionThenOverrideReplaces(t *testing.T) {
	g := NewGateway(Config{})
	addTypes(g, "A1", "A2", "T")
	mustLink(t, g, "a1t", "A1", "T", 2)
	mustLink(t, g, "a2t", "A2", "T", 2)
	mustGrant(t, g, "alice", "A1", "read")
	mustGrant(t, g, "alice", "A2", "write")

	// 两条路径的并集。
	dec, err := decide(t, g, "alice", "T")
	mustAllow(t, dec, err, "read", "write")
	if len(dec.Contributions) != 2 {
		t.Fatalf("contributions=%v, want 2", dec.Contributions)
	}

	// 覆盖替换整个并集，只保留 T 自身的直接授权 admin。
	mustGrant(t, g, "alice", "T", "admin")
	if err := g.SetOverride("T", OverrideReplace); err != nil {
		t.Fatalf("SetOverride: %v", err)
	}
	dec, err = decide(t, g, "alice", "T")
	mustAllow(t, dec, err, "admin")

	// 若 T 自身没有直接授权，覆盖后并集被替换为空。
	g2 := NewGateway(Config{})
	addTypes(g2, "A1", "A2", "T")
	mustLink(t, g2, "a1t", "A1", "T", 2)
	mustLink(t, g2, "a2t", "A2", "T", 2)
	mustGrant(t, g2, "alice", "A1", "read")
	mustGrant(t, g2, "alice", "A2", "write")
	if err := g2.SetOverride("T", OverrideReplace); err != nil {
		t.Fatalf("SetOverride: %v", err)
	}
	dec, err = decide(t, g2, "alice", "T")
	mustNoPermission(t, dec, err, ReasonOverrideReplaced)
}

// 剩余深度恰为零的那一跳仍允许传播生效，再往外一跳则自然终止
// （不视为错误）。
func TestExactDepthExhaustionHop(t *testing.T) {
	g := NewGateway(Config{})
	addTypes(g, "A", "B", "C", "D")
	// A -2-> B -1-> C -1-> D：A->B 后剩 1；B->C 时 min(1,1)-1=0，
	// 恰好耗尽的那一跳仍生效；C 处剩余为 0，D 不可达。
	mustLink(t, g, "ab", "A", "B", 2)
	mustLink(t, g, "bc", "B", "C", 1)
	mustLink(t, g, "cd", "C", "D", 1)
	mustGrant(t, g, "alice", "A", "read")

	dec, err := decide(t, g, "alice", "B")
	mustAllow(t, dec, err, "read")
	dec, err = decide(t, g, "alice", "C")
	mustAllow(t, dec, err, "read")

	// D 不可达：深度自然耗尽，报 ErrNoPermission 而非配置错误，
	// 且与传播环路错误可区分。
	dec, err = decide(t, g, "alice", "D")
	mustNoPermission(t, dec, err, ReasonDepthExhausted)
	if errors.Is(err, ErrPropagationCycle) {
		t.Fatal("depth exhaustion must not be reported as cycle")
	}

	// 深度为 0 的链接不参与传播。
	g2 := NewGateway(Config{})
	addTypes(g2, "A", "B")
	mustLink(t, g2, "ab", "A", "B", 0)
	mustGrant(t, g2, "alice", "A", "read")
	dec, err = decide(t, g2, "alice", "B")
	mustNoPermission(t, dec, err, ReasonNoGrant)
}

// 显式直接授权（含显式否定项）始终优先于传播结果。
func TestExplicitGrantBeatsPropagation(t *testing.T) {
	g := NewGateway(Config{})
	addTypes(g, "A", "T")
	mustLink(t, g, "at", "A", "T", 2)
	mustGrant(t, g, "alice", "A", "read", "write")

	// 传播带来 read+write；显式否定 read 后只剩 write。
	if err := g.Deny("alice", "T", "read"); err != nil {
		t.Fatalf("Deny: %v", err)
	}
	dec, err := decide(t, g, "alice", "T")
	mustAllow(t, dec, err, "write")
	if !reflect.DeepEqual(dec.Denied, []Action{"read"}) {
		t.Fatalf("denied=%v, want [read]", dec.Denied)
	}

	// 显式允许一个传播未覆盖的动作，直接生效。
	mustGrant(t, g, "alice", "T", "admin")
	dec, err = decide(t, g, "alice", "T")
	mustAllow(t, dec, err, "admin", "write")

	// 全部动作都被显式否定：拒绝原因为显式拒绝，优先级高于
	// 覆盖阻断与深度耗尽。
	g2 := NewGateway(Config{})
	addTypes(g2, "A", "T")
	mustLink(t, g2, "at", "A", "T", 2)
	mustGrant(t, g2, "alice", "A", "read")
	if err := g2.Deny("alice", "T", "read"); err != nil {
		t.Fatalf("Deny: %v", err)
	}
	if err := g2.SetOverride("T", OverrideReplaceAndBlock); err != nil {
		t.Fatalf("SetOverride: %v", err)
	}
	dec, err = decide(t, g2, "alice", "T")
	mustNoPermission(t, dec, err, ReasonExplicitDeny)
}

// 错误类别可区分：对象类型不存在、非法深度、冲突覆盖、普通
// 无权限。
func TestErrorCategories(t *testing.T) {
	g := NewGateway(Config{MaxPropagationDepth: 4})
	addTypes(g, "A", "B")

	// 对象类型不存在（判定与变更两条路径）。
	_, err := decide(t, g, "alice", "ghost")
	if !errors.Is(err, ErrObjectTypeNotFound) {
		t.Fatalf("decide: expected ErrObjectTypeNotFound, got %v", err)
	}
	err = g.AddLinkType(LinkType{ID: "l", From: "A", To: "ghost", MaxDepth: 1})
	if !errors.Is(err, ErrObjectTypeNotFound) {
		t.Fatalf("add link: expected ErrObjectTypeNotFound, got %v", err)
	}
	if err := g.Grant("alice", "ghost", "read"); !errors.Is(err, ErrObjectTypeNotFound) {
		t.Fatalf("grant: expected ErrObjectTypeNotFound, got %v", err)
	}

	// 深度配置非法：负数与超出平台上限。
	for _, d := range []int{-1, 5, 100} {
		err = g.AddLinkType(LinkType{ID: "l", From: "A", To: "B", MaxDepth: d})
		if !errors.Is(err, ErrInvalidDepth) {
			t.Fatalf("depth %d: expected ErrInvalidDepth, got %v", d, err)
		}
	}

	// 普通无权限：与配置性错误区分。
	mustLink(t, g, "ab", "A", "B", 1)
	dec, err := decide(t, g, "alice", "B")
	mustNoPermission(t, dec, err, ReasonNoGrant)
	for _, cfgErr := range []error{ErrObjectTypeNotFound, ErrPropagationCycle, ErrInvalidDepth, ErrConflictingOverrides} {
		if errors.Is(err, cfgErr) {
			t.Fatalf("no-permission conflated with config error %v", cfgErr)
		}
	}
}

// 被拒绝的链接类型调整不改变既有图结构与判定结果。
func TestRejectedUpdateKeepsState(t *testing.T) {
	g := NewGateway(Config{})
	addTypes(g, "A", "B", "C")
	mustLink(t, g, "ab", "A", "B", 2)
	mustLink(t, g, "bc", "B", "C", 2)
	mustGrant(t, g, "alice", "A", "read")

	// 把 bc 改成 B->A 会构成 A->B->A 环：拒绝，且 bc 保持 B->C。
	err := g.UpdateLinkType(LinkType{ID: "bc", From: "B", To: "A", Propagates: true, MaxDepth: 2})
	if !errors.Is(err, ErrPropagationCycle) {
		t.Fatalf("expected ErrPropagationCycle, got %v", err)
	}
	dec, err := decide(t, g, "alice", "C")
	mustAllow(t, dec, err, "read")

	// 非法深度的更新同样被拒绝且原配置保留。
	err = g.UpdateLinkType(LinkType{ID: "bc", From: "B", To: "C", Propagates: true, MaxDepth: -3})
	if !errors.Is(err, ErrInvalidDepth) {
		t.Fatalf("expected ErrInvalidDepth, got %v", err)
	}
	dec, err = decide(t, g, "alice", "C")
	mustAllow(t, dec, err, "read")
}
