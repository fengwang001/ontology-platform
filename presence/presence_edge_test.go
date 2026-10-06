package presence

import (
	"errors"
	"testing"
)

func newTestSvc(t *testing.T, lease int64) *Service {
	t.Helper()
	s, err := New(lease)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func TestNewInvalidLease(t *testing.T) {
	for _, lease := range []int64{0, -1, 3601} {
		if _, err := New(lease); !errors.Is(err, ErrInvalidLease) {
			t.Fatalf("lease=%d want ErrInvalidLease, got %v", lease, err)
		}
	}
}

// 到期恰等于时刻视为已到期；差一秒仍在线。
func TestExpiryBoundary(t *testing.T) {
	s := newTestSvc(t, 10)
	must(t, s.Report("u", "d1", StatusOnline, 5))

	if q, _ := s.Query("u", "u", 14); q.Status != StatusOnline || q.ActiveCount != 1 {
		t.Fatalf("at 14 (one second before) got %+v", q)
	}
	q, err := s.Query("u", "u", 15)
	if err != nil || q.Status != StatusOffline || q.ActiveCount != 0 {
		t.Fatalf("at 15 (exact expiry) got %+v err=%v", q, err)
	}
	if err := s.Report("u", "d1", StatusAway, 15); err != nil {
		t.Fatalf("re-report after expiry: %v", err)
	}
}

// 一次 Drain 交出多条来自不同到期时刻、乱序处理但按生效时刻升序的通知，
// 且相邻通知前后状态相接。
func TestDrainMultipleOutOfOrderExpiries(t *testing.T) {
	s := newTestSvc(t, 100)
	must(t, s.Report("a", "d1", StatusOnline, 0)) // expires 100
	must(t, s.Report("b", "d1", StatusOnline, 0)) // expires 100
	must(t, s.Report("v", "d", StatusOnline, 1))
	must(t, s.Subscribe("v", "a", 2))
	must(t, s.Subscribe("v", "b", 3))

	must(t, s.Report("a", "d1", StatusBusy, 50))    // 到期 150
	must(t, s.Report("a", "d1", StatusOnline, 100)) // 到期 200
	must(t, s.Report("b", "d1", StatusOnline, 120)) // 新到期 220，旧条目变陈旧

	d, err := s.Drain("v", 250)
	if err != nil {
		t.Fatal(err)
	}
	want := []Notification{
		{Target: "a", Status: StatusBusy, EffectiveAt: 50},
		{Target: "b", Status: StatusOffline, EffectiveAt: 100},
		{Target: "a", Status: StatusOnline, EffectiveAt: 100},
		{Target: "b", Status: StatusOnline, EffectiveAt: 120},
		{Target: "a", Status: StatusOffline, EffectiveAt: 200},
		{Target: "b", Status: StatusOffline, EffectiveAt: 220},
	}
	assertNotifications(t, d.Notifications, want)
	// 同一对象相邻通知前后状态相接（跨对象按生效时刻穿插）。
	assertChains(t, d.Notifications)
}

// 隐身期间真实变化不产生通知，解除隐身不补发积压。
func TestInvisibleSuppresses(t *testing.T) {
	s := newTestSvc(t, 100)
	must(t, s.Report("a", "d1", StatusOnline, 0))
	must(t, s.Report("v", "d", StatusOnline, 0))
	must(t, s.Subscribe("v", "a", 1))

	must(t, s.SetInvisible("a", true, 10))
	must(t, s.Report("a", "d1", StatusBusy, 20))
	must(t, s.Report("a", "d1", StatusAway, 30))
	d, _ := s.Drain("v", 35)
	assertNotifications(t, d.Notifications, []Notification{
		{Target: "a", Status: StatusOffline, EffectiveAt: 10},
	})

	must(t, s.SetInvisible("a", false, 40))
	d, _ = s.Drain("v", 45)
	assertNotifications(t, d.Notifications, []Notification{
		{Target: "a", Status: StatusAway, EffectiveAt: 40},
	})
}

// 拉黑期间真实变化不产生通知；被拉黑者不收任何通知；解除后不补发。
func TestBlockSuppresses(t *testing.T) {
	s := newTestSvc(t, 1000)
	must(t, s.Report("a", "d1", StatusOnline, 0))
	must(t, s.Report("v", "d", StatusOnline, 0))
	must(t, s.Subscribe("v", "a", 1))

	must(t, s.Block("a", "v", 10))
	must(t, s.Report("a", "d1", StatusBusy, 20))
	must(t, s.Report("a", "d1", StatusAway, 30))
	d, _ := s.Drain("v", 35)
	assertNotifications(t, d.Notifications, []Notification{
		{Target: "a", Status: StatusOffline, EffectiveAt: 10},
	})

	must(t, s.Unblock("a", "v", 40))
	d, _ = s.Drain("v", 45)
	assertNotifications(t, d.Notifications, []Notification{
		{Target: "a", Status: StatusAway, EffectiveAt: 40},
	})

	if err := s.Block("a", "a", 50); !errors.Is(err, ErrBlockSelf) {
		t.Fatalf("block self: %v", err)
	}
}

// 同一时刻到期先于切换。
func TestSameTimeExpiryBeforeSwitch(t *testing.T) {
	s := newTestSvc(t, 50)
	must(t, s.Report("a", "d1", StatusOnline, 0)) // expires 50
	must(t, s.Report("v", "d", StatusOnline, 0))
	must(t, s.Subscribe("v", "a", 1))

	must(t, s.SetInvisible("a", true, 50)) // 到期 offline@50 先生效，隐身不再产生可见变化
	d, _ := s.Drain("v", 50)
	assertNotifications(t, d.Notifications, []Notification{
		{Target: "a", Status: StatusOffline, EffectiveAt: 50},
	})

	must(t, s.SetInvisible("a", false, 60)) // 仍离线，无通知
	d2, _ := s.Drain("v", 60)
	assertNotifications(t, d2.Notifications, nil)
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func assertNotifications(t *testing.T, got, want []Notification) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("notifications len=%d want %d\ngot=%+v", len(got), len(want), got)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("notif[%d]=%+v want %+v\nfull=%+v", i, got[i], want[i], got)
		}
	}
}

// assertChains 校验同一对象的相邻通知前后状态相接，且整体按生效时刻非降序。
func assertChains(t *testing.T, got []Notification) {
	t.Helper()
	last := map[string]Status{}
	var prevAt int64 = -1
	for i, n := range got {
		if int64(i) > 0 && n.EffectiveAt < prevAt {
			t.Fatalf("notifications not sorted by effective time at %d: %+v", i, got)
		}
		prevAt = n.EffectiveAt
		if old, ok := last[n.Target]; ok && old == n.Status {
			t.Fatalf("target %s consecutive equal status %s", n.Target, n.Status)
		}
		last[n.Target] = n.Status
	}
}
