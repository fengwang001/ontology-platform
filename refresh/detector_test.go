package refresh

import (
	"bytes"
	"errors"
	"io"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	testL = 10 * time.Minute
	testD = 30 * time.Minute
	testG = time.Minute
)

func newTestDetector(t *testing.T) (*Detector, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	d, err := New(testL, testD, testG, WithLogger(&buf))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return d, &buf
}

func t0() time.Time { return time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC) }

// 登录签发首个令牌，标识按全局签发顺序 t1、t2……
func TestLoginIssuesGlobalSequence(t *testing.T) {
	d, _ := newTestDetector(t)
	now := t0()

	t1, e1 := d.Login(now)
	t2, e2 := d.Login(now)
	if t1 != "t1" || t2 != "t2" {
		t.Fatalf("got %q,%q want t1,t2", t1, t2)
	}
	if !e1.Equal(now.Add(testL)) || !e2.Equal(now.Add(testL)) {
		t.Fatalf("got %v,%v want %v", e1, e2, now.Add(testL))
	}
}

// 绝对寿命截断：新令牌到期取 now+L 与 创建时刻+D 的较小者。
func TestAbsoluteLifetimeTruncates(t *testing.T) {
	d, _ := newTestDetector(t)
	now := t0()

	// 首令牌到期恒为创建时刻+L（D>=L 时绝对寿命不会截断首令牌）。
	t1, exp := d.Login(now)
	if !exp.Equal(now.Add(testL)) {
		t.Fatalf("first token expiry=%v want %v", exp, now.Add(testL))
	}

	// 每 5 分钟续期，使令牌链在临近绝对寿命时仍然存活。
	var res Result
	var err error
	res.Token = t1
	nextID := 1
	for m := 5; m <= 25; m += 5 {
		res, err = d.Renew(Entry{Token: res.Token, At: now.Add(time.Duration(m) * time.Minute)})
		if err != nil {
			t.Fatalf("renew at +%dm: %v", m, err)
		}
		nextID++
		if res.Token != Token("t"+strconv.Itoa(nextID)) {
			t.Fatalf("global sequence broken: got %q want t%d", res.Token, nextID)
		}
	}
	want := now.Add(testD)
	if !res.Issued || !res.ExpiresAt.Equal(want) {
		t.Fatalf("renewed expiry=%v want %v", res.ExpiresAt, want)
	}

	// 再次续期，仍截断在绝对寿命时刻。
	res2, err := d.Renew(Entry{Token: res.Token, At: now.Add(28 * time.Minute)})
	if err != nil {
		t.Fatalf("second renew: %v", err)
	}
	if !res2.ExpiresAt.Equal(want) {
		t.Fatalf("second renewal expiry=%v want %v", res2.ExpiresAt, want)
	}

	// 到达绝对寿命时刻，家族失效，令牌自身到期不再被检查/截断。
	if _, err := d.Renew(Entry{Token: res2.Token, At: want}); !errors.Is(err, ErrFamilyExpired) {
		t.Fatalf("at absolute boundary got %v want ErrFamilyExpired", err)
	}
}

// 续期换发新令牌，旧令牌变为已换出并记下换出时刻与后继。
func TestRenewRotatesAndRecordsSuccessor(t *testing.T) {
	d, _ := newTestDetector(t)
	now := t0()
	t1, _ := d.Login(now)

	at := now.Add(2 * time.Minute)
	res, err := d.Renew(Entry{Token: t1, At: at})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Issued || res.Token != "t2" || !res.ExpiresAt.Equal(at.Add(testL)) {
		t.Fatalf("renew result=%+v", res)
	}
	old := d.tokens[t1]
	if !old.rotated || !old.rotatedAt.Equal(at) || old.successor != "t2" {
		t.Fatalf("old token state=%+v", old)
	}
	if d.families[old.family].current != "t2" {
		t.Fatalf("family current not advanced")
	}
}

