package limiter_test

import (
	"errors"
	"testing"

	"ontology/limiter"
	"ontology/rule"
)

func mustAdd(t *testing.T, l *limiter.Limiter, id string, pattern []rule.Pair, limit, w int64, mode rule.Mode) {
	t.Helper()
	if err := l.AddRule(id, pattern, limit, w, mode); err != nil {
		t.Fatalf("AddRule(%s): %v", id, err)
	}
}

func eqStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestEstBoundary(t *testing.T) {
	// est+1 恰等于 L 放行；大 1 不放行。
	l := limiter.New()
	mustAdd(t, l, "r", []rule.Pair{{Key: "route", Value: "/p"}}, 3, 100, rule.Enforce)
	desc := rule.Descriptor{"route": "/p"}
	// L=3：est 0,1,2 时放行三次（est+1 <= 3），第四次 est=3 => 4>3 拒绝。
	for i := 0; i < 3; i++ {
		d, err := l.Allow(desc, int64(10+i))
		if err != nil || !d.Allowed || d.RejectedBy != "" {
			t.Fatalf("req %d: d=%+v err=%v", i, d, err)
		}
	}
	d, err := l.Allow(desc, 13)
	if err != nil || d.Allowed || d.RejectedBy != "r" {
		t.Fatalf("4th: d=%+v err=%v", d, err)
	}
}

func TestRollDifference(t *testing.T) {
	// k'=k+1 prev 保留并加权；k'>=k+2 全部清零。
	l := limiter.New()
	mustAdd(t, l, "r", []rule.Pair{{Key: "k", Value: "*"}}, 10, 100, rule.Enforce)
	desc := rule.Descriptor{"k": "t1"}
	for i := 0; i < 6; i++ {
		if _, err := l.Allow(desc, 50); err != nil {
			t.Fatal(err)
		}
	}
	// now=200：k'=2=k+1，prev=6,cur=0，边界全额 est=6。
	d, err := l.Allow(desc, 200)
	if err != nil || !d.Allowed {
		t.Fatalf("handoff: d=%+v err=%v", d, err)
	}
	// now=300（对新计数器 k=2 而言 k'=3=k+1，prev=1）——改为直接测清空：
	// 等待两个窗口以上：last incr 在 200(窗口2)，跳到 400(窗口4>=k+2)。
	d, err = l.Allow(desc, 400)
	if err != nil || !d.Allowed {
		t.Fatalf("two-window gap: d=%+v err=%v", d, err)
	}
}

