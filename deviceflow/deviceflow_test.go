package deviceflow

import (
	"fmt"
	"reflect"
	"testing"
)

func testConfig(gen func() string) Config {
	return Config{E: 600, I0: 5, D: 5, Imax: 20, H: 100, Cmax: 5, Z: 3, Gen: gen}
}

// uniqueGen 每次返回不同的合规用户码。
func uniqueGen() func() string {
	n := 0
	return func() string {
		n++
		return fmt.Sprintf("CODE-%04d", n)
	}
}

// seqGen 依次返回给定串，用完后重复最后一个。
func seqGen(codes ...string) func() string {
	idx := 0
	return func() string {
		s := codes[idx]
		if idx < len(codes)-1 {
			idx++
		}
		return s
	}
}

func mustNew(t *testing.T, cfg Config) *Service {
	t.Helper()
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func mustStart(t *testing.T, s *Service, client string, now int64) *StartResult {
	t.Helper()
	res, err := s.Start(client, now)
	if err != nil {
		t.Fatalf("Start(%q,%d): %v", client, now, err)
	}
	return res
}

func mustPoll(t *testing.T, s *Service, deviceCode string, now int64) *PollResult {
	t.Helper()
	res, err := s.Poll(deviceCode, now)
	if err != nil {
		t.Fatalf("Poll(%q,%d): %v", deviceCode, now, err)
	}
	return res
}

func expectKind(t *testing.T, err error, kind Kind) *Error {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error kind %s, got nil", kind)
	}
	e, ok := err.(*Error)
	if !ok {
		t.Fatalf("expected *Error, got %T: %v", err, err)
	}
	if e.Kind != kind {
		t.Fatalf("expected kind %s, got %s (%v)", kind, e.Kind, e)
	}
	return e
}

func mustAuthorize(t *testing.T, s *Service, userCode string, approve bool, now int64) {
	t.Helper()
	if err := s.Authorize(userCode, approve, now); err != nil {
		t.Fatalf("Authorize(%q,%v,%d): %v", userCode, approve, now, err)
	}
}

func mustInterval(t *testing.T, s *Service, deviceCode string) *IntervalInfo {
	t.Helper()
	info, err := s.Interval(deviceCode)
	if err != nil {
		t.Fatalf("Interval(%q): %v", deviceCode, err)
	}
	return info
}

// TestSpecPenaltyExample 复现规格中的提前轮询惩罚示例：
// E=600、I0=5、D=5、Imax=20、H=100、Cmax=5、Z=3。
func TestSpecPenaltyExample(t *testing.T) {
	s := mustNew(t, testConfig(uniqueGen()))

	st := mustStart(t, s, "c", 0)
	if st.DeviceCode != "d1" || st.Interval != 5 || st.ExpiresAt != 600 {
		t.Fatalf("Start: %+v", st)
	}
	if info := mustInterval(t, s, "d1"); info.NextAllowed != 0 {
		t.Fatalf("nextAllowed after Start = %d, want 0", info.NextAllowed)
	}

	res := mustPoll(t, s, "d1", 0)
	if res.Outcome != OutcomeAuthorizationPending || res.NextAllowed != 5 {
		t.Fatalf("Poll(d1,0): %+v", res)
	}
	res = mustPoll(t, s, "d1", 3)
	if res.Outcome != OutcomeSlowDown || res.Interval != 10 || res.NextAllowed != 13 {
		t.Fatalf("Poll(d1,3): %+v", res)
	}
	res = mustPoll(t, s, "d1", 12)
	if res.Outcome != OutcomeSlowDown || res.Interval != 15 || res.NextAllowed != 27 {
		t.Fatalf("Poll(d1,12): %+v", res)
	}
	res = mustPoll(t, s, "d1", 27)
	if res.Outcome != OutcomeAuthorizationPending || res.NextAllowed != 42 {
		t.Fatalf("Poll(d1,27): %+v", res)
	}

	st = mustStart(t, s, "c", 50)
	if st.DeviceCode != "d2" || st.Interval != 15 {
		t.Fatalf("Start(c,50): %+v, want interval 15", st)
	}
	st = mustStart(t, s, "c", 105)
	if st.Interval != 10 {
		t.Fatalf("Start(c,105): %+v, want interval 10", st)
	}
	st = mustStart(t, s, "c", 112)
	if st.Interval != 5 {
		t.Fatalf("Start(c,112): %+v, want interval 5", st)
	}
}

// TestSpecGenRetryExample 复现规格中的 gen 重试示例：
// gen 依次返回 ABCD-EFGH、abcdefgh、WXYZ-1234。
func TestSpecGenRetryExample(t *testing.T) {
	s := mustNew(t, testConfig(seqGen("ABCD-EFGH", "abcdefgh", "WXYZ-1234")))

	st1 := mustStart(t, s, "c1", 0)
	if st1.UserCode != "ABCD-EFGH" {
		t.Fatalf("UserCode = %q, want original gen string", st1.UserCode)
	}
	if s.lastGenCalls != 1 {
		t.Fatalf("lastGenCalls = %d, want 1", s.lastGenCalls)
	}

	st2 := mustStart(t, s, "c2", 0)
	if st2.UserCode != "WXYZ-1234" {
		t.Fatalf("UserCode = %q, want WXYZ-1234", st2.UserCode)
	}
	if s.lastGenCalls != 2 {
		t.Fatalf("lastGenCalls = %d, want 2 (1 次重复失败 + 1 次成功)", s.lastGenCalls)
	}

	// 三种规范化写法等价，批准第一个授权。
	mustAuthorize(t, s, "ab-cd-ef-gh", true, 1)
	if info := mustInterval(t, s, st1.DeviceCode); info.Status != StatusApproved {
		t.Fatalf("status = %s, want approved", info.Status)
	}
}

