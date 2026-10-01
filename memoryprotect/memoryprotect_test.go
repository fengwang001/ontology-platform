package memoryprotect

import (
	"errors"
	"reflect"
	"testing"
)

func mustCreate(t *testing.T, m *Manager, path string, min, low int64) {
	t.Helper()
	if err := m.Create(path, min, low); err != nil {
		t.Fatalf("Create(%q, %d, %d): %v", path, min, low, err)
	}
}

func mustSetUsage(t *testing.T, m *Manager, path string, usage int64) {
	t.Helper()
	if err := m.SetUsage(path, usage); err != nil {
		t.Fatalf("SetUsage(%q, %d): %v", path, usage, err)
	}
}

func assertErrorIs(t *testing.T, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("error = %v, want %v", got, want)
	}
}

func assertEffective(t *testing.T, m *Manager, path string, wantLow, wantMin int64) {
	t.Helper()
	gotLow, gotMin, err := m.Effective(path)
	if err != nil {
		t.Fatalf("Effective(%q): %v", path, err)
	}
	if gotLow != wantLow || gotMin != wantMin {
		t.Fatalf("Effective(%q) = (%d, %d), want (%d, %d)", path, gotLow, gotMin, wantLow, wantMin)
	}
}

func TestSpecReclaimExample(t *testing.T) {
	m := NewManager()
	mustCreate(t, m, "/a", 20, 60)
	mustCreate(t, m, "/b", 0, 0)
	mustSetUsage(t, m, "/a", 100)
	mustSetUsage(t, m, "/b", 100)

	got, err := m.Reclaim(150)
	if err != nil {
		t.Fatal(err)
	}
	want := ReclaimResult{
		Records: []ReclaimRecord{
			{Path: "/b", Bytes: 100, Pass: 1},
			{Path: "/a", Bytes: 40, Pass: 1},
			{Path: "/a", Bytes: 10, Pass: 2},
		},
		Reclaimed: 150,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Reclaim = %+v, want %+v", got, want)
	}

	m = NewManager()
	mustCreate(t, m, "/a", 20, 60)
	mustCreate(t, m, "/b", 0, 0)
	mustSetUsage(t, m, "/a", 100)
	mustSetUsage(t, m, "/b", 100)
	got, err = m.Reclaim(250)
	if err != nil {
		t.Fatal(err)
	}
	if got.Reclaimed != 180 || !got.Insufficient || len(got.Records) != 3 {
		t.Fatalf("Reclaim(250) = %+v, want 180 reclaimed and insufficient", got)
	}
	if got.Records[2].Bytes != 40 {
		t.Fatalf("second pass reclaim = %d, want 40", got.Records[2].Bytes)
	}
}

func TestEffectiveLowRules(t *testing.T) {
	m := NewManager()
	mustCreate(t, m, "/p", 0, 10)
	mustCreate(t, m, "/p/x", 0, 100)
	mustCreate(t, m, "/p/y", 0, 8)
	mustSetUsage(t, m, "/p/x", 6)
	mustSetUsage(t, m, "/p/y", 8)

	assertEffective(t, m, "/p", 10, 0)
	assertEffective(t, m, "/p/x", 4, 0)
	assertEffective(t, m, "/p/y", 5, 0)

	m = NewManager()
	mustCreate(t, m, "/root-child", 0, 100)
	mustSetUsage(t, m, "/root-child", 30)
	assertEffective(t, m, "/root-child", 30, 0)
}

func TestHugeProductUsesExactIntegerDivision(t *testing.T) {
	m := NewManager()
	mustCreate(t, m, "/p", 1_000_000_000_000_000, 1_000_000_000_000_000)
	mustCreate(t, m, "/p/x", 1_000_000_000_000_000, 1_000_000_000_000_000)
	mustCreate(t, m, "/p/y", 1_000_000_000_000_000, 1_000_000_000_000_000)
	mustSetUsage(t, m, "/p/x", 1_000_000_000_000_000)
	mustSetUsage(t, m, "/p/y", 1_000_000_000_000_000)

	assertEffective(t, m, "/p/x", 500_000_000_000_000, 500_000_000_000_000)
	assertEffective(t, m, "/p/y", 500_000_000_000_000, 500_000_000_000_000)
}

func TestEffectiveMinIsClampedToEffectiveLow(t *testing.T) {
	m := NewManager()
	mustCreate(t, m, "/p", 100, 100)
	mustCreate(t, m, "/p/x", 100, 100)
	mustCreate(t, m, "/p/y", 0, 100)
	mustSetUsage(t, m, "/p/x", 100)
	mustSetUsage(t, m, "/p/y", 100)

	assertEffective(t, m, "/p", 100, 100)
	assertEffective(t, m, "/p/x", 50, 50)
	assertEffective(t, m, "/p/y", 50, 0)
}

