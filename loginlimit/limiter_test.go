package loginlimit

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func testConfig() Config {
	return Config{
		Window:    time.Minute,
		Threshold: 3,
		BaseLock:  10 * time.Second,
		MaxLock:   100 * time.Second,
		Cooldown:  5 * time.Minute,
	}
}

func fmtUnlock(u time.Time) string {
	if u.IsZero() {
		return "-"
	}
	return u.Format(time.RFC3339)
}

func explain(res Result, ok bool) string {
	switch res.Reason {
	case ReasonEmptyAccount:
		return "账号为空，参数校验拒绝"
	case ReasonEmptySource:
		return "来源为空，参数校验拒绝"
	case ReasonClockBackwards:
		return "时钟早于已见最大读数，拒绝"
	case ReasonLocked:
		return "键处于锁定期(at<lockUntil)，不计失败、不延长锁定"
	case ReasonBadPassword:
		return "未锁定且口令错误，两个键各记一次失败"
	default:
		if ok {
			return "未锁定且口令正确，清空账号键失败记录与级别"
		}
		return "放行"
	}
}

// logAttempt 打印输入、输出与判定依据。
func logAttempt(t *testing.T, tag, account, source string, at time.Time, ok bool, res Result) {
	t.Helper()
	t.Logf("%s: 输入={account:%q source:%q at:%s passwordOK:%v} 输出={allowed:%v reason:%q unlockAt:%s dims:%v} 判定依据=%s",
		tag, account, source, at.Format(time.RFC3339), ok,
		res.Allowed, res.Reason, fmtUnlock(res.UnlockAt), res.LockedDimensions,
		explain(res, ok))
}

func try(t *testing.T, l *Limiter, tag, account, source string, at time.Time, ok bool) Result {
	t.Helper()
	res := l.Attempt(account, source, at, ok)
	logAttempt(t, tag, account, source, at, ok, res)
	return res
}

func TestNewRejectsInvalidConfig(t *testing.T) {
	base := testConfig()
	cases := []struct {
		name string
		mut  func(*Config)
	}{
		{"window 非正", func(c *Config) { c.Window = 0 }},
		{"threshold 非正", func(c *Config) { c.Threshold = -1 }},
		{"baseLock 非正", func(c *Config) { c.BaseLock = 0 }},
		{"maxLock 非正", func(c *Config) { c.MaxLock = 0 }},
		{"cooldown 非正", func(c *Config) { c.Cooldown = 0 }},
		{"maxLock 小于 baseLock", func(c *Config) { c.MaxLock = c.BaseLock - time.Second }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := base
			tc.mut(&cfg)
			_, err := New(cfg)
			if err == nil {
				t.Fatalf("期望参数被拒绝: %s", tc.name)
			}
			t.Logf("输入=%+v 输出=err:%v 判定依据=参数非正或 M<B", cfg, err)
		})
	}
}

func TestValidationOrder(t *testing.T) {
	l, err := New(testConfig())
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Unix(1000, 0)
	try(t, l, "建立时钟", "u", "ip", t0, true)

	// 按顺序只报第一个原因：空账号 → 空来源 → 时钟回退。
	if r := l.Attempt("", "ip", t0.Add(time.Second), false); r.Reason != ReasonEmptyAccount {
		t.Fatalf("空账号: got %s", r.Reason)
	}
	if r := l.Attempt("u", "", t0.Add(time.Second), false); r.Reason != ReasonEmptySource {
		t.Fatalf("空来源: got %s", r.Reason)
	}
	if r := l.Attempt("u", "ip", t0.Add(-time.Second), false); r.Reason != ReasonClockBackwards {
		t.Fatalf("时钟回退: got %s", r.Reason)
	}
}