// TestSpecApprovedSlowDownExample 复现规格示例：过快判定先于状态判定，
// 已批准的授权被过快轮询时得不到令牌。
func TestSpecApprovedSlowDownExample(t *testing.T) {
	s := mustNew(t, testConfig(uniqueGen()))
	st := mustStart(t, s, "c", 0)

	res := mustPoll(t, s, st.DeviceCode, 0)
	if res.Outcome != OutcomeAuthorizationPending || res.NextAllowed != 5 {
		t.Fatalf("Poll(d,0): %+v", res)
	}
	mustAuthorize(t, s, st.UserCode, true, 1)

	res = mustPoll(t, s, st.DeviceCode, 3)
	if res.Outcome != OutcomeSlowDown || res.Interval != 10 || res.NextAllowed != 13 {
		t.Fatalf("Poll(d,3): %+v, want slow_down", res)
	}
	res = mustPoll(t, s, st.DeviceCode, 13)
	if res.Outcome != OutcomeToken || res.TokenSeq != 1 || res.Token != "tok-1" {
		t.Fatalf("Poll(d,13): %+v, want token", res)
	}
	res = mustPoll(t, s, st.DeviceCode, 14)
	if res.Outcome != OutcomeInvalidGrant {
		t.Fatalf("Poll(d,14): %+v, want invalid_grant", res)
	}
}

// TestSpecApprovedExpiresUnpolled 复现规格示例：批准后一直不轮询，
// 到期后 Poll 返回已过期。
func TestSpecApprovedExpiresUnpolled(t *testing.T) {
	s := mustNew(t, testConfig(uniqueGen()))
	st := mustStart(t, s, "c", 0)
	mustAuthorize(t, s, st.UserCode, true, 1)

	res := mustPoll(t, s, st.DeviceCode, 600)
	if res.Outcome != OutcomeExpired {
		t.Fatalf("Poll(d,600): %+v, want expired", res)
	}
}

// TestSpecRateLimitExample 复现规格中的限流示例（Z=2）。
func TestSpecRateLimitExample(t *testing.T) {
	cfg := testConfig(uniqueGen())
	cfg.Z = 2
	s := mustNew(t, cfg)

	mustStart(t, s, "c", 0)
	mustPoll(t, s, "d1", 0)
	mustPoll(t, s, "d1", 3)
	mustPoll(t, s, "d1", 12)
	res := mustPoll(t, s, "d1", 20)
	if res.Outcome != OutcomeSlowDown || res.Interval != 20 || res.NextAllowed != 40 {
		t.Fatalf("Poll(d1,20): %+v", res)
	}

	_, err := s.Start("c", 50)
	e := expectKind(t, err, KindRateLimited)
	if e.S != 3 || e.U != 112 {
		t.Fatalf("Start(c,50): s=%d u=%d, want s=3 u=112", e.S, e.U)
	}

	_, err = s.Start("c", 105)
	e = expectKind(t, err, KindRateLimited)
	if e.S != 2 || e.U != 112 {
		t.Fatalf("Start(c,105): s=%d u=%d, want s=2 u=112", e.S, e.U)
	}

	ci, err := s.ClientInterval("c", 105)
	if err != nil {
		t.Fatalf("ClientInterval: %v", err)
	}
	if ci.S != 2 || !ci.Limited || ci.U != 112 || ci.Interval != 15 {
		t.Fatalf("ClientInterval(c,105): %+v", ci)
	}

	st := mustStart(t, s, "c", 112)
	if st.Interval != 10 {
		t.Fatalf("Start(c,112): interval=%d, want 10", st.Interval)
	}

	// ClientInterval 不推进时钟：看过 200 之后，150 仍被接受。
	if _, err := s.ClientInterval("c", 200); err != nil {
		t.Fatalf("ClientInterval(c,200): %v", err)
	}
	st = mustStart(t, s, "c", 150)
	if st.Interval != 5 {
		t.Fatalf("Start(c,150): interval=%d, want 5 (事件均已出窗)", st.Interval)
	}
}

// TestFirstPollImmediatelyAllowed 发起后首次轮询立即允许（nextAllowed=now）。
func TestFirstPollImmediatelyAllowed(t *testing.T) {
	s := mustNew(t, testConfig(uniqueGen()))
	st := mustStart(t, s, "c", 7)
	res := mustPoll(t, s, st.DeviceCode, 7)
	if res.Outcome != OutcomeAuthorizationPending {
		t.Fatalf("first poll at start time: %+v, want authorization_pending", res)
	}
}

