package hold_test

import (
	"errors"
	"testing"

	"ontology/hold"
	"ontology/tier"
)

func TestPlaceValidationAndDuplicate(t *testing.T) {
	r := hold.NewRegistry()
	cases := []struct {
		name     string
		id       string
		from, to int64
		owner    string
		wantErr  error
	}{
		{"ok", "h1", 10, 20, "u", nil},
		{"empty id", "", 10, 20, "u", tier.ErrInvalidArgument},
		{"empty owner", "h2", 10, 20, "", tier.ErrInvalidArgument},
		{"from==to", "h2", 20, 20, "u", tier.ErrInvalidArgument},
		{"from>to", "h2", 30, 20, "u", tier.ErrInvalidArgument},
		{"from negative", "h2", -1, 20, "u", tier.ErrInvalidArgument},
		{"to over max", "h2", 0, tier.MaxClock + 1, "u", tier.ErrInvalidArgument},
		{"boundary ok", "h3", 0, tier.MaxClock, "u", nil},
		{"duplicate", "h1", 0, 1, "other", tier.ErrDuplicateHold},
	}
	for _, c := range cases {
		err := r.Place(c.id, c.from, c.to, c.owner)
		t.Logf("Place(%q,%d,%d,%q) -> %v", c.id, c.from, c.to, c.owner, err)
		if !errors.Is(err, c.wantErr) {
			t.Fatalf("%s: want %v, got %v", c.name, c.wantErr, err)
		}
	}
}

func TestReleaseOrdering(t *testing.T) {
	r := hold.NewRegistry()
	if err := r.Place("h", 0, 10, "alice"); err != nil {
		t.Fatal(err)
	}
	// 拒绝顺序：参数非法 -> 不存在 -> 权限。
	if err := r.Release("", "alice"); !errors.Is(err, tier.ErrInvalidArgument) {
		t.Fatalf("empty id: %v", err)
	}
	if err := r.Release("h", ""); !errors.Is(err, tier.ErrInvalidArgument) {
		t.Fatalf("empty who: %v", err)
	}
	if err := r.Release("missing", "alice"); !errors.Is(err, tier.ErrHoldNotFound) {
		t.Fatalf("missing: %v", err)
	}
	if err := r.Release("h", "bob"); !errors.Is(err, tier.ErrPermission) {
		t.Fatalf("non owner: %v", err)
	}
	t.Logf("non-owner rejected; holds still active: %d", len(r.Snapshot()))
	if err := r.Release("h", "admin"); err != nil { // 字面量 admin 可解除
		t.Fatalf("admin release: %v", err)
	}
	if err := r.Release("h", "alice"); !errors.Is(err, tier.ErrHoldNotFound) {
		t.Fatalf("after release: %v", err)
	}
	// owner 本人解除。
	if err := r.Place("h2", 0, 10, "alice"); err != nil {
		t.Fatal(err)
	}
	if err := r.Release("h2", "alice"); err != nil {
		t.Fatalf("owner release: %v", err)
	}
}

func TestHalfOpenEndpoints(t *testing.T) {
	r := hold.NewRegistry()
	// 保全 [40,50)。
	if err := r.Place("e", 40, 50, "u"); err != nil {
		t.Fatal(err)
	}
	// 半开两端：[30,40) 端点相接不算相交；[40,50) 完全相交；
	// [50,60) 端点相接不算相交；[35,41) 部分相交。
	cases := []struct {
		from, to int64
		want     bool
	}{
		{30, 40, false},
		{50, 60, false},
		{39, 40, false},
		{40, 50, true},
		{35, 41, true},
		{49, 50, true},
		{40, 41, true},
		{10, 30, false},
	}
	for _, c := range cases {
		got := r.IntersectsInterval(c.from, c.to)
		t.Logf("IntersectsInterval([%d,%d)) = %v (want %v)", c.from, c.to, got, c.want)
		if got != c.want {
			t.Fatalf("[%d,%d): got %v want %v", c.from, c.to, got, c.want)
		}
	}
}
