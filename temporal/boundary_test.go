package temporal

import (
	"context"
	"reflect"
	"testing"
)

// 相同生效时刻的多条变更按 (生效, 提交, ID) 全序叠加。
func TestSameEffectiveTieBreaks(t *testing.T) {
	ctx := context.Background()
	e := NewEngine(0, nil)
	// 同生效时刻 10，提交时刻也相同 5，最终由固有 ID 决定顺序。
	mustSubmit(t, e, Change{ID: "z", Subject: "u", Label: "l", Kind: Deny,
		Committed: 5, Effective: 10})
	mustSubmit(t, e, Change{ID: "a", Subject: "u", Label: "l", Kind: Grant,
		Committed: 5, Effective: 10})
	mustSubmit(t, e, Change{ID: "m", Subject: "u", Label: "l", Kind: Deny,
		Committed: 5, Effective: 10})
	// 全序为 a(grant) -> m(deny) -> z(deny)，末条存活为 z => deny。
	d, _ := e.Decide(ctx, "u", "l", 10)
	if d.Allowed || d.Default {
		t.Fatalf("ID tie-break expected deny, got %+v", d)
	}
	// 提交时刻优先于 ID：更早提交的 grant 即使 ID 更大也排在前面，
	// 但它之后仍有更晚提交的 deny 生效，最终仍为 deny；这里改用隔离桶验证。
	e2 := NewEngine(0, nil)
	mustSubmit(t, e2, Change{ID: "late-id", Subject: "u", Label: "l", Kind: Grant,
		Committed: 3, Effective: 10}) // 更早提交
	mustSubmit(t, e2, Change{ID: "early-id", Subject: "u", Label: "l", Kind: Deny,
		Committed: 8, Effective: 10}) // 更晚提交
	// 顺序 late-id(grant) -> early-id(deny)，deny 在后 => deny。
	d2, _ := e2.Decide(ctx, "u", "l", 10)
	if d2.Allowed {
		t.Fatalf("commit tie-break expected deny, got %+v", d2)
	}
}

// 历史查询可重复读：规则集合不再变化后，同一历史时刻多次查询完全一致，
// 且严格忽略提交时刻或生效时刻晚于该时刻的变更。
func TestHistoricalRepeatableRead(t *testing.T) {
	ctx := context.Background()
	e := NewEngine(0, nil)
	mustSubmit(t, e, Change{ID: "g1", Subject: "u", Label: "l", Kind: Grant,
		Committed: 1, Effective: 5})
	mustSubmit(t, e, Change{ID: "g2", Subject: "u", Label: "l", Kind: Deny,
		Committed: 20, Effective: 20})

	var first *Decision
	for i := 0; i < 5; i++ {
		d, err := e.Decide(ctx, "u", "l", 10)
		if err != nil {
			t.Fatal(err)
		}
		if first == nil {
			first = d
		} else if !reflect.DeepEqual(first, d) {
			t.Fatalf("non-repeatable historical read: %+v vs %+v", first, d)
		}
	}
	if !first.Allowed || first.Examined != 1 {
		t.Fatalf("history must ignore future changes, got %+v", first)
	}
	// 即使后来排队了新变更，历史时刻 10 的视图依旧只看到 g1。
	mustSubmit(t, e, Change{ID: "queued", Subject: "u", Label: "l", Kind: Deny,
		Committed: 21, Effective: 30})
	d, _ := e.Decide(ctx, "u", "l", 10)
	if !reflect.DeepEqual(first.Basis, d.Basis) || d.Examined != 1 {
		t.Fatalf("history changed after new submission: %+v vs %+v", first, d)
	}
}

// 排队中撤回：撤回后对任何时刻都不再产生影响。
func TestQueuedWithdrawInvisible(t *testing.T) {
	ctx := context.Background()
	e := NewEngine(0, nil)
	mustSubmit(t, e, Change{ID: "g1", Subject: "u", Label: "l", Kind: Grant,
		Committed: 1, Effective: 10})
	mustSubmit(t, e, Change{ID: "g2", Subject: "u", Label: "l", Kind: Deny,
		Committed: 1, Effective: 20})
	if err := e.Withdraw(ctx, "g2", 5); err != nil {
		t.Fatal(err)
	}
	d, _ := e.Decide(ctx, "u", "l", 25)
	if !d.Allowed {
		t.Fatalf("withdrawn deny should never take effect, got %+v", d)
	}
	// 撤回不存在或重复撤回 => 通用错误，且状态不变。
	if err := e.Withdraw(ctx, "g2", 6); err == nil {
		t.Fatal("double withdraw must fail")
	}
}