// TestNextAllowedBoundary nextAllowed 恰等于 now 允许，小 1 过快。
func TestNextAllowedBoundary(t *testing.T) {
	s := mustNew(t, testConfig(uniqueGen()))
	st := mustStart(t, s, "c", 0)
	mustPoll(t, s, st.DeviceCode, 0) // nextAllowed=5

	res := mustPoll(t, s, st.DeviceCode, 4)
	if res.Outcome != OutcomeSlowDown {
		t.Fatalf("Poll at nextAllowed-1: %+v, want slow_down", res)
	}
	// 过快后 nextAllowed = 4+10 = 14：恰等于 14 允许。
	res = mustPoll(t, s, st.DeviceCode, 14)
	if res.Outcome != OutcomeAuthorizationPending {
		t.Fatalf("Poll at 14 (==nextAllowed): %+v, want authorization_pending", res)
	}

	// 另一实例：nextAllowed=14 时，13 过快。
	s2 := mustNew(t, testConfig(uniqueGen()))
	st2 := mustStart(t, s2, "c", 0)
	mustPoll(t, s2, st2.DeviceCode, 0)
	mustPoll(t, s2, st2.DeviceCode, 4) // nextAllowed=14
	res = mustPoll(t, s2, st2.DeviceCode, 13)
	if res.Outcome != OutcomeSlowDown {
		t.Fatalf("Poll at 13 (<14): %+v, want slow_down", res)
	}
}

// TestIntervalEscalatesAndCaps 连续过快使 interval 递增并封顶于 Imax，
// 到达 Imax 后仍记事件并按 Imax 重设 nextAllowed。
func TestIntervalEscalatesAndCaps(t *testing.T) {
	s := mustNew(t, testConfig(uniqueGen()))
	st := mustStart(t, s, "c", 0)
	mustPoll(t, s, st.DeviceCode, 0) // nextAllowed=5

	type step struct {
		now          int64
		wantInterval int64
		wantNext     int64
	}
	steps := []step{
		{1, 10, 11},
		{2, 15, 17},
		{3, 20, 23},
		{4, 20, 24}, // 已封顶，仍记事件并重设
		{5, 20, 25},
	}
	for i, stp := range steps {
		res := mustPoll(t, s, st.DeviceCode, stp.now)
		if res.Outcome != OutcomeSlowDown || res.Interval != stp.wantInterval || res.NextAllowed != stp.wantNext {
			t.Fatalf("step %d: %+v, want slow_down interval=%d nextAllowed=%d",
				i, res, stp.wantInterval, stp.wantNext)
		}
	}
	ci, err := s.ClientInterval("c", 5)
	if err != nil {
		t.Fatalf("ClientInterval: %v", err)
	}
	if ci.S != 5 {
		t.Fatalf("S = %d, want 5（封顶后仍记事件）", ci.S)
	}
}

// TestSlowDownResetsNextAllowedFromPenaltyTime 过快后 nextAllowed 以
// 惩罚时刻重设（now+新 interval），而不是沿用旧值累加。
func TestSlowDownResetsNextAllowedFromPenaltyTime(t *testing.T) {
	s := mustNew(t, testConfig(uniqueGen()))
	st := mustStart(t, s, "c", 0)
	mustPoll(t, s, st.DeviceCode, 0) // nextAllowed=5

	res := mustPoll(t, s, st.DeviceCode, 3)
	if res.NextAllowed != 13 { // 3+10，而非 5+10
		t.Fatalf("nextAllowed = %d, want 13 (3+10)", res.NextAllowed)
	}
	res = mustPoll(t, s, st.DeviceCode, 10)
	if res.NextAllowed != 25 { // 10+15，而非 13+15
		t.Fatalf("nextAllowed = %d, want 25 (10+15)", res.NextAllowed)
	}
}

// TestDeniedSlowDown 已拒绝的授权被过快轮询时同样返回过快而非拒绝访问。
func TestDeniedSlowDown(t *testing.T) {
	s := mustNew(t, testConfig(uniqueGen()))
	st := mustStart(t, s, "c", 0)
	mustPoll(t, s, st.DeviceCode, 0) // nextAllowed=5
	mustAuthorize(t, s, st.UserCode, false, 1)

	res := mustPoll(t, s, st.DeviceCode, 3)
	if res.Outcome != OutcomeSlowDown {
		t.Fatalf("Poll(denied,3): %+v, want slow_down", res)
	}
	res = mustPoll(t, s, st.DeviceCode, 13)
	if res.Outcome != OutcomeAccessDenied {
		t.Fatalf("Poll(denied,13): %+v, want access_denied", res)
	}
	res = mustPoll(t, s, st.DeviceCode, 14)
	if res.Outcome != OutcomeInvalidGrant {
		t.Fatalf("Poll(denied,14): %+v, want invalid_grant", res)
	}
}

// TestTokenOnlyOnce 令牌只返回一次，随后为无效授权。
func TestTokenOnlyOnce(t *testing.T) {
	s := mustNew(t, testConfig(uniqueGen()))
	st := mustStart(t, s, "c", 0)
	mustAuthorize(t, s, st.UserCode, true, 0)

	res := mustPoll(t, s, st.DeviceCode, 0)
	if res.Outcome != OutcomeToken || res.TokenSeq != 1 {
		t.Fatalf("first poll: %+v, want token", res)
	}
	for _, now := range []int64{1, 2, 100} {
		res = mustPoll(t, s, st.DeviceCode, now)
		if res.Outcome != OutcomeInvalidGrant {
			t.Fatalf("Poll at %d: %+v, want invalid_grant", now, res)
		}
	}
}

