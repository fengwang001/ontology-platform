package refresh

import (
	"bytes"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	testL = time.Hour
	testD = 24 * time.Hour
	testG = 5 * time.Minute
)

var t0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func newTestDetector(t *testing.T) (*Detector, *bytes.Buffer) {
	t.Helper()
	var log bytes.Buffer
	d, err := New(Config{L: testL, D: testD, G: testG, Log: &log})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return d, &log
}

func mustLogin(t *testing.T, d *Detector, at time.Time) *Token {
	t.Helper()
	tok, err := d.Login(at)
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	return tok
}

func mustRenew(t *testing.T, d *Detector, id string, at time.Time) *Token {
	t.Helper()
	tok, err := d.Renew(id, at)
	if err != nil {
		t.Fatalf("Renew(%s): %v", id, err)
	}
	return tok
}

func TestConfigValidation(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
		want error
	}{
		{"L zero", Config{L: 0, D: testD, G: testG}, ErrNonPositiveL},
		{"L negative", Config{L: -1, D: testD, G: testG}, ErrNonPositiveL},
		{"D zero", Config{L: testL, D: 0, G: testG}, ErrNonPositiveD},
		{"D negative", Config{L: testL, D: -1, G: testG}, ErrNonPositiveD},
		{"G negative", Config{L: testL, D: testD, G: -1}, ErrNegativeG},
		{"G equals L", Config{L: testL, D: testD, G: testL}, ErrGraceTooLarge},
		{"G greater than L", Config{L: testL, D: testD, G: 2 * testL}, ErrGraceTooLarge},
		{"D smaller than L", Config{L: testL, D: testL - 1, G: 0}, ErrAbsoluteShorter},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := New(tc.cfg); !errors.Is(err, tc.want) {
				t.Fatalf("New = %v, want %v", err, tc.want)
			}
		})
	}
	// 边界合法：G 允许为 0，D 允许等于 L。
	if _, err := New(Config{L: testL, D: testL, G: 0}); err != nil {
		t.Fatalf("New boundary config: %v", err)
	}
}

func TestLoginIssuesGlobalIDs(t *testing.T) {
	d, _ := newTestDetector(t)
	t1 := mustLogin(t, d, t0)
	t2 := mustLogin(t, d, t0.Add(time.Second))
	if t1.ID != "t1" || t1.FamilyID != "f1" {
		t.Fatalf("first login = %+v, want t1/f1", t1)
	}
	if t2.ID != "t2" || t2.FamilyID != "f2" {
		t.Fatalf("second login = %+v, want t2/f2", t2)
	}
	if !t1.Expires.Equal(t0.Add(testL)) {
		t.Fatalf("first token expires %s, want %s", t1.Expires, t0.Add(testL))
	}
}

func TestRotationAndTheftRevokesFamily(t *testing.T) {
	d, _ := newTestDetector(t)
	t1 := mustLogin(t, d, t0)
	t2 := mustRenew(t, d, t1.ID, t0.Add(time.Minute))
	if t2.ID != "t2" || t2.FamilyID != t1.FamilyID {
		t.Fatalf("renewed = %+v, want t2 in %s", t2, t1.FamilyID)
	}
	// 旧令牌在宽限外再次出示 -> 盗用，家族整体吊销。
	if _, err := d.Renew(t1.ID, t0.Add(time.Hour)); !errors.Is(err, ErrReuseDetected) {
		t.Fatalf("old token outside grace: err=%v, want ErrReuseDetected", err)
	}
	// 盗用判定后，最新令牌也立即不可用。
	if _, err := d.Renew(t2.ID, t0.Add(time.Hour)); !errors.Is(err, ErrFamilyRevoked) {
		t.Fatalf("current token after theft: err=%v, want ErrFamilyRevoked", err)
	}
}

