package presence

import (
	"errors"
	"testing"
)

// 积压溢出：超过 1000 条丢弃最早的，Drain 标明丢弃条数。
func TestOverflowDropped(t *testing.T) {
	s := newTestSvc(t, 3600)
	must(t, s.Report("a", "d1", StatusOnline, 0))
	must(t, s.Report("v", "d", StatusOnline, 1))
	must(t, s.Subscribe("v", "a", 1))

	cycle := []Status{StatusOnline, StatusBusy}
	const reports = 2 * (maxNotifications + 5)
	for i := int64(0); i < reports; i++ {
		st := cycle[i%2]
		must(t, s.Report("a", "d1", st, 10+i))
	}
	d, err := s.Drain("v", 3000)
	if err != nil {
		t.Fatal(err)
	}
	// 订阅起始 online，第一次 online 上报不产生通知，故通知数为 reports-1。
	wantDropped := reports - 1 - maxNotifications
	if d.Dropped != wantDropped {
		t.Fatalf("Dropped=%d want %d", d.Dropped, wantDropped)
	}
	if len(d.Notifications) != maxNotifications {
		t.Fatalf("len=%d want %d", len(d.Notifications), maxNotifications)
	}
	last := d.Notifications[len(d.Notifications)-1]
	// 2010 为偶数，最后一次状态为 cycle[1]=busy。
	if last.Status != StatusBusy {
		t.Fatalf("last=%+v", last)
	}
	d2, _ := s.Drain("v", 3001)
	if d2.Dropped != 0 || len(d2.Notifications) != 0 {
		t.Fatalf("second drain %+v", d2)
	}
}

// 拒绝次序：参数非法 > 时钟回退 > 不存在 > 状态不允许；拒绝不改变状态与时钟。
func TestRejectionOrder(t *testing.T) {
	s := newTestSvc(t, 100)
	must(t, s.Report("u", "d", StatusOnline, 10))

	if err := s.Report("", "d", StatusOnline, 11); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("empty user: %v", err)
	}
	if err := s.Report("u", "d", Status(99), 11); !errors.Is(err, ErrInvalidStatus) {
		t.Fatalf("bad status: %v", err)
	}
	if err := s.Report("u", "d", StatusOnline, 9); !errors.Is(err, ErrClockRollback) {
		t.Fatalf("rollback: %v", err)
	}
	if err := s.Subscribe("ghost", "u", 9); !errors.Is(err, ErrClockRollback) {
		t.Fatalf("rollback over missing: %v", err)
	}
	if err := s.Subscribe("ghost", "u", 11); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("missing viewer: %v", err)
	}
	must(t, s.Report("v", "d", StatusOnline, 11))
	must(t, s.Subscribe("v", "u", 11))
	if err := s.Subscribe("v", "u", 12); !errors.Is(err, ErrAlreadySubscribed) {
		t.Fatalf("dup subscribe: %v", err)
	}
	must(t, s.Report("w", "d", StatusOnline, 12))
	if err := s.Subscribe("w", "w", 12); !errors.Is(err, ErrSubscribeSelf) {
		t.Fatalf("self subscribe: %v", err)
	}
	if err := s.Offline("u", "nope", 12); !errors.Is(err, ErrDeviceNotFound) {
		t.Fatalf("offline missing device: %v", err)
	}
	for i := 0; i < maxDevices; i++ {
		dev := "dev" + string(rune('a'+i))
		must(t, s.Report("full", dev, StatusOnline, 12))
	}
	if err := s.Report("full", "dev9", StatusOnline, 13); !errors.Is(err, ErrTooManyDevices) {
		t.Fatalf("too many: %v", err)
	}
	must(t, s.Offline("full", "deva", 14))
	// 设备标识槽位不回收；但已下线的标识可重新上报续租。
	if err := s.Report("full", "deva", StatusOnline, 15); err != nil {
		t.Fatalf("re-report offlined identity should accept: %v", err)
	}
}

// Query 本人返回各设备状态；他人不返回。
func TestQuerySelfDevices(t *testing.T) {
	s := newTestSvc(t, 100)
	must(t, s.Report("u", "z", StatusAway, 0))
	must(t, s.Report("u", "a", StatusOnline, 0))
	q, _ := s.Query("u", "u", 1)
	if len(q.Devices) != 2 || q.Devices[0].Device != "a" || q.Devices[1].Device != "z" {
		t.Fatalf("devices %+v", q.Devices)
	}
	q2, _ := s.Query("other", "u", 1)
	if q2.Devices != nil {
		t.Fatalf("other must not see devices: %+v", q2.Devices)
	}
}

// 聚合优先级：在线 > 忙碌 > 离开 > 离线。
func TestAggregatePriority(t *testing.T) {
	s := newTestSvc(t, 1000)
	must(t, s.Report("u", "d1", StatusAway, 0))
	must(t, s.Report("u", "d2", StatusBusy, 0))
	q, _ := s.Query("u", "u", 1)
	if q.Status != StatusBusy {
		t.Fatalf("want busy, got %s", q.Status)
	}
	must(t, s.Report("u", "d3", StatusOnline, 1))
	q, _ = s.Query("u", "u", 2)
	if q.Status != StatusOnline {
		t.Fatalf("want online, got %s", q.Status)
	}
	must(t, s.Offline("u", "d3", 3))
	q, _ = s.Query("u", "u", 4)
	if q.Status != StatusBusy {
		t.Fatalf("back to busy, got %s", q.Status)
	}
}

// 解除拉黑但真实状态仍为离线时不应凭空产生通知。
func TestUnblockWhenStillOffline(t *testing.T) {
	s := newTestSvc(t, 10)
	must(t, s.Report("a", "d1", StatusOnline, 0))
	must(t, s.Report("v", "d", StatusOnline, 0))
	must(t, s.Subscribe("v", "a", 1))
	must(t, s.Block("a", "v", 2))
	d, _ := s.Drain("v", 5)
	if len(d.Notifications) != 1 {
		t.Fatalf("block notif=%d", len(d.Notifications))
	}
	must(t, s.Unblock("a", "v", 20)) // 租约已过期，真实状态离线 == 记忆离线
	d, _ = s.Drain("v", 20)
	if len(d.Notifications) != 0 {
		t.Fatalf("unexpected notif on unblock: %+v", d.Notifications)
	}
}
