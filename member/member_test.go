package member

import (
	"errors"
	"reflect"
	"testing"
)

func TestClassify(t *testing.T) {
	if !IsMulticastGroup(0xE0000000) || !IsMulticastGroup(0xEFFFFFFF) {
		t.Fatal("multicast bounds")
	}
	if IsMulticastGroup(0xDFFFFFFF) || IsMulticastGroup(0xF0000000) {
		t.Fatal("multicast outside")
	}
	if !IsLocalGroup(0xE00000FF) || IsLocalGroup(0xE0000100) {
		t.Fatal("local boundary")
	}
}

func TestReportRefreshExpiry(t *testing.T) {
	tb := New(3, 100, 10, 2)
	if _, err := tb.Report(1, 0xE1000001, 0); err != nil {
		t.Fatal(err)
	}
	if r, ok := tb.Lookup(1, 0xE1000001); !ok || r.Exp != 100 {
		t.Fatalf("lookup %+v %v", r, ok)
	}
	if _, err := tb.Report(1, 0xE1000001, 50); err != nil {
		t.Fatal(err)
	}
	if r, _ := tb.Lookup(1, 0xE1000001); r.Exp != 150 {
		t.Fatalf("refresh exp = %d", r.Exp)
	}
	tb.Advance(149)
	if _, ok := tb.Lookup(1, 0xE1000001); !ok {
		t.Fatal("alive@149")
	}
	tb.Advance(150)
	if _, ok := tb.Lookup(1, 0xE1000001); ok {
		t.Fatal("must expire exactly at 150")
	}
}

func TestLimitsAndOrder(t *testing.T) {
	tb := New(2, 100, 1, 1)
	if _, err := tb.Report(1, 0xE1000001, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := tb.Report(1, 0xE2000002, 0); !errors.Is(err, ErrPortLimit) {
		t.Fatalf("port limit: %v", err)
	}
	if _, err := tb.Report(2, 0xE2000002, 0); !errors.Is(err, ErrGroupLimit) {
		t.Fatalf("group limit: %v", err)
	}
	// 到期后两个名额同时释放。
	tb.Advance(100)
	if _, err := tb.Report(2, 0xE2000002, 100); err != nil {
		t.Fatalf("expired slot: %v", err)
	}
}

func TestPendingLifecycle(t *testing.T) {
	tb := New(2, 100, 10, 10)
	_, _ = tb.Report(1, 0xE1000001, 0)
	r, pending, err := tb.PrepareLeave(1, 0xE1000001, 30)
	if err != nil || pending || r.Exp != 30 || !r.Pending {
		t.Fatalf("first prepare: %+v pending=%v err=%v", r, pending, err)
	}
	// 只降不升：更大的新 exp 无效。
	_, pending, _ = tb.PrepareLeave(1, 0xE1000001, 80)
	if !pending {
		t.Fatal("repeat prepare must report alreadyPending")
	}
	if r, _ := tb.Lookup(1, 0xE1000001); r.Exp != 30 {
		t.Fatalf("exp must stay 30, got %d", r.Exp)
	}
	// Report 解除 pending 并刷新。
	wasPending, err := tb.Report(1, 0xE1000001, 50)
	if err != nil || !wasPending {
		t.Fatalf("report after pending: %v %v", wasPending, err)
	}
	if r, _ := tb.Lookup(1, 0xE1000001); r.Pending || r.Exp != 150 {
		t.Fatalf("report reset: %+v", r)
	}
}

func TestFastLeaveAndNotMember(t *testing.T) {
	tb := New(2, 100, 10, 10)
	if err := tb.FastLeave(1, 0xE1000001); !errors.Is(err, ErrNotMember) {
		t.Fatalf("not member: %v", err)
	}
	_, _ = tb.Report(1, 0xE1000001, 0)
	if err := tb.FastLeave(1, 0xE1000001); err != nil {
		t.Fatal(err)
	}
	if _, ok := tb.Lookup(1, 0xE1000001); ok {
		t.Fatal("fast leave removed")
	}
}

func TestMembersAndTouched(t *testing.T) {
	tb := New(5, 100, 10, 10)
	_, _ = tb.Report(3, 0xE1000001, 0)
	_, _ = tb.Report(1, 0xE1000001, 0)
	_, _ = tb.Report(2, 0xE2000002, 0)
	if got := tb.Members(0xE1000001); !reflect.DeepEqual(got, []int{1, 3}) {
		t.Fatalf("members sorted = %v", got)
	}
	if tb.Touched() != 2 {
		t.Fatalf("touched = %d", tb.Touched())
	}
	if got := tb.Members(0xE9999999); len(got) != 0 {
		t.Fatalf("unknown group = %v", got)
	}
}