func TestLockedRejectsEvenCorrectPassword(t *testing.T) {
	l, _ := New(testConfig())
	t0 := time.Unix(1000, 0)
	// 3 次错误口令触发锁定，最后一次失败在 t0+2s，锁定到该时刻 +10s。
	for i := 0; i < 3; i++ {
		r := try(t, l, "失败", "u", "ip", t0.Add(time.Duration(i)*time.Second), false)
		if r.Reason != ReasonBadPassword {
			t.Fatalf("第%d次: %s", i+1, r.Reason)
		}
	}

	// 锁定期内即使口令正确也必须被拒。
	r := try(t, l, "锁定期内正确口令", "u", "ip", t0.Add(5*time.Second), true)
	if r.Reason != ReasonLocked || r.Allowed {
		t.Fatalf("锁定期内应拒绝: %+v", r)
	}
	wantUnlock := t0.Add(2 * time.Second).Add(10 * time.Second)
	if !r.UnlockAt.Equal(wantUnlock) {
		t.Fatalf("解锁时刻=%s want=%s", r.UnlockAt, wantUnlock)
	}

	// 锁定期满后正确口令放行（触发时失败记录已清空）。
	r = try(t, l, "解锁后正确口令", "u", "ip", t0.Add(12*time.Second), true)
	if !r.Allowed {
		t.Fatalf("解锁后应放行: %+v", r)
	}
}

func TestFirstFailureAfterUnlockReaccumulates(t *testing.T) {
	l, _ := New(testConfig())
	t0 := time.Unix(2000, 0)
	for i := 0; i < 3; i++ {
		try(t, l, "失败", "u", "ip", t0.Add(time.Duration(i)*time.Second), false)
	}

	// 锁定期满后首次失败重新累计：2 次内不锁定。
	at := t0.Add(20 * time.Second)
	try(t, l, "解锁后失败1", "u", "ip", at, false)
	r := try(t, l, "解锁后失败2", "u", "ip", at.Add(time.Second), false)
	if r.Reason != ReasonBadPassword {
		t.Fatalf("重新累计期间应只报口令错误: %+v", r)
	}
	// 第 3 次触发二级锁定，时长 B*2=20s；触发当次仍报口令错误。
	r = try(t, l, "解锁后失败3(触发)", "u", "ip", at.Add(2*time.Second), false)
	if r.Reason != ReasonBadPassword {
		t.Fatalf("触发当次应报口令错误: %+v", r)
	}
	r = try(t, l, "确认二级锁定", "u", "ip", at.Add(3*time.Second), true)
	if r.Reason != ReasonLocked {
		t.Fatalf("期望二级锁定: %+v", r)
	}
	wantUnlock := at.Add(2 * time.Second).Add(20 * time.Second)
	if !r.UnlockAt.Equal(wantUnlock) {
		t.Fatalf("二级时长应为 20s: unlock=%s want=%s", r.UnlockAt, wantUnlock)
	}
}

func TestLockDurationDoublesAndCaps(t *testing.T) {
	cfg := testConfig()
	l, _ := New(cfg)
	t0 := time.Unix(3000, 0)

	// 逐级触发：10s, 20s, 40s, 80s，之后封顶 100s。
	wantDurs := []time.Duration{
		10 * time.Second, 20 * time.Second, 40 * time.Second,
		80 * time.Second, 100 * time.Second, 100 * time.Second,
	}
	at := t0
	for level, want := range wantDurs {
		for k := 0; k < cfg.Threshold; k++ {
			r := l.Attempt("u", "ip", at.Add(time.Duration(k)*time.Second), false)
			if r.Reason != ReasonBadPassword {
				t.Fatalf("level=%d k=%d 意外结果: %+v", level+1, k, r)
			}
		}
		last := at.Add(2 * time.Second)
		r := l.Attempt("u", "ip", last.Add(time.Second), true)
		if r.Reason != ReasonLocked || !r.UnlockAt.Equal(last.Add(want)) {
			t.Fatalf("level=%d 时长 want=%s got unlock=%s (%+v)", level+1, want, r.UnlockAt, r)
		}
		t.Logf("级别%d: 输入=3次错误 输出=锁定至%s 判定依据=min(B*2^(j-1),M) want=%s",
			level+1, r.UnlockAt.Format(time.RFC3339), want)
		// 每次仅越过锁定期、未达到冷却 R，使级别继续累加。
		at = last.Add(want).Add(time.Second)
	}
}

