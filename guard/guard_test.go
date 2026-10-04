package guard

import (
	"errors"
	"testing"
)

func checkErr(t *testing.T, step string, got, want error) {
	t.Helper()
	t.Logf("输入 %s -> 输出 %v（判定依据 %v）", step, got, want)
	if !errors.Is(got, want) {
		t.Fatalf("%s: got %v want %v", step, got, want)
	}
}

func TestNewValidation(t *testing.T) {
	bad := []*Guard{
		New(-43201, 0, 0, 1, 1, 1, 1),
		New(50401, 0, 0, 1, 1, 1, 1),
		New(0, 86400, 0, 1, 1, 1, 1),
		New(0, 0, -1, 1, 1, 1, 1),
		New(0, 0, 0, -1, 1, 1, 1),
		New(0, 0, 0, 1, 86401, 1, 1),
		New(0, 0, 0, 1, 1, 0, 1),
		New(0, 0, 0, 1, 1, 1, 9),
	}
	for i, g := range bad {
		if g != nil {
			t.Fatalf("bad case %d must be rejected", i)
		}
	}
	t.Log("输入越界参数（tz/cs/ce/lw/lh/hb/dmax）-> 输出 nil，依据：构造期参数校验")
}

func TestSpecOverlapTimeoutBoundary(t *testing.T) {
	g := New(0, 79200, 28800, 5400, 5400, 600, 8)
	g.Register("a")
	checkErr(t, "Login(36000,d1)", g.Login(36000, "a", "d1"), nil)
	checkErr(t, "Login(36100,d2)", g.Login(36100, "a", "d2"), nil)
	checkErr(t, "Heartbeat(36500,d1)", g.Heartbeat(36500, "a", "d1"), nil)
	checkErr(t, "Logout(37000,d1)", g.Logout(37000, "a", "d1"), nil)
	r, err := g.Remaining(37000, "a")
	if err != nil || r != 4400 {
		t.Fatalf("Remaining: r=%d err=%v want 4400", r, err)
	}
	u, _ := g.Used(37000, "a", 0)
	t.Logf("Remaining(37000)=4400, Used=%d；依据：d2 在 last+HB=36700 逻辑超时，被 d1 区间覆盖，在线并集 [36000,37000)", u)

	g2 := New(0, 79200, 28800, 5400, 5400, 600, 8)
	g2.Register("a")
	g2.Login(40000, "a", "d1")
	checkErr(t, "Heartbeat(40599,d1)", g2.Heartbeat(40599, "a", "d1"), nil)

	g3 := New(0, 79200, 28800, 5400, 5400, 600, 8)
	g3.Register("a")
	g3.Login(40000, "a", "d1")
	checkErr(t, "Heartbeat(40600,d1)", g3.Heartbeat(40600, "a", "d1"), ErrDeviceOffline)
	u3, _ := g3.Used(40600, "a", 0)
	if u3 != 600 {
		t.Fatalf("used stops at logical timeout: %d want 600", u3)
	}
	t.Log("取等：Heartbeat(40600)=last+HB -> 设备不在线，Used 停在 600")
}

func TestSpecExhaustionAndRejections(t *testing.T) {
	g := New(0, 79200, 28800, 5400, 5400, 600, 8)
	g.Register("a")
	g.Login(39000, "a", "d1")
	// 每 500 秒心跳一次（500 < HB=600，永不超时），直到耗尽。
	for tm := int64(39500); tm < 44400; tm += 500 {
		if err := g.Heartbeat(tm, "a", "d1"); err != nil {
			t.Fatalf("heartbeat %d: %v", tm, err)
		}
	}
	u, _ := g.Used(44400, "a", 0)
	if u != 5400 {
		t.Fatalf("used at exhaustion %d want 5400", u)
	}
	checkErr(t, "Heartbeat(44400,d1)", g.Heartbeat(44400, "a", "d1"), ErrDeviceOffline)
	checkErr(t, "Login(44400,d1)", g.Login(44400, "a", "d1"), ErrQuotaExhausted)
	t.Log("每 500 秒心跳：39000 起 5400 秒恰在 44400 耗尽强制下线，依据：入账达到 Lw")
}

