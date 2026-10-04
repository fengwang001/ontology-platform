package quota

import (
	"errors"
	"testing"
)

func TestRegisterAndLimits(t *testing.T) {
	r := NewRegistry()
	if err := r.Register("t", Limits{Qu: 3, Qd: 1, Qw: 3}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := r.Register("t", Limits{}); !errors.Is(err, ErrDuplicateTenant) {
		t.Fatalf("dup = %v, want ErrDuplicateTenant", err)
	}
	if err := r.Register("", Limits{}); !errors.Is(err, ErrInvalidTenant) {
		t.Fatalf("empty tenant = %v", err)
	}
	if err := r.Register("x", Limits{Qu: 1_000_001}); !errors.Is(err, ErrInvalidQuota) {
		t.Fatalf("bad quota = %v", err)
	}
	if r.Has("nope") {
		t.Fatal("Has unknown tenant = true")
	}
}

func TestCheckApplyRoll(t *testing.T) {
	r := NewRegistry()
	if err := r.Register("t", Limits{Qu: 3, Qd: 1, Qw: 3}); err != nil {
		t.Fatal(err)
	}
	now := int64(86400) // day 1
	if err := r.Check("t", now, [3]int{3, 1, 3}); err != nil {
		t.Fatalf("exact-fit check: %v", err)
	}
	r.Apply("t", now, [3]int{3, 1, 3})
	if err := r.Check("t", now, [3]int{1, 0, 0}); !errors.Is(err, ErrExceeded) {
		t.Fatalf("over check = %v", err)
	}
	u, d, w, err := r.Used("t", now+100)
	if err != nil || u != 3 || d != 1 || w != 3 {
		t.Fatalf("used same day = (%d,%d,%d),%v", u, d, w, err)
	}
	// 跨日：now 的纯函数，无需任何操作触发。
	u, d, w, err = r.Used("t", now+86400)
	if err != nil || u != 0 || d != 0 || w != 0 {
		t.Fatalf("used next day = (%d,%d,%d),%v", u, d, w, err)
	}
	if _, _, _, err := r.Used("nope", now); !errors.Is(err, ErrUnknownTenant) {
		t.Fatalf("unknown used = %v", err)
	}
}
