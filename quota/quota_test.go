package quota_test

import (
	"errors"
	"testing"

	"ontology/quota"
)

func TestRegister(t *testing.T) {
	m := quota.NewMeter()
	cases := []struct {
		name string
		id   string
		q    quota.Quotas
		want error
	}{
		{"空租户名", "", quota.Quotas{}, quota.ErrInvalidArgument},
		{"URL 配额超上限", "t", quota.Quotas{URL: quota.MaxQuota + 1}, quota.ErrInvalidArgument},
		{"目录配额超上限", "t", quota.Quotas{Dir: quota.MaxQuota + 1}, quota.ErrInvalidArgument},
		{"预热配额超上限", "t", quota.Quotas{Prewarm: quota.MaxQuota + 1}, quota.ErrInvalidArgument},
		{"恰等上限", "t", quota.Quotas{URL: quota.MaxQuota, Dir: quota.MaxQuota, Prewarm: quota.MaxQuota}, nil},
		{"零配额合法", "z", quota.Quotas{}, nil},
	}
	for _, c := range cases {
		if got := m.Register(c.id, c.q); !errors.Is(got, c.want) {
			t.Errorf("%s: Register(%q, %+v) = %v, want %v", c.name, c.id, c.q, got, c.want)
		}
	}
}

func TestChargePurgeOrderAndExact(t *testing.T) {
	m := quota.NewMeter()
	if err := m.Register("t", quota.Quotas{URL: 3, Dir: 1}); err != nil {
		t.Fatal(err)
	}
	// 恰等配额允许。
	if err := m.ChargePurge(100, "t", 3, 1); err != nil {
		t.Fatalf("恰等配额应允许: %v", err)
	}
	if got := m.Used(100, "t", quota.KindURL); got != 3 {
		t.Fatalf("URL 已用量 = %d, want 3", got)
	}
	// URL 与目录同时不足时先报 URL。
	if err := m.ChargePurge(100, "t", 1, 1); !errors.Is(err, quota.ErrURLQuota) {
		t.Fatalf("应先报 URL 配额不足, got %v", err)
	}
	// URL 够、目录不够时报目录。
	if err := m.ChargePurge(100, "t", 0, 1); !errors.Is(err, quota.ErrDirQuota) {
		t.Fatalf("应报目录配额不足, got %v", err)
	}
	// 拒绝不留痕：已用量与时钟不变。
	if got := m.Used(100, "t", quota.KindDir); got != 1 {
		t.Fatalf("拒绝后目录已用量 = %d, want 1", got)
	}
	if got := m.MaxNow(); got != 100 {
		t.Fatalf("拒绝后时钟 = %d, want 100", got)
	}
}

func TestClockAndTenantOrder(t *testing.T) {
	m := quota.NewMeter()
	if err := m.Register("t", quota.Quotas{URL: 1, Dir: 1, Prewarm: 1}); err != nil {
		t.Fatal(err)
	}
	if err := m.Touch(500, "t"); err != nil {
		t.Fatal(err)
	}
	// 时钟回退优先于租户不存在。
	if err := m.ChargePurge(499, "ghost", 0, 0); !errors.Is(err, quota.ErrClockBackward) {
		t.Fatalf("应先报时钟回退, got %v", err)
	}
	if err := m.ChargePurge(500, "ghost", 0, 0); !errors.Is(err, quota.ErrTenantNotFound) {
		t.Fatalf("应报租户不存在, got %v", err)
	}
	if err := m.Touch(499, "t"); !errors.Is(err, quota.ErrClockBackward) {
		t.Fatalf("Touch 应报时钟回退, got %v", err)
	}
	if err := m.AdvanceClock(499); !errors.Is(err, quota.ErrClockBackward) {
		t.Fatalf("AdvanceClock 应报时钟回退, got %v", err)
	}
	// now 相等不算回退。
	if err := m.Touch(500, "t"); err != nil {
		t.Fatalf("now 相等应允许: %v", err)
	}
}

func TestDayRolloverIsPureFunction(t *testing.T) {
	m := quota.NewMeter()
	if err := m.Register("t", quota.Quotas{URL: 2, Dir: 2, Prewarm: 2}); err != nil {
		t.Fatal(err)
	}
	day0 := int64(1000)
	if err := m.ChargePurge(day0, "t", 2, 2); err != nil {
		t.Fatal(err)
	}
	if err := m.CheckPrewarm(day0, "t", 2); err != nil {
		t.Fatal(err)
	}
	m.CommitPrewarm(day0, "t", 2)
	// 无任何操作触发，下一日的已用量即为 0（now 的纯函数）。
	day1 := day0 + quota.DaySeconds
	for _, k := range []quota.Kind{quota.KindURL, quota.KindDir, quota.KindPrewarm} {
		if got := m.Used(day1, "t", k); got != 0 {
			t.Fatalf("跨日后 kind=%d 已用量 = %d, want 0", k, got)
		}
	}
	// 同一日号边界：86399 与 0 同日，86400 进入下一日。
	if got := m.Used(86399+1000, "t", quota.KindURL); got != 0 {
		t.Fatalf("日号 1 内已用量应为 0, got %d", got)
	}
	// 新一日可重新计费。
	if err := m.ChargePurge(day1, "t", 2, 2); err != nil {
		t.Fatalf("跨日后应可重新计费: %v", err)
	}
	if got := m.Used(day1, "t", quota.KindURL); got != 2 {
		t.Fatalf("跨日后已用量 = %d, want 2", got)
	}
}

func TestPrewarmTwoPhase(t *testing.T) {
	m := quota.NewMeter()
	if err := m.Register("t", quota.Quotas{Prewarm: 3}); err != nil {
		t.Fatal(err)
	}
	if err := m.CheckPrewarm(10, "t", 3); err != nil {
		t.Fatal(err)
	}
	// 只检查不提交，已用量不变、时钟不变。
	if got := m.Used(10, "t", quota.KindPrewarm); got != 0 {
		t.Fatalf("Check 后已用量 = %d, want 0", got)
	}
	if got := m.MaxNow(); got != 0 {
		t.Fatalf("Check 后时钟 = %d, want 0", got)
	}
	m.CommitPrewarm(10, "t", 3)
	if got := m.Used(10, "t", quota.KindPrewarm); got != 3 {
		t.Fatalf("Commit 后已用量 = %d, want 3", got)
	}
	if err := m.CheckPrewarm(10, "t", 1); !errors.Is(err, quota.ErrPrewarmQuota) {
		t.Fatalf("应报预热配额不足, got %v", err)
	}
}
