package temporal

import (
	"bytes"
	"context"
	"testing"
)

func TestSmoke(t *testing.T) {
	ctx := context.Background()
	buf := &bytes.Buffer{}
	e := NewEngine(0, NewAuditLog(buf))

	if _, err := e.Submit(ctx, Change{ID: "a", Subject: "u1", Label: "doc",
		Kind: Grant, Committed: 5, Effective: 10}); err != nil {
		t.Fatal(err)
	}
	// 生效时刻之前：排队中，不影响判定（无规则 => 默认拒绝）。
	d, err := e.Decide(ctx, "u1", "doc", 9)
	if err != nil || d.Allowed || !d.Default {
		t.Fatalf("before effective: %+v err=%v", d, err)
	}
	// 跨过生效时刻：必须按新规则。
	d, _ = e.Decide(ctx, "u1", "doc", 10)
	if !d.Allowed || d.Default {
		t.Fatalf("at effective: %+v", d)
	}
	if buf.Len() == 0 {
		t.Fatal("audit log empty")
	}
}

func TestErrorPriorities(t *testing.T) {
	ctx := context.Background()
	e := NewEngine(10, nil)

	// 优先级 1：生效早于提交。
	_, err := e.Submit(ctx, Change{ID: "x", Kind: Grant, Committed: 5, Effective: 4})
	codeMust(t, err, ErrCodeEffectiveBeforeCommit)

	// 优先级 2：查询早于最早记录时刻。
	_, err = e.Decide(ctx, "u", "l", 3)
	codeMust(t, err, ErrCodeQueryBeforeHorizon)

	// 优先级 3：撤回已生效变更。
	mustSubmit(t, e, Change{ID: "p", Subject: "u", Label: "l",
		Kind: Grant, Committed: 10, Effective: 10})
	err = e.Withdraw(ctx, "p", 11)
	codeMust(t, err, ErrCodeWithdrawAlreadyEffective)

	// 被拒绝的提交/撤回不改变状态：仍然允许。
	d, _ := e.Decide(ctx, "u", "l", 11)
	if !d.Allowed {
		t.Fatalf("state mutated by rejected calls: %+v", d)
	}
}

func mustSubmit(t *testing.T, e *Engine, c Change) {
	t.Helper()
	if _, err := e.Submit(context.Background(), c); err != nil {
		t.Fatalf("submit %s: %v", c.ID, err)
	}
}

func codeMust(t *testing.T, err error, want Code) {
	t.Helper()
	de, ok := AsDomainError(err)
	if !ok {
		t.Fatalf("want domain error %s, got %v", want, err)
	}
	if de.Code != want {
		t.Fatalf("want code %s, got %s", want, de.Code)
	}
}