func TestCurfewForceOffline(t *testing.T) {
	g := New(0, 79200, 28800, 5400, 5400, 600, 8)
	g.Register("a")
	if err := g.Login(79140, "a", "d1"); err != nil {
		t.Fatal(err)
	}
	checkErr(t, "Heartbeat(79200,d1)", g.Heartbeat(79200, "a", "d1"), ErrDeviceOffline)
	u, _ := g.Used(79200, "a", 0)
	if u != 60 {
		t.Fatalf("curfew accrual: %d want 60", u)
	}
	checkErr(t, "Login(80000,d1)", g.Login(80000, "a", "d1"), ErrInCurfew)
	if err := g.Login(115200, "a", "d1"); err != nil {
		t.Fatalf("next day x=28800=ce should be allowed: %v", err)
	}
	t.Log("Login(79140) 于 79200 进入宵禁被强制下线（仅 60 秒）；次日 115200 取等 ce 解禁可登录")
}

func TestCrossDayHoliday(t *testing.T) {
	g := New(0, 0, 0, 1000, 7200, 3600, 8)
	g.Register("a")
	if err := g.SetHoliday(0, 1, true); err != nil {
		t.Fatal(err)
	}
	if err := g.Login(86000, "a", "d1"); err != nil {
		t.Fatal(err)
	}
	// 每 3000 秒心跳（< HB=3600），保证跨日持续在线到节假日额度耗尽。
	for tm := int64(89000); tm < 93600; tm += 3000 {
		if err := g.Heartbeat(tm, "a", "d1"); err != nil {
			t.Fatalf("heartbeat %d: %v", tm, err)
		}
	}
	// 耗尽后设备离线；查询只推进，不再接受心跳。
	u0, _ := g.Used(93600, "a", 0)
	u1, _ := g.Used(93600, "a", 1)
	t.Logf("跨日节假日：Used(日0)=%d Used(日1)=%d", u0, u1)
	if u0 != 400 || u1 != 7200 {
		t.Fatalf("got (%d,%d) want (400,7200)", u0, u1)
	}
	checkErr(t, "Heartbeat(93600,d1)", g.Heartbeat(93600, "a", "d1"), ErrDeviceOffline)
}

func TestEndOfDayExhaustion(t *testing.T) {
	g := New(0, 0, 0, 1400, 1400, 3600, 8)
	g.Register("a")
	g.Login(85000, "a", "d1") // 85000+1400=86400 恰为日界
	checkErr(t, "Heartbeat(86400,d1)", g.Heartbeat(86400, "a", "d1"), ErrDeviceOffline)
	u0, _ := g.Used(86400, "a", 0)
	if u0 != 1400 {
		t.Fatalf("day0 end exhaustion: %d", u0)
	}
	if err := g.Login(86400, "a", "d1"); err != nil {
		t.Fatalf("relogin next day: %v", err)
	}
	t.Log("日末 86400 恰好用满 1400 下线，次日须重新登录且成功")
}

func TestZeroQuotaDay(t *testing.T) {
	g := New(0, 0, 0, 3600, 0, 3600, 8)
	g.Register("a")
	g.Calendar().SetHoliday(0, 1, true)
	g.Login(86000, "a", "d1")
	checkErr(t, "Heartbeat(86400,d1)", g.Heartbeat(86400, "a", "d1"), ErrDeviceOffline)
	u1, _ := g.Used(86400, "a", 1)
	if u1 != 0 {
		t.Fatalf("zero quota day accrual: %d", u1)
	}
	checkErr(t, "Login(86400,d1)", g.Login(86400, "a", "d1"), ErrQuotaExhausted)
	t.Log("跨入节假日额度 0 的日：日界当下下线，登录报额度已尽")
}

func TestWithinDayCurfew(t *testing.T) {
	g := New(0, 36000, 60000, 3600, 3600, 3600, 8)
	g.Register("a")
	g.Login(35900, "a", "d1")
	checkErr(t, "Heartbeat(36000,d1)", g.Heartbeat(36000, "a", "d1"), ErrDeviceOffline)
	u, _ := g.Used(36000, "a", 0)
	if u != 100 {
		t.Fatalf("within-day curfew accrual: %d", u)
	}
	checkErr(t, "Login(36000,d1)", g.Login(36000, "a", "d1"), ErrInCurfew)
	if err := g.Login(60000, "a", "d1"); err != nil {
		t.Fatalf("ce exclusive release: %v", err)
	}
	t.Log("cs<ce 日内写法：x=cs 取等下线并拒登录，x=ce 取等解禁")
}