// 宽限内重试：不签发新令牌、原样返回同一后继、家族不变。
func TestRetryWithinGraceReturnsSameSuccessor(t *testing.T) {
	d, _ := newTestDetector(t)
	now := t0()
	t1, _ := d.Login(now)
	at := now.Add(2 * time.Minute)
	r1, err := d.Renew(Entry{Token: t1, At: at})
	if err != nil {
		t.Fatal(err)
	}

	retry, err := d.Renew(Entry{Token: t1, At: at.Add(testG - time.Nanosecond)})
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if retry.Issued || retry.Token != r1.Token {
		t.Fatalf("retry=%+v want same successor %q, no issuance", retry, r1.Token)
	}
	if !retry.ExpiresAt.Equal(r1.ExpiresAt) {
		t.Fatalf("retry expiry=%v want %v", retry.ExpiresAt, r1.ExpiresAt)
	}
	if d.nextID != 2 {
		t.Fatalf("retry issued new token, nextID=%d", d.nextID)
	}

	// 再次重试（同窗口）仍返回同一后继，换出时刻不被刷新。
	retry2, err := d.Renew(Entry{Token: t1, At: at.Add(testG / 2)})
	if err != nil || retry2.Token != r1.Token || retry2.Issued {
		t.Fatalf("second retry=%+v err=%v", retry2, err)
	}
	if !d.tokens[t1].rotatedAt.Equal(at) {
		t.Fatalf("retry refreshed rotatedAt")
	}
}

