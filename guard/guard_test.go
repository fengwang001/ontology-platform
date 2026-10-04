package guard

import (
	"errors"
	"testing"
)

func mustNew(t *testing.T, tz, cs, ce, lw, lh, hb int64, dmax int) *Guard {
	t.Helper()
	g, err := New(tz, cs, ce, lw, lh, hb, dmax)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return g
}

func regLogin(t *testing.T, g *Guard, now int64, acct, dev string) {
	t.Helper()
	if err := g.Register(acct); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := g.Login(now, acct, dev); err != nil {
		t.Fatalf("Login(%d,%s,%s): %v", now, acct, dev, err)
	}
}

func checkErr(t *testing.T, name string, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("%s: err=%v want %v", name, got, want)
	}
}

// 题给例 1：多设备重叠、取等超时、耗尽、宵禁。
func TestSpecExampleOne(t *testing.T) {
	g := mustNew(t, 0, 79200, 28800, 5400, 5400, 600, 8)
	regLogin(t, g, 36000, "a", "d1")
	checkErr(t, "login d2", g.Login(36100, "a", "d2"), nil)
	checkErr(t, "hb d1 36500", g.Heartbeat(36500, "a", "d1"), nil)
	checkErr(t, "logout d1", g.Logout(37000, "a", "d1"), nil)
	if u, _ := g.Used(37000, "a", 0); u != 1000 {
		t.Fatalf("used=%d want 1000", u)
	}
	if r, _ := g.Remaining(37000, "a"); r != 4400 {
		t.Fatalf("remaining=%d want 4400", r)
	}
	if devs := g.onlineDevices(37000, "a"); len(devs) != 0 {
		t.Fatalf("all should be offline at 37000, got %v", devs)
	}

	// 40599 成功 / 40600 取等超时 / 每 500 秒心跳在 44400 耗尽：三个互斥分支独立重放。
	prelude := func(gg *Guard) {
		gg.Register("a")
		gg.Login(36000, "a", "d1")
		gg.Login(36100, "a", "d2")
		gg.Heartbeat(36500, "a", "d1")
		gg.Logout(37000, "a", "d1")
		gg.Login(40000, "a", "d1")
	}
	gOK := mustNew(t, 0, 79200, 28800, 5400, 5400, 600, 8)
	prelude(gOK)
	checkErr(t, "hb one second before timeout", gOK.Heartbeat(40599, "a", "d1"), nil)
	if u, _ := gOK.Used(40599, "a", 0); u != 1599 {
		t.Fatalf("used at 40599=%d want 1599", u)
	}
	gTO := mustNew(t, 0, 79200, 28800, 5400, 5400, 600, 8)
	prelude(gTO)
	checkErr(t, "hb at timeout instant", gTO.Heartbeat(40600, "a", "d1"), ErrDeviceOffline)
	if u, _ := gTO.Used(40600, "a", 0); u != 1600 {
		t.Fatalf("used after equal-timeout=%d want 1600", u)
	}
	gEx := mustNew(t, 0, 79200, 28800, 5400, 5400, 600, 8)
	prelude(gEx)
	for tm := int64(40500); tm < 44400; tm += 500 {
		if err := gEx.Heartbeat(tm, "a", "d1"); err != nil {
			t.Fatalf("hb %d: %v", tm, err)
		}
	}
	checkErr(t, "hb at exhaustion", gEx.Heartbeat(44400, "a", "d1"), ErrDeviceOffline)
	checkErr(t, "login at exhaustion", gEx.Login(44400, "a", "d2"), ErrQuotaExhausted)

	regLogin(t, g, 79140, "c", "d1")
	if devs := g.onlineDevices(79200, "c"); len(devs) != 0 {
		t.Fatalf("curfew forced offline failed: %v", devs)
	}
	if u, _ := g.Used(79200, "c", 0); u != 60 {
		t.Fatalf("curfew used=%d want 60", u)
	}
	checkErr(t, "login in curfew", g.Login(100000, "c", "d1"), ErrCurfew)
	if err := g.Login(115200, "c", "d1"); err != nil {
		t.Fatalf("login after curfew end: %v", err)
	}
}

// 题给例 2：跨日切分 + 节假日额度。
func TestSpecExampleHoliday(t *testing.T) {
	g := mustNew(t, 0, 0, 0, 3600, 7200, 600, 8)
	checkErr(t, "set holiday day1", g.SetHoliday(0, 1, true), nil)
	regLogin(t, g, 85000, "a", "d1")
	for tm := int64(85500); tm <= 93600; tm += 500 {
		g.Heartbeat(tm, "a", "d1")
	}
	if devs := g.onlineDevices(93600, "a"); len(devs) != 0 {
		t.Fatalf("should be forced offline at 93600: %v", devs)
	}
	if u, _ := g.Used(93600, "a", 0); u != 1400 {
		t.Fatalf("day0 used=%d want 1400", u)
	}
	if u, _ := g.Used(93600, "a", 1); u != 7200 {
		t.Fatalf("day1 used=%d want 7200", u)
	}
}