func TestWildcardBuckets(t *testing.T) {
	// 通配 {tenant:*} 按实际取值各自计数。
	l := limiter.New()
	mustAdd(t, l, "r", []rule.Pair{{Key: "tenant", Value: "*"}}, 10, 100, rule.Enforce)
	if _, err := l.Allow(rule.Descriptor{"tenant": "t1"}, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Allow(rule.Descriptor{"tenant": "t2"}, 0); err != nil {
		t.Fatal(err)
	}
	if got := l.Tracked(); got != 2 {
		t.Fatalf("Tracked=%d want 2", got)
	}
	// t2 的桶不受 t1 影响：继续在 t1 打满，t2 仍应放行。
	for i := 0; i < 9; i++ {
		l.Allow(rule.Descriptor{"tenant": "t1"}, 0)
	}
	d, err := l.Allow(rule.Descriptor{"tenant": "t1"}, 0)
	if err != nil || d.Allowed {
		t.Fatalf("t1 should be saturated: %+v", d)
	}
	d, err = l.Allow(rule.Descriptor{"tenant": "t2"}, 0)
	if err != nil || !d.Allowed {
		t.Fatalf("t2 must stay independent: %+v", d)
	}
}

func TestSelectionAndOverlay(t *testing.T) {
	// 精确值对数多者胜、并列取 id 小者、不同键集合叠加。
	l := limiter.New()
	mustAdd(t, l, "a", []rule.Pair{{Key: "tenant", Value: "*"}, {Key: "route", Value: "/pay"}}, 100, 100, rule.Enforce)
	mustAdd(t, l, "b", []rule.Pair{{Key: "tenant", Value: "vip"}, {Key: "route", Value: "*"}}, 100, 100, rule.Enforce)
	mustAdd(t, l, "d", []rule.Pair{{Key: "route", Value: "/pay"}}, 100, 100, rule.Enforce)
	desc := rule.Descriptor{"tenant": "vip", "route": "/pay"}
	d, err := l.Allow(desc, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !eqStrings(d.Selected, []string{"a", "d"}) {
		t.Fatalf("selected=%v want [a d]", d.Selected)
	}
	// 加入更具体的 c（2 个精确值），同键集合中 a/b 全被压制。
	mustAdd(t, l, "c", []rule.Pair{{Key: "tenant", Value: "vip"}, {Key: "route", Value: "/pay"}}, 100, 100, rule.Enforce)
	d, _ = l.Allow(desc, 1)
	if !eqStrings(d.Selected, []string{"c", "d"}) {
		t.Fatalf("selected=%v want [c d]", d.Selected)
	}
}

func TestRejectDoesNotAdvanceOthers(t *testing.T) {
	// 被强制拒绝时：任何规则（含影子）计数不推进，不记影子；
	// 报告字节序最小的不放行强制规则。
	l := limiter.New()
	mustAdd(t, l, "r2", []rule.Pair{{Key: "route", Value: "/p"}}, 1, 100, rule.Enforce)
	mustAdd(t, l, "r1", []rule.Pair{{Key: "route", Value: "/p"}, {Key: "tenant", Value: "*"}}, 1, 100, rule.Enforce)
	mustAdd(t, l, "s", []rule.Pair{{Key: "tenant", Value: "*"}}, 100, 100, rule.Shadow)
	desc := rule.Descriptor{"route": "/p", "tenant": "v"}
	d, err := l.Allow(desc, 0)
	if err != nil || !d.Allowed {
		t.Fatalf("first: %+v %v", d, err)
	}
	if !eqStrings(d.Selected, []string{"r1", "r2", "s"}) {
		t.Fatalf("selected=%v want [r1 r2 s]", d.Selected)
	}
	trackedAfterFirst := l.Tracked()
	// 第二次：r1 超限 => 整体拒绝，报告 r1；s 是影子不参与拒绝，且不计数。
	d, err = l.Allow(desc, 1)
	if err != nil || d.Allowed || d.RejectedBy != "r1" {
		t.Fatalf("second: %+v %v", d, err)
	}
	if len(d.ShadowReject) != 0 {
		t.Fatalf("shadow stats must not move on enforce reject, got %v", d.ShadowReject)
	}
	if l.Tracked() != trackedAfterFirst {
		t.Fatalf("counters advanced on reject: %d -> %d", trackedAfterFirst, l.Tracked())
	}
	if l.ShadowRejectCount("s") != 0 {
		t.Fatal("ShadowReject must stay 0")
	}
}

func TestShadowFlow(t *testing.T) {
	// 题给影子例：s 影子 L=1，r 强制 L=2。
	l := limiter.New()
	mustAdd(t, l, "s", []rule.Pair{{Key: "route", Value: "*"}, {Key: "tenant", Value: "*"}}, 1, 100, rule.Shadow)
	mustAdd(t, l, "r", []rule.Pair{{Key: "route", Value: "/p"}}, 2, 100, rule.Enforce)
	desc := rule.Descriptor{"route": "/p", "tenant": "v"}

	d, _ := l.Allow(desc, 0)
	if !d.Allowed || len(d.ShadowReject) != 0 {
		t.Fatalf("t0: %+v", d)
	}
	// now=1：s est=1 超限（影子），r est=1 放行 => 整体放行。
	d, _ = l.Allow(desc, 1)
	if !d.Allowed || !eqStrings(d.ShadowReject, []string{"s"}) {
		t.Fatalf("t1: %+v", d)
	}
	if l.ShadowRejectCount("s") != 1 {
		t.Fatalf("shadow reject count=%d", l.ShadowRejectCount("s"))
	}
	// now=2：r est=2 => 2+1>2 强制拒绝；s 计数与影子统计不变。
	d, _ = l.Allow(desc, 2)
	if d.Allowed || d.RejectedBy != "r" {
		t.Fatalf("t2: %+v", d)
	}
	if l.ShadowRejectCount("s") != 1 {
		t.Fatal("shadow stats changed on enforce reject")
	}

	// s 切为强制，沿用原计数（s.cur=2=L+1）：此时 s 与 r 都超限，
	// 字节序 r<s，报告 r；等窗口滚动让 r 恢复后，s 仍凭沿用计数拒绝。
	if err := l.SetMode("s", rule.Enforce); err != nil {
		t.Fatal(err)
	}
	d, _ = l.Allow(desc, 3)
	if d.Allowed || d.RejectedBy != "r" {
		t.Fatalf("after setmode both reject: %+v", d)
	}
	// 跳到窗口 1 内：r 的 W=100，在 now=150（k+1）prev=2 加权衰减后
	// r 恢复放行；s（L=1）prev=2，est=prev*50/100=1，1+1>1 仍拒绝。
	d, _ = l.Allow(desc, 150)
	if d.Allowed || d.RejectedBy != "s" {
		t.Fatalf("promoted s must reject from inherited count: %+v", d)
	}
}

func TestSetModeKeepsCountersAndRemove(t *testing.T) {
	l := limiter.New()
	mustAdd(t, l, "s", []rule.Pair{{Key: "route", Value: "/p"}}, 1, 1000, rule.Shadow)
	desc := rule.Descriptor{"route": "/p"}
	l.Allow(desc, 0)
	l.Allow(desc, 1) // s.cur=2（封顶 L+1）
	before := l.Tracked()
	if err := l.SetMode("s", rule.Enforce); err != nil {
		t.Fatal(err)
	}
	if l.Tracked() != before {
		t.Fatal("SetMode must not reset counters")
	}
	// 沿用计数：est=2，2+1>1 => s 立刻拒绝。
	d, _ := l.Allow(desc, 2)
	if d.Allowed || d.RejectedBy != "s" {
		t.Fatalf("promoted shadow should reject with inherited count: %+v", d)
	}
	// RemoveRule 清空该规则全部计数器。
	if err := l.RemoveRule("s"); err != nil {
		t.Fatal(err)
	}
	if l.Tracked() != 0 {
		t.Fatalf("after remove tracked=%d want 0", l.Tracked())
	}
	if err := l.RemoveRule("s"); !errors.Is(err, limiter.ErrNotFound) {
		t.Fatalf("remove again err=%v want ErrNotFound", err)
	}
}

func TestErrorsAndClock(t *testing.T) {
	l := limiter.New()
	pat := []rule.Pair{{Key: "k", Value: "v"}}
	mustAdd(t, l, "r", pat, 5, 100, rule.Enforce)

	// AddRule 优先级：参数非法 > id 已存在 > 模式重复。
	if err := l.AddRule("", pat, 5, 100, rule.Enforce); !errors.Is(err, limiter.ErrInvalid) {
		t.Fatalf("empty id: %v", err)
	}
	if err := l.AddRule("r", []rule.Pair{{Key: "k2", Value: "v"}}, 5, 100, rule.Enforce); !errors.Is(err, limiter.ErrExists) {
		t.Fatalf("exists: %v", err)
	}
	if err := l.AddRule("r2", pat, 5, 100, rule.Enforce); !errors.Is(err, limiter.ErrDuplicate) {
		t.Fatalf("dup pattern: %v", err)
	}
	if err := l.SetMode("nope", rule.Enforce); !errors.Is(err, limiter.ErrNotFound) {
		t.Fatalf("setmode missing: %v", err)
	}

	// Allow 优先级：描述符非法 > 时间非法 > 时钟回退。
	if _, err := l.Allow(rule.Descriptor{}, 0); !errors.Is(err, limiter.ErrInvalid) {
		t.Fatalf("empty desc: %v", err)
	}
	if _, err := l.Allow(rule.Descriptor{"k": ""}, 0); !errors.Is(err, limiter.ErrInvalid) {
		t.Fatalf("empty value: %v", err)
	}
	desc := rule.Descriptor{"k": "v"}
	if _, err := l.Allow(desc, -1); !errors.Is(err, limiter.ErrTime) {
		t.Fatalf("neg now: %v", err)
	}
	if _, err := l.Allow(desc, 1_000_000_000_000_001); !errors.Is(err, limiter.ErrTime) {
		t.Fatalf("huge now: %v", err)
	}
	l.Allow(desc, 10)
	if _, err := l.Allow(desc, 9); !errors.Is(err, limiter.ErrClockGoBack) {
		t.Fatalf("clock back: %v", err)
	}
	// 回退被拒不改变 maxNow：10 之后 11 仍合法。
	if _, err := l.Allow(desc, 11); err != nil {
		t.Fatalf("11 should be accepted after rejected rollback: %v", err)
	}
}

func TestNoRuleSelected(t *testing.T) {
	l := limiter.New()
	mustAdd(t, l, "r", []rule.Pair{{Key: "k", Value: "v"}}, 1, 100, rule.Enforce)
	d, err := l.Allow(rule.Descriptor{"other": "x"}, 0)
	if err != nil || !d.Allowed || len(d.Selected) != 0 || l.Tracked() != 0 {
		t.Fatalf("no match: d=%+v err=%v tracked=%d", d, err, l.Tracked())
	}
}
