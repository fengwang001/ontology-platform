package ontology

import (
	"errors"
	"reflect"
	"testing"
)

// logDecision 打印每次判定的输入、输出与依据。
func logDecision(t *testing.T, subject SubjectID, target ObjectTypeID, dec Decision, err error) {
	t.Helper()
	t.Logf("decide input=(subject=%s target=%s) output=(allowed=%v denied=%v reason=%s err=%v)",
		subject, target, dec.Allowed, dec.Denied, dec.Reason, err)
	for _, c := range dec.Contributions {
		t.Logf("  basis contribution: origin=%s remaining=%d actions=%v", c.Origin, c.Remaining, c.Actions)
	}
	for _, n := range dec.Notes {
		t.Logf("  basis note: %s", n)
	}
	t.Logf("  basis stats: %+v", dec.Stats)
}

func decide(t *testing.T, g *Gateway, subject SubjectID, target ObjectTypeID) (Decision, error) {
	t.Helper()
	dec, err := g.Decide(subject, target)
	logDecision(t, subject, target, dec, err)
	return dec, err
}

func addTypes(g *Gateway, ids ...ObjectTypeID) {
	for _, id := range ids {
		g.AddObjectType(id)
	}
}

func mustLink(t *testing.T, g *Gateway, id LinkTypeID, from, to ObjectTypeID, depth int) {
	t.Helper()
	lt := LinkType{ID: id, From: from, To: to, Propagates: depth > 0, MaxDepth: depth}
	if err := g.AddLinkType(lt); err != nil {
		t.Fatalf("AddLinkType(%s): %v", id, err)
	}
}

func mustGrant(t *testing.T, g *Gateway, sub SubjectID, typ ObjectTypeID, actions ...Action) {
	t.Helper()
	for _, a := range actions {
		if err := g.Grant(sub, typ, a); err != nil {
			t.Fatalf("Grant(%s,%s,%s): %v", sub, typ, a, err)
		}
	}
}

func mustAllow(t *testing.T, dec Decision, err error, want ...Action) {
	t.Helper()
	if err != nil {
		t.Fatalf("expected grant, got err=%v (reason=%s)", err, dec.Reason)
	}
	if !reflect.DeepEqual(dec.Allowed, want) {
		t.Fatalf("allowed=%v, want %v", dec.Allowed, want)
	}
}

func mustNoPermission(t *testing.T, dec Decision, err error, reason ReasonCode) {
	t.Helper()
	if !errors.Is(err, ErrNoPermission) {
		t.Fatalf("expected ErrNoPermission, got %v", err)
	}
	if dec.Reason != reason {
		t.Fatalf("reason=%s, want %s", dec.Reason, reason)
	}
	if len(dec.Allowed) != 0 {
		t.Fatalf("allowed=%v, want empty", dec.Allowed)
	}
}

// 环路检测：写路径拒绝成环的传播配置，且被拒绝的操作不改变
// 既有图结构与判定结果。
func TestCycleDetection(t *testing.T) {
	g := NewGateway(Config{})
	addTypes(g, "A", "B", "C")
	mustLink(t, g, "ab", "A", "B", 2)
	mustLink(t, g, "bc", "B", "C", 2)
	mustGrant(t, g, "alice", "A", "read")

	dec, err := decide(t, g, "alice", "C")
	mustAllow(t, dec, err, "read")

	// C->A 会构成 A->B->C->A 传播环，必须被拒绝。
	err = g.AddLinkType(LinkType{ID: "ca", From: "C", To: "A", Propagates: true, MaxDepth: 2})
	if !errors.Is(err, ErrPropagationCycle) {
		t.Fatalf("expected ErrPropagationCycle, got %v", err)
	}
	if errors.Is(err, ErrNoPermission) {
		t.Fatal("cycle error must be distinguishable from no-permission")
	}

	// 被拒绝的操作不改变既有判定结果。
	dec, err = decide(t, g, "alice", "C")
	mustAllow(t, dec, err, "read")

	// 自环同样是环。
	err = g.AddLinkType(LinkType{ID: "aa", From: "A", To: "A", Propagates: true, MaxDepth: 1})
	if !errors.Is(err, ErrPropagationCycle) {
		t.Fatalf("self-loop: expected ErrPropagationCycle, got %v", err)
	}

	// 不参与传播的链接（深度 0）可以在图中成环，不构成传播环。
	if err := g.AddLinkType(LinkType{ID: "ca0", From: "C", To: "A", MaxDepth: 0}); err != nil {
		t.Fatalf("non-propagating link may form a graph cycle: %v", err)
	}
}