// TestExpiredPrecedence 已过期先于过快与 pending/approved/denied；
// consumed 先于已过期。
func TestExpiredPrecedence(t *testing.T) {
	// 已过期先于过快与 pending：interval 大于有效期。
	cfg := testConfig(uniqueGen())
	cfg.E = 10
	cfg.I0 = 100
	cfg.Imax = 100
	s := mustNew(t, cfg)
	st := mustStart(t, s, "c", 0)
	mustPoll(t, s, st.DeviceCode, 0) // nextAllowed=100
	res := mustPoll(t, s, st.DeviceCode, 50)
	if res.Outcome != OutcomeExpired {
		t.Fatalf("Poll at 50 (nextAllowed=100, expired at 10): %+v, want expired", res)
	}

	// 已过期先于 approved。
	s2 := mustNew(t, cfg)
	st2 := mustStart(t, s2, "c", 0)
	mustAuthorize(t, s2, st2.UserCode, true, 1)
	if res := mustPoll(t, s2, st2.DeviceCode, 10); res.Outcome != OutcomeExpired {
		t.Fatalf("approved expired: %+v, want expired", res)
	}

	// 已过期先于 denied。
	s3 := mustNew(t, cfg)
	st3 := mustStart(t, s3, "c", 0)
	mustAuthorize(t, s3, st3.UserCode, false, 1)
	if res := mustPoll(t, s3, st3.DeviceCode, 10); res.Outcome != OutcomeExpired {
		t.Fatalf("denied expired: %+v, want expired", res)
	}

	// consumed 先于已过期。
	s4 := mustNew(t, cfg)
	st4 := mustStart(t, s4, "c", 0)
	mustAuthorize(t, s4, st4.UserCode, true, 0)
	if res := mustPoll(t, s4, st4.DeviceCode, 0); res.Outcome != OutcomeToken {
		t.Fatalf("poll for token: %+v", res)
	}
	if res := mustPoll(t, s4, st4.DeviceCode, 50); res.Outcome != OutcomeInvalidGrant {
		t.Fatalf("consumed after expiry: %+v, want invalid_grant", res)
	}
}

// TestRateLimitBoundaryAndU 限流：s 恰等于 Z 被拒，s 小 1 通过；
// u 取第 s−Z+1 个窗口内事件的时刻加 H。
func TestRateLimitBoundaryAndU(t *testing.T) {
	s := mustNew(t, testConfig(uniqueGen())) // Z=3
	mustStart(t, s, "c", 0)
	mustPoll(t, s, "d1", 0)
	mustPoll(t, s, "d1", 3)  // 事件@3
	mustPoll(t, s, "d1", 12) // 事件@12
	mustPoll(t, s, "d1", 20) // 事件@20
	mustPoll(t, s, "d1", 25) // 事件@25（20 过快后 nextAllowed=40，25<40）

	// 窗口内事件 [3,12,20,25]，s=4 > Z=3，u 取第 2 个事件 12+100。
	_, err := s.Start("c", 50)
	e := expectKind(t, err, KindRateLimited)
	if e.S != 4 || e.U != 112 {
		t.Fatalf("Start(c,50): s=%d u=%d, want s=4 u=112", e.S, e.U)
	}

	// now=103：事件 3 恰出窗（3+100 不大于 103），s=3 恰等于 Z 仍被拒，
	// u 取第 1 个窗口内事件 12+100。
	_, err = s.Start("c", 103)
	e = expectKind(t, err, KindRateLimited)
	if e.S != 3 || e.U != 112 {
		t.Fatalf("Start(c,103): s=%d u=%d, want s=3 u=112", e.S, e.U)
	}

	// now=112：事件 12 也出窗，s=2 = Z-1，通过。
	st := mustStart(t, s, "c", 112)
	if st.Interval != 15 { // min(20, 5+5*2)
		t.Fatalf("Start(c,112): interval=%d, want 15", st.Interval)
	}
}

// TestRateLimitBeforeCap 限流先于超限：同时满足两者时报限流。
func TestRateLimitBeforeCap(t *testing.T) {
	cfg := testConfig(uniqueGen())
	cfg.Cmax = 1
	cfg.Z = 1
	s := mustNew(t, cfg)
	mustStart(t, s, "c", 0)   // 活跃名额已满
	mustPoll(t, s, "d1", 0)   // nextAllowed=5
	mustPoll(t, s, "d1", 1)   // 过快，事件@1
	_, err := s.Start("c", 2) // s=1>=Z 且活跃=1=Cmax
	e := expectKind(t, err, KindRateLimited)
	if e.S != 1 || e.U != 101 {
		t.Fatalf("want rate_limited s=1 u=101, got s=%d u=%d", e.S, e.U)
	}
}

