package ontology

import (
	"bytes"
	"fmt"
	"log"
	"strings"
	"sync"
	"testing"
	"time"
)

// testCode 是确定性口令函数：(user, step) -> 口令。不同步/不同用户必不同。
func testCode(user string, step int64) string {
	return fmt.Sprintf("%s@%d", user, step)
}

// fixedClock 返回固定时刻（秒）。
func fixedClock(unixSeconds int64) func() time.Time {
	return func() time.Time { return time.Unix(unixSeconds, 0) }
}

func newTestVerifier(t *testing.T, unixSeconds int64) (*Verifier, *bytes.Buffer) {
	t.Helper()
	v := NewVerifier(testCode)
	v.SetClock(fixedClock(unixSeconds))
	var buf bytes.Buffer
	v.SetLogger(log.New(&buf, "", 0))
	return v, &buf
}

// 设备时钟快一步：用 c+1 的口令成功后，当前步 c 的合法口令必须被拒（已使用水位规则）。
func TestFastClockOneStepRejectsCurrentCode(t *testing.T) {
	v, _ := newTestVerifier(t, 100) // P=10 -> c=10
	if err := v.Register("alice", 10, 1, 1); err != nil {
		t.Fatalf("register: %v", err)
	}

	first := v.Verify("alice", testCode("alice", 11)) // 设备快一步
	if !first.OK || first.Reason != OutcomeOK || first.MatchedStep != 11 {
		t.Fatalf("fast-step verify = %+v, want ok matched at 11", first)
	}
	if first.Drift != 1 || first.Watermark != 11 {
		t.Fatalf("after fast-step: d=%d u=%d, want d=1 u=11", first.Drift, first.Watermark)
	}

	// 当前步 c=10 的口令本身合法，但 10 <= u=11，永远不能再通过。
	second := v.Verify("alice", testCode("alice", 10))
	if second.OK || second.Reason != OutcomeCodeUsed {
		t.Fatalf("current-step verify = %+v, want code-used", second)
	}

	// 再次提交快一步口令同样被拒（重放）。
	replay := v.Verify("alice", testCode("alice", 11))
	if replay.OK || replay.Reason != OutcomeCodeUsed {
		t.Fatalf("replay verify = %+v, want code-used", replay)
	}
}

// 漂移校正使窗口中心移动：快两步成功后 d=2，之后窗口以 c+2 为中心。
func TestDriftCorrectionShiftsWindowCenter(t *testing.T) {
	v, _ := newTestVerifier(t, 100) // c=10
	if err := v.Register("bob", 10, 2, 2); err != nil {
		t.Fatalf("register: %v", err)
	}

	first := v.Verify("bob", testCode("bob", 12)) // 偏移 +2，恰好在 D 内
	if !first.OK || first.Drift != 2 || first.Watermark != 12 {
		t.Fatalf("first verify = %+v, want ok d=2 u=12", first)
	}

	// 时钟前进 10 秒，c=11；校正后容忍窗口中心为 c+d=13。
	v.SetClock(fixedClock(110))
	res := v.Verify("bob", testCode("bob", 13))
	if !res.OK || res.CurrentStep != 11 || res.MatchedStep != 13 {
		t.Fatalf("shifted verify = %+v, want ok at step 13 with c=11", res)
	}
	if res.WindowLo != 11 || res.WindowHi != 13 {
		t.Fatalf("window = [%d,%d], want [11,13]", res.WindowLo, res.WindowHi)
	}

	// 校正后旧中心 c=10 的口令落在窗口交集 [11,13] 之外，判错误而非已使用。
	old := v.Verify("bob", testCode("bob", 10))
	if old.OK || old.Reason != OutcomeCodeWrong {
		t.Fatalf("out-of-window verify = %+v, want code-wrong", old)
	}
}