func TestRetryWithinGraceReturnsSameSuccessor(t *testing.T) {
	d, log := newTestDetector(t)
	t1 := mustLogin(t, d, t0)
	t2 := mustRenew(t, d, t1.ID, t0.Add(10*time.Minute))

	// 宽限内重试：不签发新令牌、原样返回后继、家族不变。
	retry := mustRenew(t, d, t1.ID, t0.Add(12*time.Minute))
	if retry.ID != t2.ID {
		t.Fatalf("retry returned %s, want same successor %s", retry.ID, t2.ID)
	}
	if !retry.Expires.Equal(t2.Expires) || retry.FamilyID != t2.FamilyID {
		t.Fatalf("retry token = %+v, want identical to %+v", retry, t2)
	}

	// 家族状态未变：当前令牌仍是 t2，再续期得到 t3（而非 t4）。
	t3 := mustRenew(t, d, t2.ID, t0.Add(20*time.Minute))
	if t3.ID != "t3" {
		t.Fatalf("got %s, want t3", t3.ID)
	}

	// 日志记录输入、输出与「重试」判定依据。
	if !strings.Contains(log.String(), "input=Renew token=t1") ||
		!strings.Contains(log.String(), "retry-successor=t2") ||
		!strings.Contains(log.String(), "reused within grace") {
		t.Fatalf("log missing retry rationale:\n%s", log.String())
	}
}

func TestGraceBoundary(t *testing.T) {
	// elapsed == G 不属于「不足宽限」，按盗用处理。
	d, _ := newTestDetector(t)
	t1 := mustLogin(t, d, t0)
	rotateAt := t0.Add(10 * time.Minute)
	t2 := mustRenew(t, d, t1.ID, rotateAt)

	_, err := d.Renew(t1.ID, rotateAt.Add(testG))
	if !errors.Is(err, ErrReuseDetected) {
		t.Fatalf("at exact grace boundary: err=%v, want ErrReuseDetected", err)
	}
	if _, err := d.Renew(t2.ID, rotateAt.Add(testG)); !errors.Is(err, ErrFamilyRevoked) {
		t.Fatalf("boundary theft should revoke family, got %v", err)
	}

	// 严格小于 G 则是重试。
	d2, _ := newTestDetector(t)
	u1 := mustLogin(t, d2, t0)
	u2 := mustRenew(t, d2, u1.ID, rotateAt)
	got := mustRenew(t, d2, u1.ID, rotateAt.Add(testG-time.Nanosecond))
	if got.ID != u2.ID {
		t.Fatalf("just inside grace returned %s, want %s", got.ID, u2.ID)
	}
}

func TestSuccessorMovedTriggersRevocation(t *testing.T) {
	d, _ := newTestDetector(t)
	t1 := mustLogin(t, d, t0)
	t2 := mustRenew(t, d, t1.ID, t0.Add(time.Minute))
	t3 := mustRenew(t, d, t2.ID, t0.Add(2*time.Minute))

	// 仍在宽限窗口内，但 t1 的后继 t2 已不是当前令牌（当前是 t3）：盗用。
	_, err := d.Renew(t1.ID, t0.Add(3*time.Minute))
	if !errors.Is(err, ErrReuseDetected) {
		t.Fatalf("reuse after successor moved: err=%v, want ErrReuseDetected", err)
	}
	// 最新令牌 t3 随之立即不可用。
	if _, err := d.Renew(t3.ID, t0.Add(4*time.Minute)); !errors.Is(err, ErrFamilyRevoked) {
		t.Fatalf("latest token should be revoked, got %v", err)
	}
}

func TestAbsoluteLifetimeTruncatesExpiry(t *testing.T) {
	var log bytes.Buffer
	d, err := New(Config{L: 20 * time.Hour, D: 24 * time.Hour, G: time.Hour, Log: &log})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// 首令牌按完整寿命 L 到期；在距绝对寿命只剩 5h 时续期，新令牌到期被截断。
	t1 := mustLogin(t, d, t0)
	late := t0.Add(19 * time.Hour)
	t1 = mustRenew(t, d, t1.ID, late)
	deadline := t0.Add(24 * time.Hour)
	if !t1.Expires.Equal(deadline) {
		t.Fatalf("truncated expiry %s, want %s", t1.Expires, deadline)
	}

	// 恰在绝对寿命时刻续期被拒；前一纳秒仍可续期，新令牌同样截断。
	edge := mustRenew(t, d, t1.ID, deadline.Add(-time.Nanosecond))
	if !edge.Expires.Equal(deadline) {
		t.Fatalf("renewed expiry %s, want %s", edge.Expires, deadline)
	}
	if _, err := d.Renew(edge.ID, deadline); !errors.Is(err, ErrFamilyAbsoluteExpired) {
		t.Fatalf("at absolute deadline: err=%v, want ErrFamilyAbsoluteExpired", err)
	}
	if !strings.Contains(log.String(), "absolute lifetime reached") {
		t.Fatalf("log missing absolute-lifetime rationale:\n%s", log.String())
	}
}