// 已生效变更的追溯撤销：沿 dependsOn 传递，牵动后续变更重新考察。
func TestRetroactiveRevokeCascade(t *testing.T) {
	ctx := context.Background()
	e := NewEngine(0, nil)
	mustSubmit(t, e, Change{ID: "base", Subject: "u", Label: "l", Kind: Grant,
		Committed: 1, Effective: 2})
	// 后续变更在内容上依赖 base 确立的状态。
	mustSubmit(t, e, Change{ID: "dependent", Subject: "u", Label: "l", Kind: Deny,
		Committed: 3, Effective: 4, DependsOn: "base"})
	// 独立变更不依赖 base。
	mustSubmit(t, e, Change{ID: "independent", Subject: "u", Label: "l", Kind: Grant,
		Committed: 3, Effective: 3})

	// 撤销前：dependent 的 deny 是最后规则 => deny。
	d, _ := e.Decide(ctx, "u", "l", 6)
	if d.Allowed {
		t.Fatalf("pre-revoke expected deny, got %+v", d)
	}

	res, err := e.Submit(ctx, Change{ID: "rev", Subject: "u", Label: "l",
		Kind: Revoke, Committed: 7, Effective: 8, Target: "base"})
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]Reassessment{}
	for _, r := range res.Reassessment {
		byID[r.ChangeID] = r
	}
	dr, ok := byID["dependent"]
	if !ok || dr.StillValid || !dr.EffectChanged {
		t.Fatalf("dependent must be invalidated by cascade: %+v", byID)
	}
	if _, touched := byID["independent"]; touched {
		t.Fatalf("independent change must not be reassessed: %+v", byID)
	}

	// 新基线下：base 与 dependent 均失效，independent 的 grant 成为最后规则。
	d, _ = e.Decide(ctx, "u", "l", 9)
	if !d.Allowed {
		t.Fatalf("post-revoke expected independent grant, got %+v", d)
	}
	// 撤销生效之前的历史视图保持原状（新基线从撤销生效时刻起）。
	d, _ = e.Decide(ctx, "u", "l", 6)
	if d.Allowed {
		t.Fatalf("history before revoke effective must be preserved, got %+v", d)
	}
}

// 组合边界：同生效时刻叠加 + 排队撤回 + 追溯撤销 + 历史复读。
func TestCombinedBoundary(t *testing.T) {
	ctx := context.Background()
	e := NewEngine(0, nil)
	mustSubmit(t, e, Change{ID: "b", Subject: "u", Label: "l", Kind: Grant,
		Committed: 1, Effective: 2})
	mustSubmit(t, e, Change{ID: "q1", Subject: "u", Label: "l", Kind: Deny,
		Committed: 2, Effective: 9})
	mustSubmit(t, e, Change{ID: "q2", Subject: "u", Label: "l", Kind: Grant,
		Committed: 2, Effective: 9})
	if err := e.Withdraw(ctx, "q1", 3); err != nil {
		t.Fatal(err)
	}
	// t=9 时同刻只剩 q2(grant)。
	d, _ := e.Decide(ctx, "u", "l", 9)
	if !d.Allowed {
		t.Fatalf("expected grant from q2, got %+v", d)
	}
	// 在 12 时刻撤销 b（已生效），q2 不依赖 b，故仍成立。
	res, err := e.Submit(ctx, Change{ID: "r", Subject: "u", Label: "l",
		Kind: Revoke, Committed: 11, Effective: 12, Target: "b"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Reassessment) != 0 {
		t.Fatalf("no dependent changes expected, got %+v", res.Reassessment)
	}
	d, _ = e.Decide(ctx, "u", "l", 12)
	if !d.Allowed {
		t.Fatalf("q2 should remain valid, got %+v", d)
	}
	// 历史 t=9 不受追溯撤销影响（撤销 12 才生效）。
	d, _ = e.Decide(ctx, "u", "l", 9)
	if !d.Allowed || d.Examined != 2 {
		t.Fatalf("history at 9 drifted: %+v", d)
	}
}