func TestCooldownResetsLevel(t *testing.T) {
	cfg := testConfig()
	l, _ := New(cfg)
	t0 := time.Unix(4000, 0)

	// 触发 j=1 锁定（截止 t0+2s+10s）。
	for k := 0; k < 3; k++ {
		l.Attempt("u", "ip", t0.Add(time.Duration(k)*time.Second), false)
	}
	firstUnlock := t0.Add(2 * time.Second).Add(10 * time.Second)

	// 越过锁定截止 + 冷却 R 后再失败：j 归零后重新加一，时长回到 10s。
	at := firstUnlock.Add(cfg.Cooldown)
	for k := 0; k < 3; k++ {
		l.Attempt("u", "ip", at.Add(time.Duration(k)*time.Second), false)
	}
	r := l.Attempt("u", "ip", at.Add(3*time.Second), true)
	wantUnlock := at.Add(2 * time.Second).Add(10 * time.Second)
	if r.Reason != ReasonLocked || !r.UnlockAt.Equal(wantUnlock) {
		t.Fatalf("冷却后级别应归零、时长回到 10s: %+v want=%s", r, wantUnlock)
	}
	t.Logf("输入=解锁后等待R再3次失败 输出=锁定至%s 判定依据=at>=上次lockUntil+R 时 j 归零",
		r.UnlockAt.Format(time.RFC3339))
}

func TestSuccessClearsOnlyAccountKey(t *testing.T) {
	l, _ := New(testConfig())
	t0 := time.Unix(5000, 0)

	// 账号 u 与来源 ip 各累计 2 次失败。
	for k := 0; k < 2; k++ {
		try(t, l, "失败累计", "u", "ip", t0.Add(time.Duration(k)*time.Second), false)
	}
	// 成功：清账号键失败记录与级别，来源键保留。
	try(t, l, "成功登录", "u", "ip", t0.Add(2*time.Second), true)

	// 新账号 v 从同一来源 ip 失败：账号 v 仅 1 次，
	// 但来源 ip 已有成功前的 2 次 + 本次 = 3 次，触发来源锁定（当次仍报口令错误）。
	r := try(t, l, "新账号失败触发来源锁", "v", "ip", t0.Add(3*time.Second), false)
	if r.Reason != ReasonBadPassword {
		t.Fatalf("触发当次应报口令错误: %+v", r)
	}
	// 后续任意账号从 ip 来的尝试（即使口令正确）被来源锁拒绝。
	r = try(t, l, "来源锁定后", "v", "ip", t0.Add(4*time.Second), true)
	if r.Reason != ReasonLocked || len(r.LockedDimensions) != 1 ||
		r.LockedDimensions[0] != DimensionSource {
		t.Fatalf("应仅来源维度锁定: %+v", r)
	}
	// 账号 u 未被锁定（成功已清其失败记录）：换干净来源可正常登录。
	r = try(t, l, "账号u未受影响", "u", "clean-ip", t0.Add(5*time.Second), true)
	if !r.Allowed {
		t.Fatalf("账号 u 应可从干净来源登录: %+v", r)
	}
}

func TestSourceSprayLocksSource(t *testing.T) {
	l, _ := New(testConfig())
	t0 := time.Unix(6000, 0)

	// 同一来源对 3 个不同账号各试一次错误口令：每个账号仅 1 次失败，
	// 来源键却达 3 次，触发来源锁定；触发当次仍只报口令错误。
	for i := 0; i < 3; i++ {
		r := try(t, l, "喷洒", fmt.Sprintf("u%d", i), "evil-ip",
			t0.Add(time.Duration(i)*time.Second), false)
		if r.Reason != ReasonBadPassword {
			t.Fatalf("喷洒第%d次应只报口令错误: %+v", i+1, r)
		}
	}

	r := try(t, l, "来源锁定后", "anyone", "evil-ip", t0.Add(3*time.Second), true)
	if r.Reason != ReasonLocked {
		t.Fatalf("期望来源锁定: %+v", r)
	}
	if len(r.LockedDimensions) != 1 || r.LockedDimensions[0] != DimensionSource {
		t.Fatalf("应仅报告来源维度: %+v", r.LockedDimensions)
	}
	// 干净来源不受影响。
	r = try(t, l, "干净来源", "anyone", "clean-ip", t0.Add(4*time.Second), true)
	if !r.Allowed {
		t.Fatalf("干净来源应放行: %+v", r)
	}
}