func TestRotatedTokenExpiryChecksCurrentOnly(t *testing.T) {
	// G=0：任何对已换出令牌的出示都不构成重试。
	d, err := New(Config{L: testL, D: testD, G: 0})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t1 := mustLogin(t, d, t0)
	t2 := mustRenew(t, d, t1.ID, t0.Add(time.Minute))
	// 当前令牌 t2 到期后出示早已换出的 t1：已换出令牌不检查自身到期，
	// 但家族当前令牌到期优先于盗用，应报 ErrTokenExpired 且不吊销家族。
	at := t2.Expires.Add(time.Minute)
	if _, err := d.Renew(t1.ID, at); !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("expired-current precedence: err=%v, want ErrTokenExpired", err)
	}
	if _, err := d.Renew(t2.ID, at); !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("family must remain unrevoked after expired rejection, got %v", err)
	}
	// 在到期之前一刻仍可正常续期（证明此前的拒绝没有改变状态）。
	next := mustRenew(t, d, t2.ID, t2.Expires.Add(-time.Nanosecond))
	if next.ID != "t3" {
		t.Fatalf("got %s, want t3", next.ID)
	}
}

func TestErrorPriority(t *testing.T) {
	// 吊销后令牌同时满足多个拒绝条件，必须只报优先级最高者。
	d, _ := newTestDetector(t)
	t1 := mustLogin(t, d, t0)
	t2 := mustRenew(t, d, t1.ID, t0.Add(time.Minute))
	if err := d.Logout(t2.ID, t0.Add(2*time.Minute)); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	// 未知令牌即使家族已吊销也优先报未知。
	if _, err := d.Renew("nope", t0.Add(48*time.Hour)); !errors.Is(err, ErrTokenUnknown) {
		t.Fatalf("unknown priority: %v", err)
	}
	// 已吊销 + 已超绝对寿命 + 出示旧令牌：报家族已吊销。
	if _, err := d.Renew(t1.ID, t0.Add(48*time.Hour)); !errors.Is(err, ErrFamilyRevoked) {
		t.Fatalf("revoked priority: %v", err)
	}

	// 未吊销但超绝对寿命：绝对寿命优先于到期与盗用，且拒绝不改状态。
	d2, _ := newTestDetector(t)
	u1 := mustLogin(t, d2, t0)
	u2 := mustRenew(t, d2, u1.ID, t0.Add(time.Minute))
	if _, err := d2.Renew(u1.ID, t0.Add(testD)); !errors.Is(err, ErrFamilyAbsoluteExpired) {
		t.Fatalf("absolute lifetime priority over theft: %v", err)
	}
	// 若上面错误地吊销了家族，这里会得到 ErrFamilyRevoked。
	if _, err := d2.Renew(u2.ID, t0.Add(testD)); !errors.Is(err, ErrFamilyAbsoluteExpired) {
		t.Fatalf("rejected call changed state: %v", err)
	}

	// 当前令牌到期优先于其后一切；到期拒绝不吊销家族。
	d3, _ := newTestDetector(t)
	v1 := mustLogin(t, d3, t0)
	if _, err := d3.Renew(v1.ID, v1.Expires); !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("at exact expiry: %v", err)
	}
	if _, err := d3.Renew(v1.ID, v1.Expires.Add(time.Minute)); !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("expired rejection must not revoke family: %v", err)
	}
}

