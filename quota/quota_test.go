package quota

import (
	"errors"
	"testing"
)

func TestSetQuotaValidation(t *testing.T) {
	cases := []struct {
		name   string
		tenant string
		q      int64
		want   error
	}{
		{"空租户", "", 10, ErrInvalidParam},
		{"负额度", "A", -1, ErrInvalidParam},
		{"超界额度", "A", MaxQuota + 1, ErrInvalidParam},
		{"零额度", "A", 0, nil},
		{"恰为上界", "A", MaxQuota, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			led := NewLedger()
			if err := led.CheckSetQuota(tc.tenant, tc.q); !errors.Is(err, tc.want) {
				t.Fatalf("CheckSetQuota(%q,%d) = %v, 期望 %v", tc.tenant, tc.q, err, tc.want)
			}
		})
	}
}

func TestSetQuotaBelowUsed(t *testing.T) {
	led := NewLedger()
	if err := led.CheckSetQuota("A", 100); err != nil {
		t.Fatal(err)
	}
	led.ApplySetQuota("A", 100)
	led.Reserve("A", 60)
	led.Commit("A", "k", 10) // U=10, R=50

	if err := led.CheckSetQuota("A", 60); !errors.Is(err, nil) {
		t.Fatalf("恰等 U+R=60 应通过, 得到 %v", err)
	}
	if err := led.CheckSetQuota("A", 59); !errors.Is(err, ErrBelowUsed) {
		t.Fatalf("低于占用应报 ErrBelowUsed, 得到 %v", err)
	}
}

func TestReserveReleaseCommit(t *testing.T) {
	led := NewLedger()
	led.ApplySetQuota("A", 1000)
	led.Reserve("A", 60)
	led.Reserve("A", 40)
	if v := led.View("A"); v.Reserved != 100 || v.Open != 2 {
		t.Fatalf("Reserve 后 R=%d Open=%d, 期望 100/2", v.Reserved, v.Open)
	}
	led.Release("A", 40)
	if v := led.View("A"); v.Reserved != 60 || v.Open != 1 {
		t.Fatalf("Release 后 R=%d Open=%d, 期望 60/1", v.Reserved, v.Open)
	}
	led.Commit("A", "k", 60)
	if v := led.View("A"); v.Used != 60 || v.Reserved != 0 || v.Open != 0 {
		t.Fatalf("Commit 后 U=%d R=%d Open=%d, 期望 60/0/0", v.Used, v.Reserved, v.Open)
	}
	// 同键覆盖：U 只计净增。
	led.Reserve("A", 80)
	led.Commit("A", "k", 80)
	if v := led.View("A"); v.Used != 80 {
		t.Fatalf("覆盖同键后 U=%d, 期望 80", v.Used)
	}
	if got := led.View("A").Objects["k"]; got != 80 {
		t.Fatalf("对象表 k=%d, 期望 80", got)
	}
}

func TestViewUnknownTenant(t *testing.T) {
	led := NewLedger()
	v := led.View("ghost")
	if v.Quota != 0 || v.Used != 0 || v.Reserved != 0 || v.Open != 0 {
		t.Fatalf("未知租户应返回零值, 得到 %+v", v)
	}
}