func TestBothLockedReportsLaterUnlock(t *testing.T) {
	cfg := testConfig()
	cfg.Window = 10 * time.Second // 让上一轮失败在下一轮之前滑出窗口
	cfg.Cooldown = time.Hour      // 保证级别不被冷却归零
	l, _ := New(cfg)
	t0 := time.Unix(7000, 0)

	// 账号 u 锁定 j=1（10s）。
	for k := 0; k < 3; k++ {
		l.Attempt("u", "ip1", t0.Add(time.Duration(k)*time.Second), false)
	}
	accountUnlock := t0.Add(2 * time.Second).Add(10 * time.Second)

	// 等账号锁过期后，用来源 ip2 给 u 连续失败，使账号以 j=2 锁定（20s）。
	at := accountUnlock.Add(time.Second)
	for k := 0; k < 3; k++ {
		l.Attempt("u", "ip2", at.Add(time.Duration(k)*time.Second), false)
	}
	accountUnlock2 := at.Add(2 * time.Second).Add(20 * time.Second)

	// 等账号锁再次过期；用另一账号 v 与来源 ip3 制造来源 j=1 锁定，
	// 随后让 u 在 ip3 上失败 3 次，使两个键同时在锁且截止时刻不同。
	at = accountUnlock2.Add(time.Second)
	for k := 0; k < 3; k++ {
		l.Attempt("v", "ip3", at.Add(time.Duration(k)*time.Second), false)
	}
	// ip3 当前锁定到 at+2s+10s；直接在其锁定期内尝试 u/ip3，
	// 此时仅来源在锁。再等来源解锁后同时触发两键锁定。
	sourceUnlock := at.Add(2 * time.Second).Add(10 * time.Second)
	at3 := sourceUnlock.Add(time.Second)
	// u 账号 3 次失败：来源 ip3 第 3 次先到（双方都在第 3 次触发），
	// 账号 j=3 → 40s；来源 j=2 → 20s。
	for k := 0; k < 3; k++ {
		r := l.Attempt("u", "ip3", at3.Add(time.Duration(k)*time.Second), false)
		if r.Reason != ReasonBadPassword {
			t.Fatalf("k=%d 应报口令错误: %+v", k, r)
		}
	}
	r := l.Attempt("u", "ip3", at3.Add(3*time.Second), true)
	if r.Reason != ReasonLocked {
		t.Fatalf("两键应同时锁定: %+v", r)
	}
	if len(r.LockedDimensions) != 2 {
		t.Fatalf("应报告两个维度: %+v", r.LockedDimensions)
	}
	// 账号截止更晚（40s > 20s），应取较晚者。
	wantLater := at3.Add(2 * time.Second).Add(40 * time.Second)
	if !r.UnlockAt.Equal(wantLater) {
		t.Fatalf("应取较晚解锁时刻: got=%s want=%s", r.UnlockAt, wantLater)
	}
	t.Logf("输入=两键同时锁定 输出=dims=%v unlockAt=%s 判定依据=取较晚 lockUntil",
		r.LockedDimensions, r.UnlockAt.Format(time.RFC3339))
}

