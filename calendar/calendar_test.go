package calendar

import (
	"errors"
	"testing"
)

func TestLocal(t *testing.T) {
	cases := []struct {
		name   string
		tz     int64
		t      int64
		day    int64
		x      int64
		reason string
	}{
		{"zero tz", 0, 100, 0, 100, "day=floor(t/86400), x=余数"},
		{"negative t", 0, -1, -1, 86399, "数学向下取整而非向零截断"},
		{"negative sum spec", -28800, 100, -1, 57700, "题例：-28700 -> day=-1,x=57700"},
		{"negative sum -86400", -28800, -57600, -1, 0, "t+tz=-86400 恰为日界"},
		{"positive tz day shift", 3600, 86300, 1, 3500, "+3600 时区使日界提前一小时"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := New(tc.tz, 0, 0)
			day, x := c.Local(tc.t)
			t.Logf("输入 tz=%d t=%d（%s）-> day=%d x=%d", tc.tz, tc.t, tc.reason, day, x)
			if day != tc.day || x != tc.x {
				t.Fatalf("got (%d,%d), want (%d,%d)", day, x, tc.day, tc.x)
			}
		})
	}
}

func TestCurfew(t *testing.T) {
	cases := []struct {
		name   string
		cs, ce int64
		x      int64
		want   bool
		reason string
	}{
		{"none equal", 100, 100, 50, false, "cs==ce 无宵禁"},
		{"within day: before", 36000, 60000, 100, false, "日内区间，未到 cs"},
		{"within day: cs inclusive", 36000, 60000, 36000, true, "x==cs 取等算宵禁"},
		{"within day: middle", 36000, 60000, 40000, true, "cs<=x<ce"},
		{"within day: ce exclusive", 36000, 60000, 60000, false, "x==ce 已解禁"},
		{"wrap: day time", 79200, 28800, 36000, false, "跨午夜写法，白天不在宵禁"},
		{"wrap: cs inclusive", 79200, 28800, 79200, true, "x>=cs"},
		{"wrap: night middle", 79200, 28800, 80000, true, "x>=cs"},
		{"wrap: before ce", 79200, 28800, 28799, true, "x<ce"},
		{"wrap: ce exclusive", 79200, 28800, 28800, false, "x==ce 解禁"},
		{"wrap: zero in", 79200, 28800, 0, true, "x=0<ce"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := New(0, tc.cs, tc.ce)
			got := c.Curfew(tc.x)
			t.Logf("cs=%d ce=%d x=%d（%s）-> curfew=%v", tc.cs, tc.ce, tc.x, tc.reason, got)
			if got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestSetHoliday(t *testing.T) {
	c := New(0, 0, 0)
	if err := c.SetHoliday(10, 5, true); err != nil {
		t.Fatalf("future day should succeed: %v", err)
	}
	if !c.IsHoliday(5) {
		t.Fatal("day 5 should be holiday")
	}
	t.Log("输入 now=10 day=5 -> 节假日设置成功（日起始 432000>now）")

	err := c.SetHoliday(86400, 1, true)
	if !errors.Is(err, ErrTooLate) {
		t.Fatalf("day already started: got %v want ErrTooLate", err)
	}
	t.Log("输入 now=86400 day=1 -> 为时已晚（日起始 0<=now，判定依据 ErrTooLate）")

	err = c.SetHoliday(86400, 0, true)
	if !errors.Is(err, ErrTooLate) {
		t.Fatalf("current day: got %v want ErrTooLate", err)
	}
	if err := c.SetHoliday(10, 5, false); err != nil {
		t.Fatalf("unset future day: %v", err)
	}
	if c.IsHoliday(5) {
		t.Fatal("day 5 should no longer be holiday")
	}
	if err := c.SetHoliday(-1, 9, true); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("bad now: %v", err)
	}
}

func TestNextCurfewStart(t *testing.T) {
	c := New(0, 79200, 28800)
	if got := c.NextCurfewStart(36000); got != 79200 {
		t.Fatalf("next from day: got %d want 79200", got)
	}
	if got := c.NextCurfewStart(80000); got != 79200+86400 {
		t.Fatalf("next after cs: got %d want %d", got, 79200+86400)
	}
	none := New(0, 100, 100)
	if got := none.NextCurfewStart(0); got != -1 {
		t.Fatalf("no curfew: got %d want -1", got)
	}
}

func TestInvalidNew(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("New with bad params should panic")
		}
	}()
	New(99999, 0, 0)
}