// TestEventWindowBoundary 事件 t+H 恰等于 now 已出窗，差 1 仍计。
func TestEventWindowBoundary(t *testing.T) {
	s := mustNew(t, testConfig(uniqueGen()))
	mustStart(t, s, "c", 0)
	mustPoll(t, s, "d1", 0)
	mustPoll(t, s, "d1", 1) // 事件@1，t+H=101

	ci, err := s.ClientInterval("c", 100)
	if err != nil {
		t.Fatalf("ClientInterval: %v", err)
	}
	if ci.S != 1 {
		t.Fatalf("at now=100: S=%d, want 1（101>100 仍计）", ci.S)
	}
	ci, err = s.ClientInterval("c", 101)
	if err != nil {
		t.Fatalf("ClientInterval: %v", err)
	}
	if ci.S != 0 {
		t.Fatalf("at now=101: S=%d, want 0（101 不大于 101 已出窗）", ci.S)
	}
}

// TestBaseIntervalCappedAtImax 客户端基础间隔封顶于 Imax。
func TestBaseIntervalCappedAtImax(t *testing.T) {
	cfg := testConfig(uniqueGen())
	cfg.Z = 100
	s := mustNew(t, cfg)
	mustStart(t, s, "c", 0)
	mustPoll(t, s, "d1", 0)
	for i := int64(1); i <= 10; i++ {
		mustPoll(t, s, "d1", i) // 10 次过快事件
	}
	ci, err := s.ClientInterval("c", 10)
	if err != nil {
		t.Fatalf("ClientInterval: %v", err)
	}
	if ci.S != 10 || ci.Interval != 20 { // min(20, 5+5*10)
		t.Fatalf("ClientInterval: %+v, want S=10 Interval=20", ci)
	}
	st := mustStart(t, s, "c", 10)
	if st.Interval != 20 {
		t.Fatalf("new auth interval=%d, want 20（基础间隔封顶）", st.Interval)
	}
}

// TestNewAuthUsesCurrentBaseInterval 新授权不继承旧授权的 interval，
// 而取发起时刻的客户端基础间隔。
func TestNewAuthUsesCurrentBaseInterval(t *testing.T) {
	s := mustNew(t, testConfig(uniqueGen()))
	mustStart(t, s, "c", 0)
	mustPoll(t, s, "d1", 0)
	mustPoll(t, s, "d1", 1)
	mustPoll(t, s, "d1", 2)
	mustPoll(t, s, "d1", 3) // d1 interval 已升至 20

	st := mustStart(t, s, "c", 200) // 事件 1,2,3 均已出窗
	if st.Interval != 5 {
		t.Fatalf("d2 interval=%d, want 5（不继承 d1 的 20）", st.Interval)
	}
	if info := mustInterval(t, s, "d1"); info.Interval != 20 {
		t.Fatalf("d1 interval=%d, want 20（旧授权不受影响）", info.Interval)
	}
}

// TestActiveCapAndRelease 活跃名额：恰满被拒；denied、consumed、到期
// 均释放；approved 未领取到期前一直占用；名额按客户端隔离。
func TestActiveCapAndRelease(t *testing.T) {
	cfg := testConfig(uniqueGen())
	cfg.Cmax = 2
	cfg.Z = 100
	s := mustNew(t, cfg)

	d1 := mustStart(t, s, "c", 0)
	d2 := mustStart(t, s, "c", 0)
	if _, err := s.Start("c", 0); true {
		expectKind(t, err, KindTooManyActive)
	}
	// 名额按客户端隔离。
	if _, err := s.Start("other", 0); err != nil {
		t.Fatalf("other client should not be limited: %v", err)
	}

	// denied 立即释放。
	mustAuthorize(t, s, d2.UserCode, false, 1)
	d4 := mustStart(t, s, "c", 1)
	if _, err := s.Start("c", 1); true {
		expectKind(t, err, KindTooManyActive)
	}

	// consumed 释放（approved 领取令牌后）。
	mustAuthorize(t, s, d1.UserCode, true, 2)
	if res := mustPoll(t, s, d1.DeviceCode, 2); res.Outcome != OutcomeToken {
		t.Fatalf("poll for token: %+v", res)
	}
	d5 := mustStart(t, s, "c", 2)
	if _, err := s.Start("c", 2); true {
		expectKind(t, err, KindTooManyActive)
	}

	// 到期释放：d4 到期于 601，d5 到期于 602。
	if _, err := s.Start("c", 601); err != nil {
		t.Fatalf("Start at 601 should succeed (d4 expired): %v", err)
	}
	_ = d4
	_ = d5
}

// TestApprovedOccupiesUntilExpiry approved 未领取到期前一直占用名额。
func TestApprovedOccupiesUntilExpiry(t *testing.T) {
	cfg := testConfig(uniqueGen())
	cfg.Cmax = 1
	cfg.Z = 100
	s := mustNew(t, cfg)
	st := mustStart(t, s, "c", 0)
	mustAuthorize(t, s, st.UserCode, true, 1)

	if _, err := s.Start("c", 1); true {
		expectKind(t, err, KindTooManyActive)
	}
	if _, err := s.Start("c", 599); true {
		expectKind(t, err, KindTooManyActive)
	}
	if _, err := s.Start("c", 600); err != nil {
		t.Fatalf("Start at 600 should succeed (approved expired): %v", err)
	}
}

