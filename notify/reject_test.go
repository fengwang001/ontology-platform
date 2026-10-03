package notify

import (
	"errors"
	"testing"

	"ontology/alertstore"
	"ontology/suppress"
)

// errKind 把错误归类为可比较的类别，供对照测试使用。
func errKind(err error) string {
	switch {
	case err == nil:
		return "nil"
	case errors.Is(err, ErrInvalid):
		return "invalid"
	case errors.Is(err, ErrClock):
		return "clock"
	case errors.Is(err, alertstore.ErrFull):
		return "full"
	case errors.Is(err, alertstore.ErrNotFiring):
		return "notfiring"
	case errors.Is(err, suppress.ErrConflict):
		return "conflict"
	case errors.Is(err, suppress.ErrNotFound):
		return "notfound"
	}
	return "other"
}

// 活跃数（firing + 未吸收 resolved）达 Amax 后新建一律拒绝，不驱逐既有告警。
func TestLimitReject(t *testing.T) {
	n := mustNotifier(t, nil, 0, 1000, 2, nil)
	a1 := labels("name", "l1")
	a2 := labels("name", "l2")
	a3 := labels("name", "l3")
	must(t, n.Fire(0, a1))
	must(t, n.Fire(0, a2))
	if err := n.Fire(1, a3); !errors.Is(err, alertstore.ErrFull) {
		t.Fatalf("Fire over limit = %v, want ErrFull", err)
	}
	must(t, n.Resolve(2, a1))
	t.Log("resolved 未吸收仍占名额，Fire(a3) 继续拒绝")
	if err := n.Fire(3, a3); !errors.Is(err, alertstore.ErrFull) {
		t.Fatalf("Fire with retained resolved = %v, want ErrFull", err)
	}
	must(t, n.Fire(4, a1)) // 原地 refire 不占新名额
	rc := &recorder{fail: map[int]bool{}}
	must(t, n.Resolve(5, a1))
	tick(t, n, 6, rc) // a1 resolved 不在 lastSet，直接清除，腾出名额
	must(t, n.Fire(7, a3))
	if n.store.Len() != 2 {
		t.Fatalf("active = %d, want 2", n.store.Len())
	}
}

// 被拒绝的操作不改任何状态，含 Deduped 与时钟。
func TestRejectKeepsState(t *testing.T) {
	n := mustNotifier(t, nil, 0, 1000, 2, nil)
	a1 := labels("name", "r1")
	a2 := labels("name", "r2")
	a3 := labels("name", "r3")
	fp1 := alertstore.Fingerprint(a1)
	must(t, n.Fire(10, a1))
	must(t, n.Fire(10, a1)) // Deduped=1

	t.Log("参数非法优先于时钟回退：坏标签 + now 回退 仍报 ErrInvalid")
	if err := n.Fire(5, labels()); !errors.Is(err, ErrInvalid) {
		t.Fatalf("want ErrInvalid, got %v", err)
	}
	t.Log("时钟回退优先于状态类：now 回退 + 超限 仍报 ErrClock")
	must(t, n.Fire(10, a2)) // 满员
	if err := n.Fire(8, a3); !errors.Is(err, ErrClock) {
		t.Fatalf("want ErrClock, got %v", err)
	}
	if err := n.Fire(20, a3); !errors.Is(err, alertstore.ErrFull) {
		t.Fatalf("want ErrFull, got %v", err)
	}
	if err := n.Resolve(21, a3); !errors.Is(err, alertstore.ErrNotFiring) {
		t.Fatalf("want ErrNotFiring, got %v", err)
	}
	if err := n.AddSilence(22, "s1", suppress.Matchers{"a": "b"}, 5, 5); !errors.Is(err, ErrInvalid) {
		t.Fatalf("want ErrInvalid for empty window, got %v", err)
	}
	if err := n.ExpireSilence(23, "nope"); !errors.Is(err, suppress.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	t.Log("上述拒绝均未推进时钟：now=24 之后 now=10 仍属回退")
	if _, err := n.Tick(24, func(Notification) error { return nil }); err != nil {
		t.Fatalf("Tick(24): %v", err)
	}
	if err := n.Fire(10, a1); !errors.Is(err, ErrClock) {
		t.Fatalf("want ErrClock after Tick(24), got %v", err)
	}
	if got := n.store.Get(fp1).Deduped; got != 1 {
		t.Fatalf("Deduped = %d, want 1（被拒操作不得改状态）", got)
	}
	if n.clock != 24 {
		t.Fatalf("clock = %d, want 24", n.clock)
	}
	if _, err := n.Tick(25, nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("nil send should be ErrInvalid, got %v", err)
	}
}

// 构造参数校验。
func TestNewValidation(t *testing.T) {
	cases := []struct {
		name  string
		g     []string
		w, r  int64
		amax  int
		rules []suppress.Rule
	}{
		{"wait 超界", nil, 1e9 + 1, 0, 1, nil},
		{"repeat 为负", nil, 0, -1, 1, nil},
		{"amax 为零", nil, 0, 0, 0, nil},
		{"amax 超界", nil, 0, 0, 1e5 + 1, nil},
		{"组名空", []string{""}, 0, 0, 1, nil},
		{"规则源为空", nil, 0, 0, 1, []suppress.Rule{{Source: suppress.Matchers{}, Target: suppress.Matchers{"a": "b"}}}},
		{"规则目标超 8 对", nil, 0, 0, 1, []suppress.Rule{{
			Source: suppress.Matchers{"a": "b"},
			Target: suppress.Matchers{"1": "v", "2": "v", "3": "v", "4": "v", "5": "v", "6": "v", "7": "v", "8": "v", "9": "v"},
		}}},
	}
	for _, c := range cases {
		if _, err := New(c.g, c.w, c.r, c.amax, c.rules); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: got %v, want ErrInvalid", c.name, err)
		}
	}
}

// 告警参数校验：标签 1..16 对、键值非空且不超过 64 字节、now 不超 1e12。
func TestLabelsValidation(t *testing.T) {
	n := mustNotifier(t, nil, 0, 0, 10, nil)
	big := make(alertstore.Labels, 17)
	for i := 0; i < 17; i++ {
		big[string(rune('a'+i))] = "v"
	}
	if err := n.Fire(0, big); !errors.Is(err, ErrInvalid) {
		t.Fatalf("17 labels: %v", err)
	}
	if err := n.Fire(0, labels("k", "")); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty value: %v", err)
	}
	long := string(make([]byte, 65))
	if err := n.Fire(0, labels(long, "v")); !errors.Is(err, ErrInvalid) {
		t.Fatalf("65-byte key: %v", err)
	}
	if err := n.Fire(1_000_000_000_001, labels("k", "v")); !errors.Is(err, ErrInvalid) {
		t.Fatalf("now > 1e12: %v", err)
	}
}