// 判定路径上的防御性环路检测：即便状态被绕过写路径破坏，判定也
// 报“传播环路”而不是沿环静默传播。
func TestDecideRejectsCorruptedCycle(t *testing.T) {
	g := NewGateway(Config{})
	addTypes(g, "A", "B")
	mustLink(t, g, "ab", "A", "B", 2)
	mustGrant(t, g, "alice", "A", "read")

	// 白盒：绕过写路径直接注入成环配置。
	g.mu.Lock()
	g.linkTypes["ba"] = LinkType{ID: "ba", From: "B", To: "A", Propagates: true, MaxDepth: 2}
	g.mu.Unlock()

	_, err := decide(t, g, "alice", "B")
	if !errors.Is(err, ErrPropagationCycle) {
		t.Fatalf("expected ErrPropagationCycle, got %v", err)
	}
}

// 两种覆盖形态的行为差异：replace 用目标自身的直接授权替换传播
// 结果后继续向下游传播；replace-and-block 额外阻断下游。
func TestOverrideModesDiffer(t *testing.T) {
	build := func(mode OverrideMode) *Gateway {
		g := NewGateway(Config{})
		addTypes(g, "A", "B", "C")
		mustLink(t, g, "ab", "A", "B", 3)
		mustLink(t, g, "bc", "B", "C", 3)
		mustGrant(t, g, "alice", "A", "read")
		mustGrant(t, g, "alice", "B", "write")
		if err := g.SetOverride("B", mode); err != nil {
			t.Fatalf("SetOverride: %v", err)
		}
		return g
	}

	// replace：B 处传播来的 read 被替换为 B 自身的 write，write 继续
	// 传播到 C。
	g := build(OverrideReplace)
	dec, err := decide(t, g, "alice", "B")
	mustAllow(t, dec, err, "write")
	dec, err = decide(t, g, "alice", "C")
	mustAllow(t, dec, err, "write")

	// replace-and-block：B 自身同样只保留 write，但 C 一无所获。
	g = build(OverrideReplaceAndBlock)
	dec, err = decide(t, g, "alice", "B")
	mustAllow(t, dec, err, "write")
	dec, err = decide(t, g, "alice", "C")
	mustNoPermission(t, dec, err, ReasonNoGrant)
}

// 同一目标不得同时生效两种覆盖：追加冲突规则被拒绝且既有规则
// 不变。
func TestConflictingOverrides(t *testing.T) {
	g := NewGateway(Config{})
	addTypes(g, "T")
	if err := g.AddOverrideRule("T", OverrideReplace); err != nil {
		t.Fatalf("first rule: %v", err)
	}
	err := g.AddOverrideRule("T", OverrideReplaceAndBlock)
	if !errors.Is(err, ErrConflictingOverrides) {
		t.Fatalf("expected ErrConflictingOverrides, got %v", err)
	}
	// 既有规则未被改变。
	g.mu.RLock()
	mode := g.overrides["T"]
	g.mu.RUnlock()
	if mode != OverrideReplace {
		t.Fatalf("override mutated by rejected op: %v", mode)
	}
	// 同形态规则幂等。
	if err := g.AddOverrideRule("T", OverrideReplace); err != nil {
		t.Fatalf("idempotent rule: %v", err)
	}
}