// TestUserCodeNormalization 用户码规范化：三种写法等价。
func TestUserCodeNormalization(t *testing.T) {
	for _, writing := range []string{"ABCD-EFGH", "abcdefgh", "ab-cd-ef-gh"} {
		s := mustNew(t, testConfig(seqGen("ABCD-EFGH")))
		st := mustStart(t, s, "c", 0)
		mustAuthorize(t, s, writing, true, 0)
		if info := mustInterval(t, s, st.DeviceCode); info.Status != StatusApproved {
			t.Fatalf("writing %q: status=%s, want approved", writing, info.Status)
		}
	}

	// 规范化函数本身的边界。
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"abcd", "ABCD", true},
		{"a-b-c-d", "ABCD", true},
		{"ABCD-1234-EFGH-5678", "ABCD1234EFGH5678", true},
		{"abc", "", false},               // 少于 4
		{"abcdefghijklmnopq", "", false}, // 多于 16
		{"----", "", false},              // 去连字符后为空
		{"ABC!", "", false},              // 非法字符
		{"AB CD", "", false},             // 空格非法
		{"ABCD_EFG", "", false},          // 下划线非法
	}
	for _, c := range cases {
		got, ok := normalizeCode(c.in)
		if ok != c.ok || (ok && got != c.want) {
			t.Fatalf("normalizeCode(%q) = %q,%v, want %q,%v", c.in, got, ok, c.want, c.ok)
		}
	}
}

// TestInvalidGenCountsAsFailure 不合规的 gen 返回算一次失败尝试。
func TestInvalidGenCountsAsFailure(t *testing.T) {
	s := mustNew(t, testConfig(seqGen("ab", "ABC!", "abcdefghijklmnopq", "OKAY-1234")))
	st := mustStart(t, s, "c", 0)
	if st.UserCode != "OKAY-1234" {
		t.Fatalf("UserCode = %q, want OKAY-1234", st.UserCode)
	}
	if s.lastGenCalls != 4 { // 3 次失败 + 1 次成功
		t.Fatalf("lastGenCalls = %d, want 4", s.lastGenCalls)
	}
}

// TestGenFailure 连续 100 次失败报生成失败，gen 恰被调用 100 次，
// 且被拒绝后不改变状态、不推进时钟。
func TestGenFailure(t *testing.T) {
	calls := 0
	bad := func() string { calls++; return "xx" }
	cfg := testConfig(bad)
	s := mustNew(t, cfg)

	mustStartCalls := calls
	_ = mustStartCalls

	// 先成功一次（临时切到好 gen 不可行，故直接验证失败路径）。
	_, err := s.Start("c", 5)
	expectKind(t, err, KindCodeGeneration)
	if calls != 100 {
		t.Fatalf("gen calls = %d, want 100", calls)
	}
	if s.lastGenCalls != 100 {
		t.Fatalf("lastGenCalls = %d, want 100", s.lastGenCalls)
	}
	// 设备码未发出。
	if _, err := s.Interval("d1"); true {
		expectKind(t, err, KindUnknownDeviceCode)
	}
	// 时钟未推进：换好 gen 后以 now=0 发起仍被接受。
	s.cfg.Gen = uniqueGen()
	if _, err := s.Start("c", 0); err != nil {
		t.Fatalf("Start after gen failure should not see clock advance: %v", err)
	}
}

// TestDuplicateUserCodeRules 与未到期授权（含 denied 与 consumed）
// 重复才算冲突，已到期的可复用。
func TestDuplicateUserCodeRules(t *testing.T) {
	cfg := testConfig(uniqueGen())
	cfg.Z = 100

	// denied 未到期仍冲突。
	s := mustNew(t, cfg)
	d1 := mustStart(t, s, "c1", 0)
	mustAuthorize(t, s, d1.UserCode, false, 1)
	s.cfg.Gen = seqGen(d1.UserCode, "BBBB-2222")
	st := mustStart(t, s, "c2", 1)
	if st.UserCode != "BBBB-2222" || s.lastGenCalls != 2 {
		t.Fatalf("denied 冲突: %+v calls=%d", st, s.lastGenCalls)
	}

	// consumed 未到期仍冲突。
	s2 := mustNew(t, cfg)
	d2 := mustStart(t, s2, "c1", 0)
	mustAuthorize(t, s2, d2.UserCode, true, 0)
	if res := mustPoll(t, s2, d2.DeviceCode, 0); res.Outcome != OutcomeToken {
		t.Fatalf("poll for token: %+v", res)
	}
	s2.cfg.Gen = seqGen(d2.UserCode, "CCCC-3333")
	st = mustStart(t, s2, "c2", 0)
	if st.UserCode != "CCCC-3333" || s2.lastGenCalls != 2 {
		t.Fatalf("consumed 冲突: %+v calls=%d", st, s2.lastGenCalls)
	}

	// 已到期可复用：d1 到期于 600。
	s.cfg.Gen = seqGen(d1.UserCode)
	st = mustStart(t, s, "c3", 600)
	if st.UserCode != d1.UserCode {
		t.Fatalf("到期复用: %+v", st)
	}
}

// TestExpiredBeforeDecided Authorize 时已过期先于已决定。
func TestExpiredBeforeDecided(t *testing.T) {
	s := mustNew(t, testConfig(uniqueGen()))
	st := mustStart(t, s, "c", 0)
	mustAuthorize(t, s, st.UserCode, true, 1)

	expectKind(t, s.Authorize(st.UserCode, false, 2), KindAlreadyDecided)
	expectKind(t, s.Authorize(st.UserCode, false, 600), KindExpired)
}