func TestNegativeTZ(t *testing.T) {
	g := New(-28800, 0, 0, 3600, 3600, 3600, 8)
	g.Register("a")
	// t=28000 本地为 28000-28800=-800，仍在日 -1（x=85600）。
	if err := g.Login(28000, "a", "d1"); err != nil {
		t.Fatal(err)
	}
	// 在日界 28800 那一刻查询：日 -1 入账 [100,28800)=28700，日 0 为 0。
	uNeg, _ := g.Used(28800, "a", -1)
	uZero, _ := g.Used(28800, "a", 0)
	t.Logf("tz=-28800, Login(28000) 本地属日 -1：Used(-1)=%d Used(0)=%d", uNeg, uZero)
	if uNeg != 800 || uZero != 0 {
		t.Fatalf("negative day split: (%d,%d) want (800,0)", uNeg, uZero)
	}
}

func TestClockBackwardAndMissingAccount(t *testing.T) {
	g := New(0, 0, 0, 3600, 3600, 100, 8)
	g.Register("a")
	g.Login(1000, "a", "d1")
	checkErr(t, "Login(999,...)", g.Login(999, "a", "d2"), ErrClockBackward)
	checkErr(t, "Login(1000,ghost,...)", g.Login(1000, "ghost", "d1"), ErrNoAccount)
	if _, err := g.Used(999, "a", 0); !errors.Is(err, ErrClockBackward) {
		t.Fatalf("Used backward: %v", err)
	}
}

func TestLoginRejectOrder(t *testing.T) {
	g := New(0, 79200, 28800, 3600, 3600, 3600, 1)
	g.Register("a")
	checkErr(t, "Login bad now", g.Login(-1, "a", "d"), ErrInvalidParam)
	if err := g.Login(40000, "a", "d1"); err != nil {
		t.Fatalf("accepted login: %v", err)
	}
	checkErr(t, "backward+ghost", g.Login(39999, "ghost", "d"), ErrClockBackward)
	checkErr(t, "missing acct", g.Login(40001, "ghost", "d"), ErrNoAccount)
	checkErr(t, "curfew over quota", g.Login(80000, "a", "d2"), ErrInCurfew)

	g2 := New(0, 0, 0, 0, 0, 100, 2)
	g2.Register("a")
	checkErr(t, "quota exhausted", g2.Login(1000, "a", "d1"), ErrQuotaExhausted)

	g3 := New(0, 0, 0, 3600, 3600, 3600, 1)
	g3.Register("a")
	if err := g3.Login(1000, "a", "d1"); err != nil {
		t.Fatal(err)
	}
	checkErr(t, "device already online", g3.Login(1000, "a", "d1"), ErrDeviceOnline)
	checkErr(t, "device limit", g3.Login(1000, "a", "d2"), ErrTooManyDevice)
	t.Log("拒绝次序：参数非法>时钟回退>账号不存在>宵禁>额度已尽>设备已在线>Dmax")
}

func TestRejectedOpDoesNotAdvance(t *testing.T) {
	g := New(0, 0, 0, 3600, 3600, 3600, 8)
	g.Register("a")
	if err := g.Login(1000, "a", "d1"); err != nil {
		t.Fatal(err)
	}
	// 被拒的重复登录不推进时钟：若已推进，后续 now=1500 会报时钟回退。
	if err := g.Login(2000, "a", "d1"); !errors.Is(err, ErrDeviceOnline) {
		t.Fatalf("dup login: %v", err)
	}
	checkErr(t, "Heartbeat(1500,d1) 证明时钟未推进", g.Heartbeat(1500, "a", "d1"), nil)
	u, _ := g.Used(1500, "a", 0)
	if u != 500 {
		t.Fatalf("accrual after rejected dup: %d want 500", u)
	}
	// 未注册账号的拒绝不影响已有时钟。
	if err := g.Login(2000, "ghost", "d"); !errors.Is(err, ErrNoAccount) {
		t.Fatalf("ghost: %v", err)
	}
	// 回退时钟操作被拒不改状态：2000 仍可心跳（last=1500，超时时刻 2500）。
	checkErr(t, "Heartbeat(2000,d1)", g.Heartbeat(2000, "a", "d1"), nil)
	// SetHoliday 为时已晚被拒不改时钟，之后 2000 时刻查询合法。
	if err := g.SetHoliday(2000, 0, true); !errors.Is(err, ErrTooLate) {
		t.Fatalf("holiday too late: %v", err)
	}
	checkErr(t, "Heartbeat(2000,d1) 再次成功", g.Heartbeat(2000, "a", "d1"), nil)
	u, _ = g.Used(2000, "a", 0)
	if u != 1000 {
		t.Fatalf("accrual must be exactly 1000: %d", u)
	}
	t.Log("被拒操作不改状态：时钟、设备在线集与 Used 均保持，判定依据见各错误返回")
}