// 恰在漂移上限内外：|偏移|=D 可通过（且在 w 窗口内），|偏移|=D+1 不可通过。
func TestDriftBoundInsideAndOutside(t *testing.T) {
	// 内部：偏移 +D，注册 w=D=2，步 12 在容忍窗口内 -> 通过。
	vIn, _ := newTestVerifier(t, 100) // c=10
	if err := vIn.Register("carol", 10, 2, 2); err != nil {
		t.Fatalf("register: %v", err)
	}
	inside := vIn.Verify("carol", testCode("carol", 12))
	if !inside.OK || inside.MatchedStep != 12 {
		t.Fatalf("inside-bound verify = %+v, want ok at 12", inside)
	}

	// 外部：偏移 D+1。交集硬界为 c+D=12，步 13 不在窗口内 -> 错误。
	vOut, _ := newTestVerifier(t, 100) // c=10
	if err := vOut.Register("dave", 10, 2, 2); err != nil {
		t.Fatalf("register: %v", err)
	}
	outside := vOut.Verify("dave", testCode("dave", 13))
	if outside.OK || outside.Reason != OutcomeCodeWrong {
		t.Fatalf("outside-bound verify = %+v, want code-wrong", outside)
	}

	// 负方向对称：偏移 -D 通过，-D-1 拒绝。
	vNeg, _ := newTestVerifier(t, 100)
	if err := vNeg.Register("erin", 10, 2, 2); err != nil {
		t.Fatalf("register: %v", err)
	}
	if neg := vNeg.Verify("erin", testCode("erin", 8)); !neg.OK || neg.MatchedStep != 8 {
		t.Fatalf("negative inside-bound verify = %+v, want ok at 8", neg)
	}
	vNeg2, _ := newTestVerifier(t, 100)
	if err := vNeg2.Register("frank", 10, 2, 2); err != nil {
		t.Fatalf("register: %v", err)
	}
	if negOut := vNeg2.Verify("frank", testCode("frank", 7)); negOut.OK || negOut.Reason != OutcomeCodeWrong {
		t.Fatalf("negative outside-bound verify = %+v, want code-wrong", negOut)
	}
}

// 已使用与错误必须可区分；同时验证失败不改变 d 与 u。
func TestUsedVsWrongDistinguishable(t *testing.T) {
	v, _ := newTestVerifier(t, 100) // c=10
	if err := v.Register("grace", 10, 1, 1); err != nil {
		t.Fatalf("register: %v", err)
	}
	if ok := v.Verify("grace", testCode("grace", 10)); !ok.OK {
		t.Fatalf("first verify failed: %+v", ok)
	}

	used := v.Verify("grace", testCode("grace", 10)) // 窗口内匹配但 <= u
	if used.OK || used.Reason != OutcomeCodeUsed {
		t.Fatalf("used = %+v, want code-used", used)
	}

	wrong := v.Verify("grace", "totally-bogus") // 窗口内无任何匹配
	if wrong.OK || wrong.Reason != OutcomeCodeWrong {
		t.Fatalf("wrong = %+v, want code-wrong", wrong)
	}

	if used.Drift != 0 || used.Watermark != 10 || wrong.Drift != 0 || wrong.Watermark != 10 {
		t.Fatalf("state changed on failure: used d=%d u=%d; wrong d=%d u=%d",
			used.Drift, used.Watermark, wrong.Drift, wrong.Watermark)
	}
}

// 同一用户同一口令并发提交，恰有一个通过，其余全部 code-used，且 u 单调不减。
func TestConcurrentSameCodeExactlyOnePass(t *testing.T) {
	v, _ := newTestVerifier(t, 100) // c=10
	if err := v.Register("heidi", 10, 1, 1); err != nil {
		t.Fatalf("register: %v", err)
	}
	code := testCode("heidi", 10)

	const n = 64
	start := make(chan struct{})
	var wg sync.WaitGroup
	results := make([]VerifyResult, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			<-start
			results[i] = v.Verify("heidi", code)
		}(i)
	}
	close(start)
	wg.Wait()

	pass, used, other := 0, 0, 0
	for _, r := range results {
		switch {
		case r.OK && r.Reason == OutcomeOK:
			pass++
		case !r.OK && r.Reason == OutcomeCodeUsed:
			used++
		default:
			other++
		}
	}
	if pass != 1 || used != n-1 || other != 0 {
		t.Fatalf("pass=%d used=%d other=%d, want pass=1 used=%d", pass, used, other, n-1)
	}

	final := v.Verify("heidi", code)
	if final.OK || final.Reason != OutcomeCodeUsed || final.Watermark != 10 {
		t.Fatalf("post-concurrency state = %+v, want used with u=10", final)
	}
}