// 恰在宽限边界：elapsed == G 不属于重试，判定盗用并吊销家族。
func TestGraceBoundaryIsTheft(t *testing.T) {
	d, _ := newTestDetector(t)
	now := t0()
	t1, _ := d.Login(now)
	r, err := d.Renew(Entry{Token: t1, At: now})
	if err != nil {
		t.Fatal(err)
	}

	// elapsed == G（边界）：盗用。
	_, err = d.Renew(Entry{Token: t1, At: now.Add(testG)})
	if !errors.Is(err, ErrReuseDetected) {
		t.Fatalf("at boundary got %v want ErrReuseDetected", err)
	}
	if !d.families[1].revoked {
		t.Fatalf("family not revoked after boundary reuse")
	}
	// 盗用后整个家族含最新令牌立即不可用。
	_, err = d.Renew(Entry{Token: r.Token, At: now.Add(testG)})
	if !errors.Is(err, ErrFamilyRevoked) {
		t.Fatalf("successor after theft got %v want ErrFamilyRevoked", err)
	}

	// G == 0 时，同一时刻的第二次出示 elapsed==0==G，按“不足 G”判定为盗用。
	d2, err := New(testL, testD, 0, WithLogger(io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	tA, _ := d2.Login(now)
	if _, err := d2.Renew(Entry{Token: tA, At: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := d2.Renew(Entry{Token: tA, At: now}); !errors.Is(err, ErrReuseDetected) {
		t.Fatalf("G=0 replay at same instant got %v want ErrReuseDetected", err)
	}
}

// 后继已被续期后旧令牌再出示（即便仍在宽限内）触发盗用吊销。
func TestOldTokenAfterSuccessorRotatedIsTheft(t *testing.T) {
	d, _ := newTestDetector(t)
	now := t0()
	t1, _ := d.Login(now)

	r2, err := d.Renew(Entry{Token: t1, At: now})
	if err != nil {
		t.Fatal(err)
	}
	r3, err := d.Renew(Entry{Token: r2.Token, At: now.Add(30 * time.Second)})
	if err != nil {
		t.Fatal(err)
	}

	// t1 距自身换出仅 30s（< G），但后继 t2 已不是当前令牌 -> 盗用。
	_, err = d.Renew(Entry{Token: t1, At: now.Add(30 * time.Second)})
	if !errors.Is(err, ErrReuseDetected) {
		t.Fatalf("got %v want ErrReuseDetected", err)
	}
	if !d.families[1].revoked {
		t.Fatalf("family not revoked")
	}
	// 最新令牌 t3 也立即不可用。
	_, err = d.Renew(Entry{Token: r3.Token, At: now.Add(31 * time.Second)})
	if !errors.Is(err, ErrFamilyRevoked) {
		t.Fatalf("latest token usable after theft: %v", err)
	}
}

// 超过宽限的旧令牌出示判定盗用。
func TestReuseAfterGraceIsTheft(t *testing.T) {
	d, _ := newTestDetector(t)
	now := t0()
	t1, _ := d.Login(now)
	if _, err := d.Renew(Entry{Token: t1, At: now}); err != nil {
		t.Fatal(err)
	}
	_, err := d.Renew(Entry{Token: t1, At: now.Add(testG + time.Nanosecond)})
	if !errors.Is(err, ErrReuseDetected) {
		t.Fatalf("got %v want ErrReuseDetected", err)
	}
}

// 当前令牌到期时刻起失效；已换出令牌不检查自身到期。
func TestExpirySemantics(t *testing.T) {
	now := t0()

	// 恰在到期时刻：失效。
	d, _ := newTestDetector(t)
	t1, exp := d.Login(now)
	if _, err := d.Renew(Entry{Token: t1, At: exp}); !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("at expiry got %v want ErrTokenExpired", err)
	}

	// 到期前一刻：成功。
	d2, _ := newTestDetector(t)
	t1b, expb := d2.Login(now)
	r, err := d2.Renew(Entry{Token: t1b, At: expb.Add(-time.Nanosecond)})
	if err != nil {
		t.Fatalf("just before expiry: %v", err)
	}

	// 已换出令牌在绝对寿命时刻不看自身到期，报家族绝对寿命。
	if _, err := d2.Renew(Entry{Token: t1b, At: now.Add(testD)}); !errors.Is(err, ErrFamilyExpired) {
		t.Fatalf("rotated token at absolute expiry got %v want ErrFamilyExpired", err)
	}
	// 当前后继在绝对寿命时刻报家族绝对寿命（优先级高于自身到期）。
	if _, err := d2.Renew(Entry{Token: r.Token, At: now.Add(testD)}); !errors.Is(err, ErrFamilyExpired) {
		t.Fatalf("successor at absolute expiry got %v want ErrFamilyExpired", err)
	}
	// 被拒绝不得改变任何状态：家族未吊销，仍是同一当前令牌。
	if d2.families[1].revoked || d2.families[1].current != r.Token {
		t.Fatalf("reject changed family state: %+v", d2.families[1])
	}
}

// 错误优先级固定：未知 > 已吊销 > 绝对寿命 > 当前到期 > 盗用。
func TestErrorPriority(t *testing.T) {
	d, _ := newTestDetector(t)
	now := t0()
	t1, _ := d.Login(now)
	t2, err := d.Renew(Entry{Token: t1, At: now})
	if err != nil {
		t.Fatal(err)
	}
	// 盗用吊销家族。
	if _, err := d.Renew(Entry{Token: t1, At: now.Add(testG + time.Second)}); !errors.Is(err, ErrReuseDetected) {
		t.Fatalf("theft: %v", err)
	}

	at := now.Add(testD + time.Hour) // 同时满足吊销/绝对寿命/到期
	cases := []struct {
		name string
		tok  Token
		want error
	}{
		{"unknown", "t999", ErrUnknownToken},
		{"revoked beats expired", t2.Token, ErrFamilyRevoked},
		{"revoked beats reuse", t1, ErrFamilyRevoked},
	}
	for _, c := range cases {
		if _, err := d.Renew(Entry{Token: c.tok, At: at}); !errors.Is(err, c.want) {
			t.Errorf("%s: got %v want %v", c.name, err, c.want)
		}
	}
}

// 被拒绝的操作不得改变任何状态。
func TestRejectionDoesNotMutateState(t *testing.T) {
	d, _ := newTestDetector(t)
	now := t0()
	t1, _ := d.Login(now)

	snapshot := func() string {
		f := d.families[1]
		info := d.tokens[t1]
		return strings.Join([]string{
			string(f.current),
			strconv.FormatBool(f.revoked),
			strconv.FormatBool(info.rotated),
			string(info.successor),
			strconv.Itoa(d.nextID),
		}, "|")
	}

	before := snapshot()
	for _, e := range []Entry{
		{Token: "unknown", At: now},
		{Token: t1, At: now.Add(testL)},             // 当前令牌到期
		{Token: t1, At: now.Add(testD)},             // 家族绝对寿命
		{Token: t1, At: now.Add(testD + time.Hour)}, // 仍先命中绝对寿命
	} {
		if _, err := d.Renew(e); err == nil {
			t.Fatalf("entry %+v should be rejected", e)
		}
		if got := snapshot(); got != before {
			t.Fatalf("state changed after rejected %+v: before=%s after=%s", e, before, got)
		}
	}
}

// 主动登出按家族吊销；重复登出成功且无变化；未知令牌报错。
func TestLogoutIdempotent(t *testing.T) {
	d, _ := newTestDetector(t)
	now := t0()
	t1, _ := d.Login(now)
	r, err := d.Renew(Entry{Token: t1, At: now})
	if err != nil {
		t.Fatal(err)
	}

	// 用已换出的旧令牌登出也吊销整个家族。
	if err := d.Logout(t1); err != nil {
		t.Fatalf("logout via old token: %v", err)
	}
	if _, err := d.Renew(Entry{Token: r.Token, At: now}); !errors.Is(err, ErrFamilyRevoked) {
		t.Fatalf("after logout got %v want ErrFamilyRevoked", err)
	}
	// 重复登出（用最新令牌）视为成功且无变化。
	if err := d.Logout(r.Token); err != nil {
		t.Fatalf("repeat logout: %v", err)
	}
	// 对已吊销家族再登出旧令牌同样成功。
	if err := d.Logout(t1); err != nil {
		t.Fatalf("third logout: %v", err)
	}
	// 未知令牌无法定位家族，按未知处理。
	if err := d.Logout("nope"); !errors.Is(err, ErrUnknownToken) {
		t.Fatalf("logout unknown got %v want ErrUnknownToken", err)
	}
}

// 创建检测器时的配置校验，给出可区分原因。
func TestConfigValidation(t *testing.T) {
	cases := []struct {
		name    string
		L, D, G time.Duration
		want    error
	}{
		{"L zero", 0, testD, 0, ErrNonPositiveL},
		{"L negative", -time.Second, testD, 0, ErrNonPositiveL},
		{"D zero", testL, 0, 0, ErrNonPositiveD},
		{"D negative", testL, -time.Second, 0, ErrNonPositiveD},
		{"G negative", testL, testD, -time.Nanosecond, ErrNegativeG},
		{"G equals L", testL, testD, testL, ErrGraceTooLarge},
		{"G larger than L", testL, testD, testL + time.Second, ErrGraceTooLarge},
		{"D smaller than L", testL, testL - time.Nanosecond, 0, ErrDTooSmall},
	}
	for _, c := range cases {
		if _, err := New(c.L, c.D, c.G); !errors.Is(err, c.want) {
			t.Errorf("%s: got %v want %v", c.name, err, c.want)
		}
	}
	// 边界合法值：G == L-1ns、D == L 均接受。
	if _, err := New(testL, testL, testL-time.Nanosecond); err != nil {
		t.Fatalf("boundary-valid config rejected: %v", err)
	}
}

func errString(err error) string {
	if err == nil {
		return "nil"
	}
	return err.Error()
}

// 任意交错下家族至多一个可用令牌。
func TestFamilyAtMostOneUsableToken(t *testing.T) {
	d, _ := newTestDetector(t)
	now := t0()
	root, _ := d.Login(now)
	other, _ := d.Login(now)

	var wg sync.WaitGroup
	current := root
	var chainMu sync.Mutex
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for attempt := 0; attempt < 20; attempt++ {
				chainMu.Lock()
				tok := current
				at := now.Add(time.Duration(i*20+attempt) * time.Second)
				chainMu.Unlock()
				res, err := d.Renew(Entry{Token: tok, At: at})
				if errors.Is(err, ErrReuseDetected) {
					return // 链竞争触发盗用，家族已吊销
				}
				if err != nil {
					continue
				}
				if res.Issued {
					chainMu.Lock()
					if current == tok {
						current = res.Token
					}
					chainMu.Unlock()
				}
			}
		}(i)
	}
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = d.Logout(other)
		}()
	}
	wg.Wait()

	for _, f := range d.families {
		if f.revoked {
			continue
		}
		usable := 0
		for tok, info := range d.tokens {
			if info.family != f.id || info.rotated {
				continue
			}
			if tok == f.current {
				usable++
			}
		}
		if usable != 1 {
			t.Fatalf("family %d has %d usable tokens, want exactly 1", f.id, usable)
		}
	}
}

