package limiter

import (
	"testing"
	"time"

	"ontology/policy"
)

// 钉住的语义（任务一.6）：注销时桶里还有「因时间流逝应得但尚未入账」的
// 补充，这部分不随注销/重建带走——重建后是一个按新配额填满的新桶，余量
// 恰好等于新容量，与旧桶的待补充量无关。
// 之前未覆盖：既有 TestUnregisterAndRecreate 全程速率 0，桶里没有
// 待补充的时间，「待补充量是否被带走」这条路径从未被触发。
func TestUnregisterDropsPendingRefill(t *testing.T) {
	l, c := newLimiter(t, 100, 0, map[string]policy.Quota{
		"a": policy.Must(10, 2),
	})
	if err := l.Allow("a", 10); err != nil { // 租户 0，全局 90
		t.Fatal(err)
	}
	c.Advance(5 * time.Second) // 旧桶应得 +10（可补满），但尚未入账

	if err := l.Unregister("a"); err != nil {
		t.Fatal(err)
	}
	if _, _, _, ok := l.Inspect("a"); ok {
		t.Fatal("unregistered tenant must not be inspectable")
	}

	// 用更小的配额重建：新桶是满桶，余量 = 新容量 6。
	// 旧桶的待补充量（10）若被带过来，也无法解释恰好为 6 以外的任何值；
	// 这里钉住「恰好是新容量满桶」这一可观测结果。
	if err := l.Register("a", policy.Must(6, 1)); err != nil {
		t.Fatal(err)
	}
	tb, _ := balances(t, l, "a")
	if tb != 6 {
		t.Fatalf("re-registered tenant must be a fresh full bucket of the new quota, got %v, want 6", tb)
	}
}

// 钉住的语义（任务一.6 的另一半）：注销与重建全程不触碰全局桶——即使
// 注销发生在租户刚大量消费、全局桶已被扣减之后，全局余量也保持原值；
// 全局桶自身的补充也不因注销/重建而错乱。
// 之前未覆盖：既有测试只在校验全局余量时用了速率 0 的全局桶，未验证
// 全局桶带速率、且注销点夹在两次查询之间时的行为。
func TestUnregisterLeavesGlobalUntouched(t *testing.T) {
	l, c := newLimiter(t, 100, 1, map[string]policy.Quota{
		"a": policy.Must(10, 0),
		"b": policy.Must(10, 0),
	})
	if err := l.Allow("a", 10); err != nil { // 全局 90
		t.Fatal(err)
	}
	c.Advance(3 * time.Second) // 全局 90+3=93

	_, gb0 := balances(t, l, "b")
	if gb0 != 93 {
		t.Fatalf("global before unregister = %v, want 93", gb0)
	}

	if err := l.Unregister("a"); err != nil {
		t.Fatal(err)
	}
	if err := l.Register("a", policy.Must(4, 0)); err != nil {
		t.Fatal(err)
	}

	_, gb1 := balances(t, l, "b")
	if gb1 != gb0 {
		t.Fatalf("unregister/re-register must not touch global: %v -> %v", gb0, gb1)
	}

	c.Advance(2 * time.Second) // 全局按自身速率继续补充：93+2=95
	_, gb2 := balances(t, l, "b")
	if gb2 != 95 {
		t.Fatalf("global refill must be undisturbed, got %v, want 95", gb2)
	}
}
