package otp

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// testOTP 是确定性的测试口令函数：同一（用户，时间步）恒得同一口令。
func testOTP(user string, step int64) string {
	return fmt.Sprintf("otp-%s-%d", user, step)
}

// at 返回第 step 个时间步的起始时刻（步长 period 秒）。
func at(step, period int64) time.Time {
	return time.Unix(step*period, 0)
}

// logVerify 执行一次验证并在日志中打印输入、输出与判定依据。
func logVerify(t *testing.T, v *Verifier, user, password string, now time.Time, basis string) Result {
	t.Helper()
	res := v.Verify(user, password, now)
	d, u, _ := v.State(user)
	t.Logf("输入: user=%q password=%q now=%d | 输出: %s | 判定依据: %s | 状态: d=%d u=%d",
		user, password, now.Unix(), res, basis, d, u)
	return res
}

func mustRegister(t *testing.T, v *Verifier, user string, period, w, drift int64) {
	t.Helper()
	if err := v.Register(user, period, w, drift); err != nil {
		t.Fatalf("注册 %q 失败: %v", user, err)
	}
	t.Logf("输入: Register(user=%q, P=%d, w=%d, D=%d) | 输出: 成功", user, period, w, drift)
}

// 设备时钟快一步：用未来一步的口令成功后，当前步的合法口令必须被拒（已使用）。
func TestClockOneStepAheadThenCurrentRejected(t *testing.T) {
	v := New(testOTP)
	mustRegister(t, v, "alice", 30, 1, 3)

	// 设备时钟快一步：在 c=10 时提交第 11 步的口令。
	if res := logVerify(t, v, "alice", testOTP("alice", 11), at(10, 30),
		"步 11 在窗口 [9,11]∩[7,13] 内且 > u=-1，应通过并置 d=1, u=11"); res != OK {
		t.Fatalf("期望 OK，得到 %s", res)
	}
	if d, u, _ := v.State("alice"); d != 1 || u != 11 {
		t.Fatalf("期望 d=1, u=11，得到 d=%d, u=%d", d, u)
	}

	// 当前步 c=10 的合法口令：落在窗口内但 10 <= u=11，必须报已使用。
	if res := logVerify(t, v, "alice", testOTP("alice", 10), at(10, 30),
		"步 10 在窗口 [10,12]∩[7,13] 内但 10 <= u=11，应报已使用"); res != Used {
		t.Fatalf("期望 Used，得到 %s", res)
	}
}

// 漂移校正：每次成功都把窗口中心移动到 c+d，旧中心口令最终落在窗口外（报错误而非已使用）。
func TestDriftCorrectionMovesWindowCenter(t *testing.T) {
	v := New(testOTP)
	mustRegister(t, v, "bob", 30, 1, 10)

	// 连续用未来步口令成功，d 逐步增大，窗口中心随之前移。
	for step := int64(1); step <= 3; step++ {
		if res := logVerify(t, v, "bob", testOTP("bob", step), at(0, 30),
			fmt.Sprintf("步 %d 在当前窗口内且 > u，应通过并置 d=%d", step, step)); res != OK {
			t.Fatalf("步 %d 期望 OK，得到 %s", step, res)
		}
	}
	if d, _, _ := v.State("bob"); d != 3 {
		t.Fatalf("期望 d=3，得到 d=%d", d)
	}

	// 此时窗口为 [2,4]∩[-10,10]=[2,4]，步 0 的口令已落在窗口外：报口令错误而非已使用。
	if res := logVerify(t, v, "bob", testOTP("bob", 0), at(0, 30),
		"窗口中心已移至 c+d=3，步 0 不在窗口 [2,4] 内，应报口令错误"); res != Wrong {
		t.Fatalf("期望 Wrong，得到 %s", res)
	}
}

// 恰在漂移上限内外的两个口令：步 c+D 通过，步 c+D+1 拒绝。
func TestDriftLimitBoundary(t *testing.T) {
	v := New(testOTP)
	mustRegister(t, v, "carol", 30, 3, 3)
	mustRegister(t, v, "carol2", 30, 3, 3)

	// 恰在漂移上限内：步 c+D = 5+3 = 8，窗口 [2,8]∩[2,8]。
	if res := logVerify(t, v, "carol", testOTP("carol", 8), at(5, 30),
		"步 8 恰为 c+D=8，在窗口 [2,8] 内且 > u=-1，应通过"); res != OK {
		t.Fatalf("期望 OK，得到 %s", res)
	}

	// 恰在漂移上限外：步 c+D+1 = 9，不在窗口 [2,8] 内。
	if res := logVerify(t, v, "carol2", testOTP("carol2", 9), at(5, 30),
		"步 9 超出 c+D=8，不在窗口 [2,8] 内，应报口令错误"); res != Wrong {
		t.Fatalf("期望 Wrong，得到 %s", res)
	}
}

// 已使用与口令错误必须可区分。
func TestUsedAndWrongAreDistinct(t *testing.T) {
	v := New(testOTP)
	mustRegister(t, v, "dave", 30, 1, 2)

	if res := logVerify(t, v, "dave", testOTP("dave", 4), at(4, 30),
		"步 4 在窗口 [3,5]∩[2,6] 内且 > u=-1，应通过"); res != OK {
		t.Fatalf("期望 OK，得到 %s", res)
	}
	// 同一口令再次提交：窗口内匹配步 4 <= u，报已使用。
	if res := logVerify(t, v, "dave", testOTP("dave", 4), at(4, 30),
		"步 4 <= u=4，应报已使用"); res != Used {
		t.Fatalf("期望 Used，得到 %s", res)
	}
	// 从不匹配任何窗口内步的口令：报口令错误。
	if res := logVerify(t, v, "dave", "not-a-valid-otp", at(4, 30),
		"窗口内无任何匹配步，应报口令错误"); res != Wrong {
		t.Fatalf("期望 Wrong，得到 %s", res)
	}
}