func TestNamedScenarios(t *testing.T) {
	t.Run("overlap and back-to-back sessions", func(t *testing.T) {
		g := mustNew(t, 0, 0, 0, 86400, 86400, 100, 8)
		g.Register("a")
		g.Login(1000, "a", "d1")
		g.Login(1010, "a", "d2")
		g.Logout(1020, "a", "d1")
		g.Logout(1030, "a", "d2")
		if u, _ := g.Used(1030, "a", 0); u != 30 {
			t.Fatalf("overlap used=%d want 30", u)
		}
		if err := g.Login(1030, "a", "d1"); err != nil {
			t.Fatal(err)
		}
		if err := g.Logout(1060, "a", "d1"); err != nil {
			t.Fatal(err)
		}
		if u, _ := g.Used(1060, "a", 0); u != 60 {
			t.Fatalf("back-to-back used=%d want 60", u)
		}
	})

	t.Run("exhausted exactly at day end", func(t *testing.T) {
		g := mustNew(t, 0, 0, 0, 100, 100, 600, 8)
		regLogin(t, g, 86300, "a", "d1")
		if devs := g.onlineDevices(86400, "a"); len(devs) != 0 {
			t.Fatalf("must offline at midnight when quota fills: %v", devs)
		}
		if u, _ := g.Used(86400, "a", 0); u != 100 {
			t.Fatalf("day0 used=%d want 100", u)
		}
		if r, _ := g.Remaining(86400, "a"); r != 100 {
			t.Fatalf("day1 fresh remaining=%d want 100", r)
		}
		checkErr(t, "relogin next day", g.Login(86400, "a", "d1"), nil)
	})

	t.Run("cross into zero-quota day", func(t *testing.T) {
		g := mustNew(t, 0, 0, 0, 1000, 0, 3600, 8)
		checkErr(t, "holiday day1 lh=0", g.SetHoliday(0, 1, true), nil)
		regLogin(t, g, 86300, "a", "d1")
		if devs := g.onlineDevices(86400, "a"); len(devs) != 0 {
			t.Fatalf("zero-quota day boundary must force offline: %v", devs)
		}
		if u, _ := g.Used(86400, "a", 0); u != 100 {
			t.Fatalf("day0 used=%d want 100", u)
		}
		if u, _ := g.Used(86400, "a", 1); u != 0 {
			t.Fatalf("day1 used=%d want 0", u)
		}
		checkErr(t, "login zero-quota day", g.Login(86400, "a", "d1"), ErrQuotaExhausted)
	})

	t.Run("curfew within day boundary equal", func(t *testing.T) {
		g := mustNew(t, 0, 1000, 2000, 86400, 86400, 3600, 8)
		regLogin(t, g, 900, "a", "d1")
		if devs := g.onlineDevices(1000, "a"); len(devs) != 0 {
			t.Fatalf("cs inclusive should force offline: %v", devs)
		}
		if u, _ := g.Used(1000, "a", 0); u != 100 {
			t.Fatalf("used=%d want 100", u)
		}
		checkErr(t, "login inside curfew", g.Login(1500, "a", "d1"), ErrCurfew)
		checkErr(t, "relogin at ce", g.Login(2000, "a", "d1"), nil)
	})

	t.Run("curfew wrapping midnight boundary equal", func(t *testing.T) {
		g := mustNew(t, 0, 2000, 1000, 86400, 86400, 3600, 8)
		regLogin(t, g, 1900, "a", "d1")
		if devs := g.onlineDevices(2000, "a"); len(devs) != 0 {
			t.Fatalf("wrap cs inclusive should force offline: %v", devs)
		}
		checkErr(t, "login night curfew", g.Login(86399, "a", "d1"), ErrCurfew)
		checkErr(t, "login midnight curfew", g.Login(86400, "a", "d1"), ErrCurfew)
		checkErr(t, "relogin at next-day ce", g.Login(87400, "a", "d1"), nil)
	})

	t.Run("negative t+tz", func(t *testing.T) {
		// 题给时区：t=100 时本地 day=-1 x=57700（calendar 测试覆盖）。
		// tz=-28800 时本地日 0 = UTC [28800,115200)；113000 落在 day0 x=84200，
		// 跨日界 115200 后到 115500：day0 计 2200，day1 计 300，设备保持在线。
		g := mustNew(t, -28800, 0, 0, 86400, 86400, 3600, 8)
		regLogin(t, g, 113000, "a", "d1")
		if devs := g.onlineDevices(115500, "a"); len(devs) != 1 {
			t.Fatalf("should stay online across local midnight: %v", devs)
		}
		if u, _ := g.Used(115500, "a", 0); u != 2200 {
			t.Fatalf("day0 used=%d want 2200", u)
		}
		if u, _ := g.Used(115500, "a", 1); u != 300 {
			t.Fatalf("day1 used=%d want 300", u)
		}
	})

	t.Run("SetHoliday too late", func(t *testing.T) {
		g := mustNew(t, 0, 0, 0, 100, 100, 3600, 8)
		checkErr(t, "set future", g.SetHoliday(86399, 1, true), nil)
		checkErr(t, "day start equal now", g.SetHoliday(86400, 1, false), ErrTooLate)
		checkErr(t, "past day still advances clock", g.SetHoliday(90000, 0, true), ErrTooLate)
	})

	t.Run("rejected op changes nothing", func(t *testing.T) {
		g := mustNew(t, 0, 0, 0, 100, 100, 100, 8)
		regLogin(t, g, 10, "a", "d1")
		uBefore, _ := g.Used(50, "a", 0)
		checkErr(t, "clock back", g.Heartbeat(5, "a", "d1"), ErrClockBack)
		checkErr(t, "unknown account", g.Heartbeat(50, "zz", "d1"), ErrNoAccount)
		checkErr(t, "offline device", g.Heartbeat(50, "a", "dX"), ErrDeviceOffline)
		// 被拒后合法心跳仍应成功，且已用与无被拒操作时一致。
		checkErr(t, "hb after rejections", g.Heartbeat(50, "a", "d1"), nil)
		if u, _ := g.Used(50, "a", 0); u != uBefore {
			t.Fatalf("rejected ops changed used: %d vs %d", u, uBefore)
		}
	})

	t.Run("login rejection order", func(t *testing.T) {
		g := mustNew(t, 0, 1000, 2000, 0, 0, 3600, 1)
		checkErr(t, "empty acct", g.Login(10, "", "d"), ErrInvalidParam)
		checkErr(t, "bad now", g.Login(-1, "a", "d"), ErrInvalidParam)
		checkErr(t, "no account beats curfew", g.Login(1500, "zz", "d"), ErrNoAccount)
		g.Register("a")
		checkErr(t, "curfew first", g.Login(1500, "a", "d"), ErrCurfew)
		// 非宵禁、零额度：额度先于设备冲突。
		checkErr(t, "quota before dup", g.Login(2100, "a", "d1"), ErrQuotaExhausted)
		g2 := mustNew(t, 0, 0, 0, 86400, 86400, 3600, 1)
		g2.Register("a")
		g2.Login(10, "a", "d1")
		checkErr(t, "same device", g2.Login(20, "a", "d1"), ErrDeviceOnline)
		checkErr(t, "device cap", g2.Login(20, "a", "d2"), ErrDeviceLimit)
		// 时钟回退优先于所有业务判定。
		checkErr(t, "clock back", g2.Login(5, "a", "d3"), ErrClockBack)
	})

	t.Run("steps zero while offline regardless of gap", func(t *testing.T) {
		for _, gap := range []int64{86400, 10000 * 86400} {
			g := mustNew(t, 0, 0, 0, 86400, 86400, 100, 8)
			g.Register("a")
			if s := g.Steps("a"); s != 0 {
				t.Fatalf("fresh steps=%d want 0", s)
			}
			if _, err := g.Remaining(gap, "a"); err != nil {
				t.Fatal(err)
			}
			if s := g.Steps("a"); s != 0 {
				t.Fatalf("offline gap=%d steps=%d want 0", gap, s)
			}
		}
	})

	t.Run("steps bounded online", func(t *testing.T) {
		g := mustNew(t, 0, 0, 0, 86400, 86400, 100, 3)
		regLogin(t, g, 0, "a", "d1")
		g.Login(10, "a", "d2")
		g.Login(20, "a", "d3")
		// 单次入口推进跨越 3 个日界；期间设备各自超时一次。
		// 步数上界 = 在线设备数 + 2*跨越日数 + 2 = 3 + 6 + 2 = 11。
		before := g.Steps("a")
		if _, err := g.Remaining(3*86400, "a"); err != nil {
			t.Fatal(err)
		}
		s := g.Steps("a") - before
		bound := 3 + 2*3 + 2
		if s > bound {
			t.Fatalf("steps=%d exceeds bound %d", s, bound)
		}
	})

	// 回归：推进途中设备被强制下线后结算点必须跳到 now，
	// 否则同刻新登录设备会在下一个同刻操作被历史日界事件误踢。
	t.Run("exhaustion then same-instant login survives", func(t *testing.T) {
		g := mustNew(t, 14400, 64800, 64800, 900, 300, 600, 3)
		g.Register("a")
		g.Login(258600, "a", "d2")
		if err := g.Login(259200, "a", "d0"); err != nil {
			t.Fatalf("login d0 at boundary/timeout: %v", err)
		}
		checkErr(t, "hb d0 same instant", g.Heartbeat(259200, "a", "d0"), nil)
	})
}