// 并发交错下 u 单调不减：所有返回结果的 Watermark 永不回退，且每个步只被消费一次。
func TestConcurrentWatermarkMonotonic(t *testing.T) {
	v, _ := newTestVerifier(t, 100000) // c=10000, w=D=3
	if err := v.Register("ivan", 10, 3, 3); err != nil {
		t.Fatalf("register: %v", err)
	}

	const workers = 16
	start := make(chan struct{})
	var wg sync.WaitGroup
	var passMu sync.Mutex
	passCount := map[int64]int{}
	wg.Add(workers)
	for g := 0; g < workers; g++ {
		go func(id int) {
			defer wg.Done()
			<-start
			// 各 worker 以不同顺序遍历窗口内 7 个步，制造交错。
			order := []int64{10000, 10003, 9998, 10001, 9997, 10002, 9999}
			if id%2 == 1 {
				for i, j := 0, len(order)-1; i < j; i, j = i+1, j-1 {
					order[i], order[j] = order[j], order[i]
				}
			}
			var prev int64 = -1
			for _, step := range order {
				r := v.Verify("ivan", testCode("ivan", step))
				if r.Watermark < prev {
					t.Errorf("watermark went backwards: %d -> %d", prev, r.Watermark)
				}
				prev = r.Watermark
				if r.OK {
					passMu.Lock()
					passCount[r.MatchedStep]++
					passMu.Unlock()
				}
			}
		}(g)
	}
	close(start)
	wg.Wait()

	// 水位语义下，高序号步先成功会永久屏蔽更早步，因此不保证每步都被消费；
	// 但被消费的步必须恰好消费一次（同口令并发也不会重复）。
	var largest int64 = -1
	for step := int64(9997); step <= 10003; step++ {
		if got := passCount[step]; got > 1 {
			t.Fatalf("step %d consumed %d times during storm, want at most once", step, got)
		} else if got == 1 && step > largest {
			largest = step
		}
	}
	totalPass := 0
	for _, n := range passCount {
		totalPass += n
	}
	if totalPass == 0 {
		t.Fatal("no verify succeeded during concurrent storm")
	}

	// 水位 u 等于已消费步的最大值；对 <= largest 的步重放必为 code-used。
	r := v.Verify("ivan", testCode("ivan", largest))
	if r.OK || r.Reason != OutcomeCodeUsed || r.Watermark != largest {
		t.Fatalf("replay largest step %d = %+v, want code-used with u=%d", largest, r, largest)
	}
}

// 不同用户互不影响：各自的 d/u 独立，口令按用户分别计算。
func TestDifferentUsersIsolated(t *testing.T) {
	v, _ := newTestVerifier(t, 100)
	if err := v.Register("judy", 10, 1, 1); err != nil {
		t.Fatalf("register judy: %v", err)
	}
	if err := v.Register("kate", 10, 1, 1); err != nil {
		t.Fatalf("register kate: %v", err)
	}

	if r := v.Verify("judy", testCode("judy", 11)); !r.OK {
		t.Fatalf("judy fast verify: %+v", r)
	}
	// kate 不受 judy 快一步影响：kate 的 c+1 口令仍可首次使用。
	r := v.Verify("kate", testCode("kate", 11))
	if !r.OK || r.Drift != 1 {
		t.Fatalf("kate verify = %+v, want independent ok d=1", r)
	}
	// 属于另一用户的口令不可能误匹配。
	if cross := v.Verify("kate", testCode("judy", 10)); cross.OK || cross.Reason != OutcomeCodeWrong {
		t.Fatalf("cross-user verify = %+v, want wrong", cross)
	}
}

// 原因报告顺序：未注册 → 空口令 → 已使用 → 错误，只报第一个。
func TestReasonOrdering(t *testing.T) {
	v, _ := newTestVerifier(t, 100)

	if r := v.Verify("ghost", ""); r.OK || r.Reason != OutcomeUserNotFound {
		t.Fatalf("unregistered empty = %+v, want user-not-found", r)
	}
	if r := v.Verify("ghost", "anything"); r.OK || r.Reason != OutcomeUserNotFound {
		t.Fatalf("unregistered = %+v, want user-not-found", r)
	}

	if err := v.Register("leo", 10, 1, 1); err != nil {
		t.Fatalf("register: %v", err)
	}
	if r := v.Verify("leo", ""); r.OK || r.Reason != OutcomeEmptyCode {
		t.Fatalf("empty = %+v, want empty-code", r)
	}

	if r := v.Verify("leo", testCode("leo", 10)); !r.OK {
		t.Fatalf("first use = %+v, want ok", r)
	}
	if r := v.Verify("leo", testCode("leo", 10)); r.OK || r.Reason != OutcomeCodeUsed {
		t.Fatalf("reuse = %+v, want code-used", r)
	}
	if r := v.Verify("leo", ""); r.OK || r.Reason != OutcomeEmptyCode {
		t.Fatalf("empty after use = %+v, want empty-code (ordering)", r)
	}
	if r := v.Verify("leo", "nope"); r.OK || r.Reason != OutcomeCodeWrong {
		t.Fatalf("bogus = %+v, want code-wrong", r)
	}
}