func TestConcurrentFailuresAreNotLost(t *testing.T) {
	cfg := testConfig()
	cfg.Threshold = 200
	l, _ := New(cfg)
	at := time.Unix(8000, 0)

	// 200 个并发失败（不同账号、同一来源）：来源键恰好在第 200 次触发锁定，
	// 失败不丢、级别只加一。
	var wg sync.WaitGroup
	results := make([]Reason, 200)
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r := l.Attempt(fmt.Sprintf("u%03d", i), "ip", at, false)
			results[i] = r.Reason
		}(i)
	}
	wg.Wait()

	for i, r := range results {
		if r != ReasonBadPassword {
			t.Fatalf("工作尝试 %d 应报口令错误（触发当次不锁定）: %s", i, r)
		}
	}
	// 紧接着的任意尝试必须被来源锁拒绝。
	r := l.Attempt("anyone", "ip", at.Add(time.Second), true)
	if r.Reason != ReasonLocked || len(r.LockedDimensions) != 1 ||
		r.LockedDimensions[0] != DimensionSource {
		t.Fatalf("来源应已锁定且级别为 1: %+v", r)
	}
	wantUnlock := at.Add(cfg.BaseLock)
	if !r.UnlockAt.Equal(wantUnlock) {
		t.Fatalf("并发触发应只加一级(10s): got=%s want=%s", r.UnlockAt, wantUnlock)
	}
	t.Logf("输入=200个并发失败 输出=全部bad_password, 随后来源锁定至%s 判定依据=互斥串行、失败不丢、级别+1",
		r.UnlockAt.Format(time.RFC3339))
}

func TestConcurrentAttemptsDuringLockAllRejected(t *testing.T) {
	l, _ := New(testConfig())
	t0 := time.Unix(9000, 0)
	for k := 0; k < 3; k++ {
		l.Attempt("u", "ip", t0.Add(time.Duration(k)*time.Second), false)
	}
	unlock := t0.Add(2 * time.Second).Add(10 * time.Second)

	// 锁定期内 50 个并发尝试（口令正确）全部被拒，且解锁时刻不被延长。
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := l.Attempt("u", "ip", t0.Add(5*time.Second), true)
			if r.Reason != ReasonLocked || !r.UnlockAt.Equal(unlock) {
				t.Errorf("锁定期并发尝试结果异常: %+v", r)
			}
		}()
	}
	wg.Wait()

	r := l.Attempt("u", "ip", t0.Add(6*time.Second), true)
	if r.Reason != ReasonLocked || !r.UnlockAt.Equal(unlock) {
		t.Fatalf("状态不应被锁定期尝试改变: %+v want unlock=%s", r, unlock)
	}
}

type call struct {
	account string
	source  string
	offset  time.Duration
	ok      bool
}

func TestSameSequenceIsDeterministic(t *testing.T) {
	base := time.Unix(10000, 0)
	calls := []call{
		{"u", "ip", 0, false},
		{"u", "ip", time.Second, false},
		{"u", "ip", 2 * time.Second, false},
		{"u", "ip", 3 * time.Second, true},
		{"", "ip", 4 * time.Second, false},
		{"u", "", 5 * time.Second, false},
		{"u2", "ip", 6 * time.Second, false},
		{"u2", "ip", 7 * time.Second, true},
		{"u2", "ip", 20 * time.Second, false},
	}
	run := func() []Result {
		l, _ := New(testConfig())
		out := make([]Result, len(calls))
		for i, c := range calls {
			out[i] = l.Attempt(c.account, c.source, base.Add(c.offset), c.ok)
		}
		return out
	}
	a, b := run(), run()
	if len(a) != len(b) {
		t.Fatal("结果长度不一致")
	}
	for i := range a {
		if !resultsEqual(a[i], b[i]) {
			t.Fatalf("相同序列结果不确定 @%d: %+v != %+v", i, a[i], b[i])
		}
		if a[i].Allowed && a[i].Reason != "" {
			t.Fatalf("放行结果不应带原因 @%d", i)
		}
	}
	t.Logf("输入=%d 次固定调用序列 输出=两次运行逐字段一致 判定依据=纯状态机、时钟单调", len(calls))
}

func resultsEqual(x, y Result) bool {
	if x.Allowed != y.Allowed || x.Reason != y.Reason ||
		!x.UnlockAt.Equal(y.UnlockAt) || len(x.LockedDimensions) != len(y.LockedDimensions) {
		return false
	}
	for i := range x.LockedDimensions {
		if x.LockedDimensions[i] != y.LockedDimensions[i] {
			return false
		}
	}
	return true
}