// 失败原因顺序：未注册 > 空口令 > 已使用 > 口令错误，只报第一个；失败不改变 d 与 u。
func TestFailureReasonPrecedence(t *testing.T) {
	v := New(testOTP)
	mustRegister(t, v, "erin", 30, 1, 2)

	if res := logVerify(t, v, "ghost", testOTP("ghost", 0), at(0, 30),
		"用户未注册，应报 NotRegistered"); res != NotRegistered {
		t.Fatalf("期望 NotRegistered，得到 %s", res)
	}
	if res := logVerify(t, v, "erin", "", at(0, 30),
		"口令为空，应报 EmptyPassword"); res != EmptyPassword {
		t.Fatalf("期望 EmptyPassword，得到 %s", res)
	}
	// 失败不得改变 d 与 u。
	if d, u, _ := v.State("erin"); d != 0 || u != -1 {
		t.Fatalf("失败后状态应不变，得到 d=%d, u=%d", d, u)
	}
}

// 注册拒绝：四种原因可区分，且被拒绝的注册不改变已有状态。
func TestRegisterRejections(t *testing.T) {
	v := New(testOTP)
	mustRegister(t, v, "frank", 30, 1, 2)

	cases := []struct {
		name             string
		user             string
		period, w, drift int64
		want             error
	}{
		{"用户已存在", "frank", 30, 1, 2, ErrUserExists},
		{"P 非正", "new1", 0, 1, 2, ErrInvalidPeriod},
		{"w 为负", "new2", 30, -1, 2, ErrInvalidRadius},
		{"D 小于 w", "new3", 30, 2, 1, ErrInvalidDrift},
	}
	for _, tc := range cases {
		err := v.Register(tc.user, tc.period, tc.w, tc.drift)
		t.Logf("输入: Register(%s, P=%d, w=%d, D=%d) | 输出: %v | 判定依据: %s",
			tc.user, tc.period, tc.w, tc.drift, err, tc.name)
		if !errors.Is(err, tc.want) {
			t.Fatalf("%s: 期望 %v，得到 %v", tc.name, tc.want, err)
		}
	}
	// 被拒绝的注册不得改变已有用户的 d 与 u。
	if d, u, ok := v.State("frank"); !ok || d != 0 || u != -1 {
		t.Fatalf("拒绝的注册改变了状态: d=%d, u=%d, ok=%v", d, u, ok)
	}
}

// 并发提交同一口令：恰有一个通过，其余报已使用；不同用户互不影响。
func TestConcurrentSamePassword(t *testing.T) {
	v := New(testOTP)
	mustRegister(t, v, "gina", 30, 1, 2)
	mustRegister(t, v, "hank", 30, 1, 2)

	const goroutines = 32
	now := at(7, 30)

	results := make(chan Result, goroutines+1)
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- v.Verify("gina", testOTP("gina", 7), now)
		}()
	}
	// 另一用户并发提交自己的口令，不应受 gina 影响。
	wg.Add(1)
	go func() {
		defer wg.Done()
		results <- v.Verify("hank", testOTP("hank", 7), now)
	}()
	wg.Wait()
	close(results)

	okCount, usedCount, otherCount := 0, 0, 0
	for res := range results {
		switch res {
		case OK:
			okCount++
		case Used:
			usedCount++
		default:
			otherCount++
		}
	}
	t.Logf("输入: %d 个 goroutine 并发提交 gina 的同一口令 + 1 个 hank | 输出: OK=%d Used=%d 其他=%d | 判定依据: 同一口令恰一个通过，其余报已使用",
		goroutines, okCount, usedCount, otherCount)
	if okCount != 2 { // gina 一个 + hank 一个
		t.Fatalf("期望恰有 2 个 OK（每用户一个），得到 %d", okCount)
	}
	if usedCount != goroutines-1 || otherCount != 0 {
		t.Fatalf("期望 %d 个 Used 且 0 个其他，得到 Used=%d 其他=%d", goroutines-1, usedCount, otherCount)
	}
	if _, u, _ := v.State("gina"); u != 7 {
		t.Fatalf("期望 gina 的 u=7，得到 u=%d", u)
	}
}

// 相同操作与时钟序列应得到相同结果（确定性重放）。
func TestDeterministicReplay(t *testing.T) {
	run := func() []Result {
		v := New(testOTP)
		if err := v.Register("ivan", 30, 1, 3); err != nil {
			t.Fatalf("注册失败: %v", err)
		}
		seq := []struct {
			password string
			step     int64
		}{
			{testOTP("ivan", 5), 5},
			{testOTP("ivan", 5), 5},
			{testOTP("ivan", 4), 5},
			{"garbage", 5},
			{testOTP("ivan", 6), 5},
		}
		out := make([]Result, 0, len(seq))
		for _, op := range seq {
			out = append(out, v.Verify("ivan", op.password, at(op.step, 30)))
		}
		return out
	}
	first, second := run(), run()
	t.Logf("输入: 固定操作与时钟序列重放两次 | 输出: %v vs %v | 判定依据: 两次结果必须一致", first, second)
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("第 %d 步结果不一致: %s vs %s", i, first[i], second[i])
		}
	}
}