func TestLogout(t *testing.T) {
	d, log := newTestDetector(t)
	t1 := mustLogin(t, d, t0)
	t2 := mustRenew(t, d, t1.ID, t0.Add(time.Minute))

	// 用旧令牌也能定位并吊销整个家族。
	if err := d.Logout(t1.ID, t0.Add(2*time.Minute)); err != nil {
		t.Fatalf("Logout with rotated token: %v", err)
	}
	// 家族内所有令牌立即不可用。
	if _, err := d.Renew(t2.ID, t0.Add(3*time.Minute)); !errors.Is(err, ErrFamilyRevoked) {
		t.Fatalf("current token after logout: %v", err)
	}
	// 重复登出成功且无变化。
	if err := d.Logout(t2.ID, t0.Add(4*time.Minute)); err != nil {
		t.Fatalf("repeat logout: %v", err)
	}
	// 未知令牌登出报错且不改变任何状态。
	if err := d.Logout("ghost", t0); !errors.Is(err, ErrTokenUnknown) {
		t.Fatalf("logout unknown: %v", err)
	}
	if !strings.Contains(log.String(), "already revoked, no change") {
		t.Fatalf("log missing idempotent logout rationale:\n%s", log.String())
	}
}

func TestConcurrentRenewSameToken(t *testing.T) {
	d, _ := newTestDetector(t)
	t1 := mustLogin(t, d, t0)

	const n = 64
	var wg sync.WaitGroup
	results := make([]string, n)
	errs := make([]error, n)
	start := make(chan struct{})
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			<-start
			tok, err := d.Renew(t1.ID, t0.Add(time.Minute))
			if err == nil {
				results[i] = tok.ID
			}
			errs[i] = err
		}(i)
	}
	close(start)
	wg.Wait()

	successor := ""
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("concurrent renew %d: %v", i, errs[i])
		}
		// 无论换出还是宽限内重试，返回的必须是同一个后继令牌。
		if successor == "" {
			successor = results[i]
		} else if results[i] != successor {
			t.Fatalf("call %d returned %s, want %s", i, results[i], successor)
		}
	}
	if successor == "" {
		t.Fatalf("exactly one rotation expected, got none")
	}
	// 恰有一个调用真正换出：全局只签发了一个新令牌（序号 2）。
	d.mu.Lock()
	issued := d.seq
	d.mu.Unlock()
	if issued != 2 {
		t.Fatalf("exactly one new token should be issued, got seq=%d", issued)
	}
	if successor != "t2" {
		t.Fatalf("successor = %s, want t2", successor)
	}

	// 家族当前恰有一个可用令牌：后续续期只产生一个新 id（t3）。
	next := mustRenew(t, d, successor, t0.Add(2*time.Minute))
	if next.ID != "t3" {
		t.Fatalf("family should have exactly one current token, next=%s want t3", next.ID)
	}
}

func TestReplayDeterminism(t *testing.T) {
	run := func() string {
		var log bytes.Buffer
		d, err := New(Config{L: testL, D: testD, G: testG, Log: &log})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		t1 := mustLogin(t, d, t0)
		t2 := mustRenew(t, d, t1.ID, t0.Add(time.Minute))
		r, err := d.Renew(t1.ID, t0.Add(2*time.Minute))
		if err != nil {
			t.Fatalf("retry: %v", err)
		}
		_, theftErr := d.Renew(t1.ID, t0.Add(time.Hour))
		_, afterErr := d.Renew(t2.ID, t0.Add(time.Hour))
		logoutErr := d.Logout(t1.ID, t0.Add(2*time.Hour))
		return strings.Join([]string{
			t1.ID, t2.ID, r.ID,
			fmtErr(theftErr), fmtErr(afterErr), fmtErr(logoutErr),
			log.String(),
		}, "\n")
	}
	first := run()
	second := run()
	if first != second {
		t.Fatalf("replay mismatch:\n--- first ---\n%s\n--- second ---\n%s", first, second)
	}
}

func fmtErr(err error) string {
	if err == nil {
		return "<nil>"
	}
	return err.Error()
}