// TestUnknownVsNotFound 未知设备码与未找到用户码的区分。
func TestUnknownVsNotFound(t *testing.T) {
	s := mustNew(t, testConfig(uniqueGen()))
	st := mustStart(t, s, "c", 0)

	_, err := s.Poll("d999", 0)
	expectKind(t, err, KindUnknownDeviceCode)
	_, err = s.Interval("d999")
	expectKind(t, err, KindUnknownDeviceCode)
	expectKind(t, s.Authorize("ZZZZ-9999", true, 0), KindNotFound)

	// 参数非法与上述两者区分。
	_, err = s.Poll("", 0)
	expectKind(t, err, KindInvalidParam)
	_, err = s.Interval("")
	expectKind(t, err, KindInvalidParam)
	expectKind(t, s.Authorize("ab", true, 0), KindInvalidParam)
	_, err = s.Poll(st.DeviceCode, -1)
	expectKind(t, err, KindInvalidParam)
	_, err = s.Poll(st.DeviceCode, maxNowValue+1)
	expectKind(t, err, KindInvalidParam)
	_, err = s.Start("", 0)
	expectKind(t, err, KindInvalidParam)
	_, err = s.ClientInterval("", 0)
	expectKind(t, err, KindInvalidParam)
}

// TestRejectedOpsDoNotChangeState 被拒绝的操作不得改变任何授权、
// 过快事件、计数器与时钟。
func TestRejectedOpsDoNotChangeState(t *testing.T) {
	genCalls := 0
	gen := func() string {
		genCalls++
		return fmt.Sprintf("CODE-%04d", genCalls)
	}
	s := mustNew(t, testConfig(gen))

	st := mustStart(t, s, "c", 0)
	mustPoll(t, s, st.DeviceCode, 0) // nextAllowed=5
	mustPoll(t, s, st.DeviceCode, 5) // nextAllowed=10，maxNow=5

	before := mustInterval(t, s, st.DeviceCode)
	ciBefore, err := s.ClientInterval("c", 5)
	if err != nil {
		t.Fatalf("ClientInterval: %v", err)
	}
	genCallsBefore := genCalls

	// 各类拒绝。
	expectKind(t, func() error { _, e := s.Start("", 5); return e }(), KindInvalidParam)
	expectKind(t, func() error { _, e := s.Start("c", 4); return e }(), KindClockRollback)
	expectKind(t, func() error { _, e := s.Poll("", 5); return e }(), KindInvalidParam)
	expectKind(t, func() error { _, e := s.Poll("d99", 5); return e }(), KindUnknownDeviceCode)
	expectKind(t, func() error { _, e := s.Poll(st.DeviceCode, 4); return e }(), KindClockRollback)
	expectKind(t, s.Authorize("x", true, 5), KindInvalidParam)
	expectKind(t, s.Authorize("ZZZZ-0000", true, 5), KindNotFound)
	expectKind(t, s.Authorize(st.UserCode, true, 4), KindClockRollback)
	expectKind(t, func() error { _, e := s.ClientInterval("c", 4); return e }(), KindClockRollback)
	expectKind(t, func() error { _, e := s.ClientInterval("", 5); return e }(), KindInvalidParam)

	// 授权、事件、计数器均未变。
	after := mustInterval(t, s, st.DeviceCode)
	if *after != *before {
		t.Fatalf("auth changed by rejected ops: before=%+v after=%+v", before, after)
	}
	ciAfter, err := s.ClientInterval("c", 5)
	if err != nil {
		t.Fatalf("ClientInterval: %v", err)
	}
	if *ciAfter != *ciBefore {
		t.Fatalf("client stats changed: before=%+v after=%+v", ciBefore, ciAfter)
	}
	if genCalls != genCallsBefore {
		t.Fatalf("gen called by rejected ops: %d -> %d", genCallsBefore, genCalls)
	}
	// 时钟未推进：now=5 的操作仍被接受。
	res := mustPoll(t, s, st.DeviceCode, 10)
	if res.Outcome != OutcomeAuthorizationPending || res.NextAllowed != 15 {
		t.Fatalf("Poll after rejected ops: %+v", res)
	}
	// 设备码序号未被消耗。
	st2 := mustStart(t, s, "c", 10)
	if st2.DeviceCode != "d2" {
		t.Fatalf("DeviceCode = %s, want d2", st2.DeviceCode)
	}
}

// TestTooManyActiveRejectedNoGenCall 超限被拒绝时不调用 gen。
func TestTooManyActiveRejectedNoGenCall(t *testing.T) {
	genCalls := 0
	gen := func() string {
		genCalls++
		return fmt.Sprintf("CODE-%04d", genCalls)
	}
	cfg := testConfig(gen)
	cfg.Cmax = 1
	s := mustNew(t, cfg)
	mustStart(t, s, "c", 0)
	if genCalls != 1 {
		t.Fatalf("genCalls = %d, want 1", genCalls)
	}
	expectKind(t, func() error { _, e := s.Start("c", 0); return e }(), KindTooManyActive)
	if genCalls != 1 {
		t.Fatalf("gen called on rejected Start: genCalls = %d", genCalls)
	}
}