// 注册校验：已存在、P 非正、w 为负、D<w 均整体拒绝并给出可区分原因，
// 且拒绝不改变状态（修正参数后可正常注册）。
func TestRegisterRejections(t *testing.T) {
	v, _ := newTestVerifier(t, 100)

	if err := v.Register("mia", 10, 1, 2); err != nil {
		t.Fatalf("initial register: %v", err)
	}
	cases := []struct {
		name string
		user string
		p    int64
		w    int64
		d    int64
		want error
	}{
		{"exists", "mia", 10, 1, 2, ErrUserExists},
		{"p-zero", "new-p0", 0, 1, 2, ErrInvalidPeriod},
		{"p-negative", "new-pn", -3, 1, 2, ErrInvalidPeriod},
		{"w-negative", "new-wn", 10, -1, 2, ErrNegativeWindow},
		{"d-below-w", "new-dw", 10, 2, 1, ErrDriftBelowWindow},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := v.Register(tc.user, tc.p, tc.w, tc.d)
			if err != tc.want {
				t.Fatalf("Register(%d,%d,%d) err=%v, want %v", tc.p, tc.w, tc.d, err, tc.want)
			}
		})
	}

	// 被拒绝的注册不得改变原用户 d/u：原参数 (10,1,2) 下 c+1 口令应正常工作。
	r := v.Verify("mia", testCode("mia", 11))
	if !r.OK || r.Drift != 1 || r.Watermark != 11 {
		t.Fatalf("state after rejected registrations = %+v, want ok with original params", r)
	}

	// 对新用户按顺序报第一个原因：已存在优先于参数非法。
	if err := v.Register("nick", 0, -1, 0); err != ErrInvalidPeriod {
		t.Fatalf("order p-before-w: err=%v, want %v", err, ErrInvalidPeriod)
	}
	if err := v.Register("nick", 10, -1, 0); err != ErrNegativeWindow {
		t.Fatalf("order w-before-d: err=%v, want %v", err, ErrNegativeWindow)
	}
}

// 确定性：相同操作与时钟序列必须得到相同结果（双跑对比）。
func TestDeterministicSameSequenceSameResult(t *testing.T) {
	run := func() []string {
		v, _ := newTestVerifier(t, 0)
		var out []string
		steps := []struct {
			sec int64
			msg string
		}{
			{0, ""},
			{0, testCode("olive", 0)},
			{5, testCode("olive", 0)},
			{15, testCode("olive", 2)},
			{25, "bad"},
			{25, testCode("olive", 2)},
		}
		_ = v.Register("olive", 10, 1, 2)
		for _, s := range steps {
			v.SetClock(fixedClock(s.sec))
			r := v.Verify("olive", s.msg)
			out = append(out, fmt.Sprintf("%t/%s/c=%d/win=[%d,%d]/m=%d/d=%d/u=%d",
				r.OK, r.Reason, r.CurrentStep, r.WindowLo, r.WindowHi, r.MatchedStep, r.Drift, r.Watermark))
		}
		return out
	}

	a, b := run(), run()
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("non-deterministic at step %d:\n%s\n%s", i, a[i], b[i])
		}
	}
}

// 日志必须打印输入、输出与判定依据。
func TestDecisionLogging(t *testing.T) {
	v, buf := newTestVerifier(t, 100)
	if err := v.Register("peggy", 10, 1, 1); err != nil {
		t.Fatalf("register: %v", err)
	}
	v.Verify("peggy", testCode("peggy", 10))
	v.Verify("peggy", testCode("peggy", 10))
	v.Verify("peggy", "wrong")
	v.Verify("nobody", "x")
	v.Verify("peggy", "")

	logs := buf.String()
	for _, want := range []string{
		"register input",
		"verify input",
		"reason=ok",
		"reason=code-used",
		"reason=code-wrong",
		"reason=user-not-found",
		"reason=empty-code",
		"basis=match-at-unused-step-10",
		"basis=match-only-at-used-step-10",
		"basis=no-matching-step-in-window",
		"basis=user-not-registered",
		"basis=empty-code",
		`user="peggy"`,
		`code="peggy@10"`,
	} {
		if !strings.Contains(logs, want) {
			t.Errorf("log missing %q\nfull logs:\n%s", want, logs)
		}
	}
}
