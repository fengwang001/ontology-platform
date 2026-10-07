package cascade

import "testing"

// replaySeed3 重放能触发 fgOwners 漂移的最小操作前缀（从差分轨迹人工缩减）。
func TestTraceFGDrift(t *testing.T) {
	c := New()
	mk := func(id string, owners []OwnerRef, fins ...string) {
		if err := c.Create(id, owners, fins); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}
	mk("a", nil)       // 顶层
	mk("m", nil, "fm") // 中间，带终结器
	mk("x", []OwnerRef{{OwnerID: "a"}, {OwnerID: "m", Blocking: true}})
	// a 后台移除：x 仍由 m 支撑，边 a<-x 摘除。
	if err := c.Delete("a", Background); err != nil {
		t.Fatal(err)
	}
	c.checkInvariants(t, "after a bg")
	// m 前台删除：x 传播前台，但 x 自身无阻塞物可与 m 同轮移除；
	// m 有终结器停留。x 的 fgOwners 应恰为 1。
	if err := c.Delete("m", Foreground); err != nil {
		t.Fatal(err)
	}
	c.checkInvariants(t, "after m fg")
	if xo := c.objects["x"]; xo != nil && xo.fgOwners != 1 {
		t.Fatalf("x.fgOwners=%d want 1", xo.fgOwners)
	}
	// 解除 m 终结器：x、m 依次移除。
	if err := c.RemoveFinalizer("m", "fm"); err != nil {
		t.Fatal(err)
	}
	c.checkInvariants(t, "after release")
}

func TestTraceFGDriftOrphanThenFG(t *testing.T) {
	c := New()
	mk := func(id string, owners []OwnerRef, fins ...string) {
		if err := c.Create(id, owners, fins); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}
	// m1 用 Orphan 删除：无终结器，立即移除，摘除 x->m1 的边但不连带删除 x；
	// m2 用 Foreground 带终结器删除（停留并前台传播）。
	mk("m1", nil)
	mk("m2", nil, "f2")
	mk("x", []OwnerRef{{OwnerID: "m1", Blocking: true}, {OwnerID: "m2", Blocking: true}}, "fx")

	if err := c.Delete("m1", Orphan); err != nil {
		t.Fatal(err)
	}
	c.checkInvariants(t, "m1 orphan")
	if err := c.Delete("m2", Foreground); err != nil {
		t.Fatal(err)
	}
	c.checkInvariants(t, "m2 fg")
	xo := c.objects["x"]
	if xo == nil {
		t.Fatalf("x should remain (m2 held by finalizer, x propagated)")
	}
	if xo.fgOwners != 1 || xo.aliveOwners != 1 {
		t.Fatalf("x counts got fg=%d alive=%d want 1/1", xo.fgOwners, xo.aliveOwners)
	}
}

// 属主前台删除（带终结器停留），非阻塞依赖者 x 无终结器，x 又有自己的
// 非阻塞依赖者 y。删除属主时 x 传播前台并在同轮移除，y 连带后台移除。
func TestTraceFGDependentRemovedSameRound(t *testing.T) {
	c := New()
	mk := func(id string, owners []OwnerRef, fins ...string) {
		if err := c.Create(id, owners, fins); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}
	mk("a", nil, "fa")
	mk("x", []OwnerRef{{OwnerID: "a", Blocking: false}})
	mk("y", []OwnerRef{{OwnerID: "x", Blocking: false}})

	if err := c.Delete("a", Foreground); err != nil {
		t.Fatal(err)
	}
	c.checkInvariants(t, "fg cascade same round")
	// a 有终结器停留；x、y 无终结器，同轮连带移除。
	if c.objects["a"] == nil || c.objects["x"] != nil || c.objects["y"] != nil {
		t.Fatalf("unexpected objects: a=%v x=%v y=%v",
			c.objects["a"] != nil, c.objects["x"] != nil, c.objects["y"] != nil)
	}
}
