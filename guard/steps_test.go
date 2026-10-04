package guard

import "testing"

func TestStepsNoDevicesZeroRegardlessOfGap(t *testing.T) {
	g := New(0, 0, 0, 3600, 3600, 600, 8)
	g.Register("a")
	// 无设备在线：相隔 1 天推进 0 步。
	if _, err := g.Remaining(86400, "a"); err != nil {
		t.Fatal(err)
	}
	if s := g.Steps("a"); s != 0 {
		t.Fatalf("idle 1 day steps=%d want 0", s)
	}
	// 相隔 10000 天仍为 0 步（事件驱动，不逐秒遍历）。
	if _, err := g.Used(int64(10001)*86400, "a", 0); err != nil {
		t.Fatal(err)
	}
	if s := g.Steps("a"); s != 0 {
		t.Fatalf("idle 10000 days steps=%d want 0", s)
	}
	t.Log("对照：无设备在线相隔 1 天与 10000 天，入口推进步数均为 0")
}

func TestStepsBoundWhenOnline(t *testing.T) {
	// HB≤3600，一次入口推进最多跨一个日界；用心跳链构造多日跨度，
	// 每次推进（最多跨 1 日界）的步数上界为 D + 2*1 + 2。
	g := New(0, 0, 0, 86400, 86400, 3600, 8)
	g.Register("a")
	if err := g.Login(86300, "a", "d1"); err != nil {
		t.Fatal(err)
	}
	// 跨日界并在同日结束本次推进：[86300,86900) 中间有一个日界。
	if _, err := g.Used(86900, "a", 0); err != nil {
		t.Fatal(err)
	}
	s := g.Steps("a")
	dayCross := int64(1)
	bound := int64(1 + 2*dayCross + 2)
	t.Logf("单设备跨 1 日界：steps=%d（上界 D+2N+2=%d）", s, bound)
	if s > bound {
		t.Fatalf("steps %d exceeds bound %d", s, bound)
	}

	// 3 设备在线（last 错开形成多个超时点），同一推进区间内：D+2N+2。
	g2 := New(0, 0, 0, 86400, 86400, 3600, 8)
	g2.Register("a")
	if err := g2.Login(86000, "a", "d1"); err != nil {
		t.Fatal(err)
	}
	if err := g2.Login(86100, "a", "d2"); err != nil {
		t.Fatal(err)
	}
	if err := g2.Login(86200, "a", "d3"); err != nil {
		t.Fatal(err)
	}
	if _, err := g2.Used(86900, "a", 0); err != nil {
		t.Fatal(err)
	}
	s = g2.Steps("a")
	bound = 3 + 2*1 + 2
	t.Logf("3 设备跨 1 日界：steps=%d（上界 %d）", s, bound)
	if s > bound {
		t.Fatalf("steps %d exceeds bound %d", s, bound)
	}
}