// TestConfigValidation 配置非法整体拒绝。
func TestConfigValidation(t *testing.T) {
	valid := testConfig(nil)
	bad := []Config{
		{E: 0, I0: 1, D: 1, Imax: 1, H: 1, Cmax: 1, Z: 1},
		{E: 1_000_000_001, I0: 1, D: 1, Imax: 1, H: 1, Cmax: 1, Z: 1},
		{E: 1, I0: 0, D: 1, Imax: 1, H: 1, Cmax: 1, Z: 1},
		{E: 1, I0: 1_000_001, D: 1, Imax: 1_000_001, H: 1, Cmax: 1, Z: 1},
		{E: 1, I0: 1, D: 0, Imax: 1, H: 1, Cmax: 1, Z: 1},
		{E: 1, I0: 1, D: 1_000_001, Imax: 1, H: 1, Cmax: 1, Z: 1},
		{E: 1, I0: 1, D: 1, Imax: 0, H: 1, Cmax: 1, Z: 1},
		{E: 1, I0: 2, D: 1, Imax: 1, H: 1, Cmax: 1, Z: 1}, // I0 > Imax
		{E: 1, I0: 1, D: 1, Imax: 1, H: 0, Cmax: 1, Z: 1},
		{E: 1, I0: 1, D: 1, Imax: 1, H: 1, Cmax: 0, Z: 1},
		{E: 1, I0: 1, D: 1, Imax: 1, H: 1, Cmax: 1_000_001, Z: 1},
		{E: 1, I0: 1, D: 1, Imax: 1, H: 1, Cmax: 1, Z: 0},
		{E: 1, I0: 1, D: 1, Imax: 1, H: 1, Cmax: 1, Z: 1_000_001},
	}
	for i, cfg := range bad {
		if _, err := New(cfg); err == nil {
			t.Fatalf("bad config %d accepted: %+v", i, cfg)
		} else if k, _ := AsKind(err); k != KindInvalidParam {
			t.Fatalf("bad config %d: kind=%s, want invalid_param", i, k)
		}
	}
	if _, err := New(valid); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	edge := Config{E: 1, I0: 1, D: 1, Imax: 1, H: 1, Cmax: 1, Z: 1}
	if _, err := New(edge); err != nil {
		t.Fatalf("edge config rejected: %v", err)
	}
}

// TestReapPopCounter 回收的弹出次数不超过本次到期的授权数加一。
func TestReapPopCounter(t *testing.T) {
	cfg := testConfig(uniqueGen())
	cfg.E = 10
	cfg.Cmax = 10
	cfg.Z = 100
	s := mustNew(t, cfg)
	mustStart(t, s, "c", 0) // d1，到期于 10
	mustStart(t, s, "c", 0) // d2，到期于 10
	mustStart(t, s, "c", 0) // d3，到期于 10

	mustPoll(t, s, "d1", 5)
	if s.lastReapPops != 0 {
		t.Fatalf("pops at now=5 = %d, want 0", s.lastReapPops)
	}
	res := mustPoll(t, s, "d2", 10)
	if res.Outcome != OutcomeExpired {
		t.Fatalf("Poll(d2,10): %+v, want expired", res)
	}
	// 三条授权同时到期：弹出 3 次，不超过 3+1。
	if s.lastReapPops != 3 || s.lastReapPops > 3+1 {
		t.Fatalf("pops = %d, want 3 (<= expired+1)", s.lastReapPops)
	}
	// 只读操作不触发回收。
	if _, err := s.ClientInterval("c", 20); err != nil {
		t.Fatalf("ClientInterval: %v", err)
	}
	if s.lastReapPops != 3 {
		t.Fatalf("read-only op triggered reap: pops = %d", s.lastReapPops)
	}
}

// TestReplayDeterminism 相同的操作序列（含 gen 返回序列）重放得到
// 完全相同的设备码、间隔、返回类别与令牌序号。
func TestReplayDeterminism(t *testing.T) {
	run := func() []any {
		s := mustNew(t, testConfig(seqGen(
			"AAAA-1111", "bbbb-2222", "AAAA-1111", "CCCC-3333", "bad!", "DDDD-4444",
		)))
		var out []any
		rec := func(v any, err error) {
			if err != nil {
				out = append(out, err.(*Error).Kind)
			} else {
				out = append(out, v)
			}
		}
		r1, e1 := s.Start("c", 0)
		rec(r1, e1)
		r2, e2 := s.Poll("d1", 0)
		rec(r2, e2)
		r3, e3 := s.Poll("d1", 2)
		rec(r3, e3)
		rec(nil, s.Authorize("aaaa-1111", true, 3))
		r4, e4 := s.Poll("d1", 20)
		rec(r4, e4)
		r5, e5 := s.Start("c", 30) // gen 序列含重复与不合规
		rec(r5, e5)
		r6, e6 := s.Start("c", 40)
		rec(r6, e6)
		r7, e7 := s.Poll("d2", 40)
		rec(r7, e7)
		r8, e8 := s.ClientInterval("c", 40)
		rec(r8, e8)
		r9, e9 := s.Interval("d1")
		rec(r9, e9)
		return out
	}
	first := run()
	second := run()
	if len(first) != len(second) {
		t.Fatalf("output length mismatch")
	}
	for i := range first {
		if !reflect.DeepEqual(first[i], second[i]) {
			t.Fatalf("replay mismatch at %d: %v vs %v", i, first[i], second[i])
		}
	}
}
