package hold

import "testing"

// 日志统一打印输入、输出与判定依据。
func logAct(t *testing.T, op string, in any, got error) {
	t.Helper()
	t.Logf("op=%s in=%v -> err=%v", op, in, got)
}

// 覆盖：参数校验、重复登记、半开区间两端相切不相交。
func TestHoldRegisterAndHalfOpen(t *testing.T) {
	r := NewRegistry()
	if err := r.Hold("h", 40000, 45000, "u"); err != nil {
		t.Fatalf("first hold: %v", err)
	}
	logAct(t, "Hold", "h,[40000,45000),u", nil)

	dup := r.Hold("h", 1, 2, "x")
	logAct(t, "Hold-dup", "h", dup)
	if dup != ErrDuplicate {
		t.Fatalf("dup = %v, want ErrDuplicate", dup)
	}

	bad := []struct {
		id, owner string
		from, to  int64
	}{
		{"", "u", 1, 2}, {"i", "", 1, 2}, {"i", "u", -1, 2},
		{"i", "u", 0, 10_000_000_000_001}, {"i", "u", 5, 5}, {"i", "u", 9, 1},
	}
	for _, b := range bad {
		if err := r.Hold(b.id, b.from, b.to, b.owner); err != ErrInvalid {
			t.Fatalf("bad hold %+v -> %v", b, err)
		}
	}

	// 半开：右端点 45000 相切 [45000,50000) 不相交；左端点 40000 同理。
	cases := []struct {
		from, to int64
		want     bool
		why      string
	}{
		{45000, 50000, false, "touch right endpoint only"},
		{0, 40000, false, "touch left endpoint only"},
		{40000, 45000, true, "same interval"},
		{44999, 45000, true, "overlap one ms"},
	}
	for _, c := range cases {
		got := r.Intersects(c.from, c.to)
		t.Logf("Intersects [%d,%d) = %v (%s)", c.from, c.to, got, c.why)
		if got != c.want {
			t.Fatalf("Intersects(%d,%d)=%v want %v", c.from, c.to, got, c.want)
		}
	}
}

// 覆盖：非 owner 拒绝、owner 与 admin 可解除、不存在与空参数顺序。
func TestReleasePermissions(t *testing.T) {
	r := NewRegistry()
	if err := r.Hold("k", 0, 10, "alice"); err != nil {
		t.Fatal(err)
	}
	if err := r.Release("k", "bob"); err != ErrPermission {
		t.Fatalf("non-owner release = %v", err)
	}
	logAct(t, "Release", "k by bob -> permission denied", ErrPermission)

	if err := r.Release("k", "alice"); err != nil {
		t.Fatalf("owner release = %v", err)
	}
	if err := r.Hold("k2", 0, 10, "alice"); err != nil {
		t.Fatal(err)
	}
	if err := r.Release("k2", "admin"); err != nil {
		t.Fatalf("admin release = %v", err)
	}
	if err := r.Release("missing", "admin"); err != ErrNotFound {
		t.Fatalf("missing = %v want ErrNotFound", err)
	}
	if err := r.Release("", "admin"); err != ErrInvalid {
		t.Fatalf("empty id = %v want ErrInvalid", err)
	}
	if err := r.Release("k3", ""); err != ErrInvalid {
		t.Fatalf("empty who = %v want ErrInvalid", err)
	}
	// 被拒绝操作不改状态：登记表已空。
	if len(r.Snapshot()) != 0 {
		t.Fatalf("snapshot = %v, want empty", r.Snapshot())
	}
}