func TestReclaimPassesAndOrdering(t *testing.T) {
	m := NewManager()
	mustCreate(t, m, "/a", 20, 60)
	mustCreate(t, m, "/b", 0, 0)
	mustSetUsage(t, m, "/a", 100)
	mustSetUsage(t, m, "/b", 100)

	got, err := m.Reclaim(100)
	if err != nil {
		t.Fatal(err)
	}
	want := ReclaimResult{
		Records:   []ReclaimRecord{{Path: "/b", Bytes: 100, Pass: 1}},
		Reclaimed: 100,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("first pass satisfied early: got %+v, want %+v", got, want)
	}

	m = NewManager()
	mustCreate(t, m, "/a", 0, 0)
	mustCreate(t, m, "/b", 0, 0)
	mustSetUsage(t, m, "/a", 100)
	mustSetUsage(t, m, "/b", 100)
	got, err = m.Reclaim(100)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Records) != 1 || got.Records[0].Path != "/a" || got.Records[0].Bytes != 100 {
		t.Fatalf("tie breaker records = %+v, want /a first", got.Records)
	}
}

func TestSecondPassUsesPostFirstUsageButFrozenProtection(t *testing.T) {
	m := NewManager()
	mustCreate(t, m, "/p", 100, 100)
	mustCreate(t, m, "/p/x", 40, 90)
	mustCreate(t, m, "/p/y", 0, 0)
	mustSetUsage(t, m, "/p/x", 100)
	mustSetUsage(t, m, "/p/y", 100)

	got, err := m.Reclaim(120)
	if err != nil {
		t.Fatal(err)
	}
	want := ReclaimResult{
		Records: []ReclaimRecord{
			{Path: "/p/y", Bytes: 100, Pass: 1},
			{Path: "/p/x", Bytes: 10, Pass: 1},
			{Path: "/p/x", Bytes: 10, Pass: 2},
		},
		Reclaimed:    120,
		Insufficient: false,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Reclaim = %+v, want %+v", got, want)
	}
}

func TestRejectionPrecedenceAndAtomicity(t *testing.T) {
	m := NewManager()
	mustCreate(t, m, "/p", 0, 0)
	mustCreate(t, m, "/p/child", 0, 0)

	assertErrorIs(t, m.Create("a", 0, 0), ErrInvalidArgument)
	assertErrorIs(t, m.Create("/", 0, 0), ErrRootOperation)
	assertErrorIs(t, m.Create("/p", 0, 0), ErrExists)
	assertErrorIs(t, m.Create("/missing/x", 0, 0), ErrParentNotFound)
	mustCreate(t, m, "/leaf", 0, 0)
	mustSetUsage(t, m, "/leaf", 0)
	mustSetUsage(t, m, "/leaf", 5)
	assertErrorIs(t, m.Create("/leaf/x", 0, 0), ErrParentHasUsage)

	assertErrorIs(t, m.SetProtect("/missing", 1, 0), ErrInvalidArgument)
	assertErrorIs(t, m.SetProtect("/", 0, 0), ErrRootOperation)
	assertErrorIs(t, m.SetProtect("/missing", 0, 0), ErrNotFound)
	assertErrorIs(t, m.SetUsage("/missing", -1), ErrInvalidArgument)
	assertErrorIs(t, m.SetUsage("/", 0), ErrRootOperation)
	assertErrorIs(t, m.SetUsage("/missing", 0), ErrNotFound)
	assertErrorIs(t, m.SetUsage("/p", 1), ErrNotLeaf)
	assertErrorIs(t, m.Remove("/"), ErrRootOperation)
	assertErrorIs(t, m.Remove("/missing"), ErrNotFound)
	assertErrorIs(t, m.Remove("/p"), ErrHasChildren)
	if _, _, err := m.Effective("/"); !errors.Is(err, ErrRootOperation) {
		t.Fatalf("Effective(/) error = %v, want %v", err, ErrRootOperation)
	}
	if _, _, err := m.Effective("/missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Effective(/missing) error = %v, want %v", err, ErrNotFound)
	}

	beforeLow, beforeMin, err := m.Effective("/p/child")
	if err != nil || beforeLow != 0 || beforeMin != 0 {
		t.Fatalf("state before rejected operations = (%d, %d), %v", beforeLow, beforeMin, err)
	}
	assertErrorIs(t, m.SetProtect("/p/child", 5, 1), ErrInvalidArgument)
	assertErrorIs(t, m.SetUsage("/p/child", -1), ErrInvalidArgument)
	assertEffective(t, m, "/p/child", 0, 0)
}