// 并发出示同一当前令牌：恰有一个换出成功，其余得到同一个后继。
func TestConcurrentSameTokenExactlyOneRotation(t *testing.T) {
	// 同刻并发：G=0 时其余出示会落在盗用边界，故取正宽限。
	d, err := New(time.Hour, 24*time.Hour, time.Minute, WithLogger(io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	now := t0()
	t1, _ := d.Login(now)

	const n = 32
	var wg sync.WaitGroup
	results := make([]Result, n)
	errs := make([]error, n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			results[i], errs[i] = d.Renew(Entry{Token: t1, At: now})
		}(i)
	}
	close(start)
	wg.Wait()

	issued := 0
	for i := range results {
		if errs[i] != nil {
			t.Fatalf("goroutine %d: %v", i, errs[i])
		}
		if results[i].Token != "t2" {
			t.Fatalf("goroutine %d token=%q want t2", i, results[i].Token)
		}
		if results[i].Issued {
			issued++
		}
	}
	if issued != 1 {
		t.Fatalf("issued count=%d want exactly 1", issued)
	}
	if d.nextID != 2 || d.families[1].current != "t2" {
		t.Fatalf("state after concurrent renew: nextID=%d current=%q", d.nextID, d.families[1].current)
	}
}

type recordedOp struct {
	kind string
	tok  Token
	at   time.Time
}

func buildScript(t *testing.T) ([]recordedOp, []string) {
	d, _ := newTestDetector(t)
	var ops []recordedOp
	var out []string

	a, _ := d.Login(t0())
	ops = append(ops, recordedOp{"login", a, t0()})
	out = append(out, "login:"+string(a))

	r, err := d.Renew(Entry{Token: a, At: t0().Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	ops = append(ops, recordedOp{"renew", a, t0().Add(time.Minute)})
	out = append(out, "renew:"+string(r.Token))

	retry, err := d.Renew(Entry{Token: a, At: t0().Add(90 * time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	ops = append(ops, recordedOp{"renew", a, t0().Add(90 * time.Second)})
	out = append(out, "renew:"+string(retry.Token))

	if err := d.Logout(a); err != nil {
		t.Fatal(err)
	}
	ops = append(ops, recordedOp{"logout", a, time.Time{}})
	out = append(out, "logout:"+errString(nil))

	_, err = d.Renew(Entry{Token: r.Token, At: t0().Add(2 * time.Minute)})
	ops = append(ops, recordedOp{"renew", r.Token, t0().Add(2 * time.Minute)})
	out = append(out, "renew-err:"+errString(err))

	return ops, out
}

func replayScript(t *testing.T, ops []recordedOp) []string {
	d, _ := newTestDetector(t)
	var out []string
	for _, o := range ops {
		switch o.kind {
		case "login":
			tok, _ := d.Login(o.at)
			out = append(out, "login:"+string(tok))
		case "renew":
			res, err := d.Renew(Entry{Token: o.tok, At: o.at})
			if err != nil {
				out = append(out, "renew-err:"+errString(err))
			} else {
				out = append(out, "renew:"+string(res.Token))
			}
		case "logout":
			out = append(out, "logout:"+errString(d.Logout(o.tok)))
		}
	}
	return out
}

// 相同操作序列重放：完全相同的令牌标识与结果。
func TestDeterministicReplay(t *testing.T) {
	ops, first := buildScript(t)
	for i := 0; i < 3; i++ {
		got := replayScript(t, ops)
		if strings.Join(got, ",") != strings.Join(first, ",") {
			t.Fatalf("replay %d mismatch:\n got=%v\nwant=%v", i, got, first)
		}
	}
}

// 日志打印输入、输出与判定依据。
func TestLogsContainInputOutputAndRationale(t *testing.T) {
	var buf bytes.Buffer
	d, err := New(testL, testD, testG, WithLogger(&buf))
	if err != nil {
		t.Fatal(err)
	}
	now := t0()
	t1, _ := d.Login(now)

	r, err := d.Renew(Entry{Token: t1, At: now})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Renew(Entry{Token: t1, At: now.Add(30 * time.Second)}); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if _, err := d.Renew(Entry{Token: t1, At: now.Add(testG + time.Second)}); !errors.Is(err, ErrReuseDetected) {
		t.Fatalf("theft: %v", err)
	}
	if err := d.Logout(r.Token); err != nil {
		t.Fatal(err)
	}

	log := buf.String()
	for _, want := range []string{
		`op=login`,
		`op=renew decision=rotated`,
		`token=t1`,          // 输入
		`successor=t2`,      // 输出
		`decision=retry`,    // 判定依据
		`elapsed=30s`,       // 重试依据
		`decision=rejected`, // 盗用拒绝
		`reason="refresh: token reuse detected`,
		`op=logout decision=revoked`,
	} {
		if !strings.Contains(log, want) {
			t.Errorf("log missing %q\nfull log:\n%s", want, log)
		}
	}
}
