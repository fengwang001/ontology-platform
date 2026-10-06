package remittance

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

func testConfig() Config {
	return Config{
		SingleLimit:     1000,
		DayLimit:        2000,
		YearLimit:       5000,
		QuoteTTL:        100,
		ReviewThreshold: 500,
		ReviewTimeout:   100,
	}
}

func newTestSystem(t *testing.T) *System {
	t.Helper()
	s, err := NewSystem(testConfig())
	if err != nil {
		t.Fatalf("NewSystem: %v", err)
	}
	return s
}

func mustQuote(t *testing.T, s *System, remitter string, srcAmount, rate, now int64) string {
	t.Helper()
	id, err := s.RequestQuote(remitter, srcAmount, rate, now)
	if err != nil {
		t.Fatalf("RequestQuote(%v, %v, %v, %v): %v", remitter, srcAmount, rate, now, err)
	}
	return id
}

func mustSubmit(t *testing.T, s *System, remitter, key, quoteID, payee string, now int64) SubmitResult {
	t.Helper()
	res, err := s.Submit(remitter, key, quoteID, payee, now)
	if err != nil {
		t.Fatalf("Submit(%v, %v, %v, %v, %v): %v", remitter, key, quoteID, payee, now, err)
	}
	return res
}

func usage(t *testing.T, s *System, remitter string, now int64) Usage {
	t.Helper()
	u, err := s.QueryUsage(remitter, now)
	if err != nil {
		t.Fatalf("QueryUsage(%v, %v): %v", remitter, now, err)
	}
	return u
}

// 报价到期恰等仍有效，晚一秒即过期。
func TestQuoteExpiryBoundary(t *testing.T) {
	s := newTestSystem(t)
	q1 := mustQuote(t, s, "alice", 100, 1_000_000, 50)
	// 恰等到期时刻 now=50+100=150：仍有效。
	res := mustSubmit(t, s, "alice", "k1", q1, "bob", 150)
	if res.TargetAmount != 100 || res.HoldAmount != 100 {
		t.Fatalf("unexpected amounts: %+v", res)
	}

	q2 := mustQuote(t, s, "alice", 100, 1_000_000, 200)
	// 晚一秒 now=200+100+1=301：已过期。
	if _, err := s.Submit("alice", "k2", q2, "bob", 301); !errors.Is(err, ErrQuoteExpired) {
		t.Fatalf("want ErrQuoteExpired, got %v", err)
	}
	// 过期报价保持未消耗但不可用；重复提交仍报过期。
	if _, err := s.Submit("alice", "k3", q2, "bob", 301); !errors.Is(err, ErrQuoteExpired) {
		t.Fatalf("want ErrQuoteExpired again, got %v", err)
	}
}

// 目标额向下取整、占用额向上取整的差异。
func TestFloorVsCeil(t *testing.T) {
	s := newTestSystem(t)
	// 3 * 1_500_000 / 1e6 = 4.5：目标额 4，占用额 5。
	q := mustQuote(t, s, "alice", 3, 1_500_000, 0)
	res := mustSubmit(t, s, "alice", "k1", q, "bob", 0)
	if res.TargetAmount != 4 || res.HoldAmount != 5 {
		t.Fatalf("want target=4 hold=5, got %+v", res)
	}
	if u := usage(t, s, "alice", 0); u.DayUsed != 5 || u.RollingYearUsed != 5 {
		t.Fatalf("want usage 5/5, got %+v", u)
	}
	// 整除时两者相等：2 * 1_000_000 / 1e6 = 2。
	q2 := mustQuote(t, s, "alice", 2, 1_000_000, 0)
	res2 := mustSubmit(t, s, "alice", "k2", q2, "bob", 0)
	if res2.TargetAmount != 2 || res2.HoldAmount != 2 {
		t.Fatalf("want target=2 hold=2, got %+v", res2)
	}
	// 极小乘积：1 * 1 / 1e6：目标额 0，占用额 1。
	q3 := mustQuote(t, s, "alice", 1, 1, 0)
	res3 := mustSubmit(t, s, "alice", "k3", q3, "bob", 0)
	if res3.TargetAmount != 0 || res3.HoldAmount != 1 {
		t.Fatalf("want target=0 hold=1, got %+v", res3)
	}
}

// 占用额恰等于限额视为满足；多一单位即拒绝。
func TestExactLimitBoundary(t *testing.T) {
	s := newTestSystem(t) // 单笔 1000，日 2000，年 5000
	// 恰等单笔限额。
	q1 := mustQuote(t, s, "alice", 1000, 1_000_000, 0)
	mustSubmit(t, s, "alice", "k1", q1, "bob", 0)
	// 超单笔一单位。
	q2 := mustQuote(t, s, "alice", 1001, 1_000_000, 0)
	if _, err := s.Submit("alice", "k2", q2, "bob", 0); !errors.Is(err, ErrSingleLimit) {
		t.Fatalf("want ErrSingleLimit, got %v", err)
	}
	// 恰等日限额（1000 + 1000 = 2000）。
	q3 := mustQuote(t, s, "alice", 1000, 1_000_000, 0)
	mustSubmit(t, s, "alice", "k3", q3, "bob", 0)
	// 超日限额一单位。
	q4 := mustQuote(t, s, "alice", 1, 1_000_000, 0)
	if _, err := s.Submit("alice", "k4", q4, "bob", 0); !errors.Is(err, ErrDayLimit) {
		t.Fatalf("want ErrDayLimit, got %v", err)
	}
	// 恰等年度额度：用日限额更宽的独立系统隔离年限额判定。
	cfg := testConfig()
	cfg.SingleLimit, cfg.DayLimit, cfg.YearLimit = 5000, 6000, 5000
	s2, err := NewSystem(cfg)
	if err != nil {
		t.Fatalf("NewSystem: %v", err)
	}
	for i := 0; i < 5; i++ {
		q := mustQuote(t, s2, "alice", 1000, 1_000_000, 0)
		mustSubmit(t, s2, "alice", string(rune('a'+i)), q, "bob", 0)
	}
	if u := usage(t, s2, "alice", 0); u.RollingYearUsed != 5000 {
		t.Fatalf("want rolling 5000, got %+v", u)
	}
	// 超年度额度一单位。
	q8 := mustQuote(t, s2, "alice", 1, 1_000_000, 0)
	if _, err := s2.Submit("alice", "k8", q8, "bob", 0); !errors.Is(err, ErrYearLimit) {
		t.Fatalf("want ErrYearLimit, got %v", err)
	}
}

// 审核逾期：恰第 R 秒批准成功，第 R+1 秒已逾期失败。
func TestReviewTimeoutBoundary(t *testing.T) {
	s := newTestSystem(t) // R=100，阈值 500
	// 恰第 R 秒批准成功。
	q1 := mustQuote(t, s, "alice", 500, 1_000_000, 1000)
	r1 := mustSubmit(t, s, "alice", "k1", q1, "bob", 1000)
	if r1.Status != StatusPendingReview {
		t.Fatalf("want pending, got %v", r1.Status)
	}
	if err := s.Approve(r1.RemittanceID, 1100); err != nil {
		t.Fatalf("approve at exact R: %v", err)
	}
	v, err := s.GetRemittance(r1.RemittanceID, 1100)
	if err != nil || v.Status != StatusSucceeded {
		t.Fatalf("want succeeded, got %v err=%v", v.Status, err)
	}

	// 第 R+1 秒批准：已逾期失败，报状态不允许；占用已释放。
	q2 := mustQuote(t, s, "alice", 500, 1_000_000, 1200)
	r2 := mustSubmit(t, s, "alice", "k2", q2, "bob", 1200)
	before := usage(t, s, "alice", 1200)
	if before.RollingYearUsed != 1000 {
		t.Fatalf("want rolling 1000, got %+v", before)
	}
	if err := s.Approve(r2.RemittanceID, 1301); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("want ErrInvalidState, got %v", err)
	}
	v2, _ := s.GetRemittance(r2.RemittanceID, 1301)
	if v2.Status != StatusFailed {
		t.Fatalf("want failed, got %v", v2.Status)
	}
	after := usage(t, s, "alice", 1301)
	if after.RollingYearUsed != 500 || after.DayUsed != 500 {
		t.Fatalf("want usage 500/500 after timeout release, got %+v", after)
	}
}

// 失败释放归还到原占用日序号，而非失败发生日。
func TestReleaseAttributionCrossDay(t *testing.T) {
	cfg := testConfig()
	cfg.SingleLimit, cfg.DayLimit, cfg.YearLimit = 1000, 100, 10000
	cfg.ReviewThreshold = 1
	cfg.ReviewTimeout = 100 * 86400 // 足够长，保证跨日时仍在审
	s, err := NewSystem(cfg)
	if err != nil {
		t.Fatalf("NewSystem: %v", err)
	}
	// 第 0 日：A 占用 100（待审核），占满当日限额。
	qA := mustQuote(t, s, "alice", 100, 1_000_000, 0)
	rA := mustSubmit(t, s, "alice", "kA", qA, "bob", 0)
	// 第 10 日：B 占用 100（待审核），占满第 10 日限额。
	day10 := int64(10 * 86400)
	qB := mustQuote(t, s, "alice", 100, 1_000_000, day10)
	rB := mustSubmit(t, s, "alice", "kB", qB, "bob", day10)
	if u := usage(t, s, "alice", day10); u.DayUsed != 100 || u.RollingYearUsed != 200 {
		t.Fatalf("want 100/200, got %+v", u)
	}
	// 第 10 日拒绝 A：释放须归还第 0 日，第 10 日占用仍为 100（仍满）。
	if err := s.Reject(rA.RemittanceID, day10); err != nil {
		t.Fatalf("reject A: %v", err)
	}
	if u := usage(t, s, "alice", day10); u.DayUsed != 100 || u.RollingYearUsed != 100 {
		t.Fatalf("after release want 100/100, got %+v", u)
	}
	qC := mustQuote(t, s, "alice", 1, 1_000_000, day10)
	if _, err := s.Submit("alice", "kC", qC, "bob", day10); !errors.Is(err, ErrDayLimit) {
		t.Fatalf("release misattributed to day10: want ErrDayLimit, got %v", err)
	}
	_ = rB
}

// 跨年度后失败：原占用日已滑出滚动窗口，释放不影响当前占用（不为负）。
func TestReleaseAttributionCrossYear(t *testing.T) {
	cfg := testConfig()
	cfg.SingleLimit, cfg.DayLimit, cfg.YearLimit = 1000, 1000, 150
	cfg.ReviewThreshold = 1
	cfg.ReviewTimeout = 1000 * 86400 // 足够长，保证跨年度时仍在审
	s, err := NewSystem(cfg)
	if err != nil {
		t.Fatalf("NewSystem: %v", err)
	}
	// 第 0 日：A 占用 100（待审核）。
	qA := mustQuote(t, s, "alice", 100, 1_000_000, 0)
	rA := mustSubmit(t, s, "alice", "kA", qA, "bob", 0)
	// 第 400 日：A 的占用已滑出滚动年度窗口，可重新占用 150（恰等年限）。
	day400 := int64(400 * 86400)
	if u := usage(t, s, "alice", day400); u.RollingYearUsed != 0 {
		t.Fatalf("want rolling 0 after window slide, got %+v", u)
	}
	qB := mustQuote(t, s, "alice", 150, 1_000_000, day400)
	mustSubmit(t, s, "alice", "kB", qB, "bob", day400)
	// 第 400 日拒绝 A：其占用早已滑出窗口，滚动占用仍为 150。
	if err := s.Reject(rA.RemittanceID, day400); err != nil {
		t.Fatalf("reject A: %v", err)
	}
	if u := usage(t, s, "alice", day400); u.RollingYearUsed != 150 || u.DayUsed != 150 {
		t.Fatalf("want 150/150, got %+v", u)
	}
}

// 审核逾期发生在跨日之后：释放同样归还原占用日。
func TestTimeoutReleaseAttribution(t *testing.T) {
	cfg := testConfig()
	cfg.SingleLimit, cfg.DayLimit, cfg.YearLimit = 1000, 100, 10000
	cfg.ReviewThreshold = 1
	cfg.ReviewTimeout = 10
	s, err := NewSystem(cfg)
	if err != nil {
		t.Fatalf("NewSystem: %v", err)
	}
	// 第 0 日提交 A（占用 100，待审核，deadline=10）。
	qA := mustQuote(t, s, "alice", 100, 1_000_000, 0)
	rA := mustSubmit(t, s, "alice", "kA", qA, "bob", 0)
	// 第 5 日提交 B（占用 100），此时 A 已逾期失败，占用归还第 0 日。
	day5 := int64(5 * 86400)
	qB := mustQuote(t, s, "alice", 100, 1_000_000, day5)
	mustSubmit(t, s, "alice", "kB", qB, "bob", day5)
	if u := usage(t, s, "alice", day5); u.DayUsed != 100 || u.RollingYearUsed != 100 {
		t.Fatalf("want 100/100 (A released at day0), got %+v", u)
	}
	v, _ := s.GetRemittance(rA.RemittanceID, day5)
	if v.Status != StatusFailed {
		t.Fatalf("want A failed, got %v", v.Status)
	}
}

// 制裁命中：拒绝且不消耗报价与幂等键。
func TestSanctionedNoConsumption(t *testing.T) {
	s := newTestSystem(t)
	s.AddSanctionedPayee("evil")
	q := mustQuote(t, s, "alice", 100, 1_000_000, 0)
	if _, err := s.Submit("alice", "k1", q, "evil", 0); !errors.Is(err, ErrSanctioned) {
		t.Fatalf("want ErrSanctioned, got %v", err)
	}
	// 报价未消耗：同一报价换收款人可成功。
	res := mustSubmit(t, s, "alice", "k1", q, "bob", 0) // 幂等键也未被记录，可复用
	if res.Status != StatusSucceeded {
		t.Fatalf("want succeeded, got %v", res.Status)
	}
	// 移出名单后可正常提交。
	s.RemoveSanctionedPayee("evil")
	q2 := mustQuote(t, s, "alice", 100, 1_000_000, 0)
	mustSubmit(t, s, "alice", "k2", q2, "evil", 0)
}

// 幂等：同键同参返回原结果且不再占用；同键不同参报冲突。
func TestIdempotency(t *testing.T) {
	s := newTestSystem(t)
	q := mustQuote(t, s, "alice", 100, 1_000_000, 0)
	res := mustSubmit(t, s, "alice", "k1", q, "bob", 0)
	// 同键同参重放：返回原结果，占用不变。
	replay, err := s.Submit("alice", "k1", q, "bob", 1)
	if err != nil || replay != res {
		t.Fatalf("replay want %+v, got %+v err=%v", res, replay, err)
	}
	if u := usage(t, s, "alice", 1); u.DayUsed != 100 {
		t.Fatalf("replay must not re-hold, got %+v", u)
	}
	// 同键不同参：冲突。
	q2 := mustQuote(t, s, "alice", 100, 1_000_000, 1)
	if _, err := s.Submit("alice", "k1", q2, "bob", 1); !errors.Is(err, ErrIdemConflict) {
		t.Fatalf("want ErrIdemConflict, got %v", err)
	}
	if _, err := s.Submit("alice", "k1", q, "carol", 1); !errors.Is(err, ErrIdemConflict) {
		t.Fatalf("want ErrIdemConflict, got %v", err)
	}
	// 重放发生在报价已消耗之后仍返回原结果（幂等检查先于报价检查）。
	replay2, err := s.Submit("alice", "k1", q, "bob", 2)
	if err != nil || replay2 != res {
		t.Fatalf("replay2 want %+v, got %+v err=%v", res, replay2, err)
	}
}

// 被拒绝的提交不记录幂等键、不消耗报价、不改变占用与时钟。
func TestRejectedSubmitLeavesNoTrace(t *testing.T) {
	s := newTestSystem(t)
	q := mustQuote(t, s, "alice", 100, 1_000_000, 100)
	// 超单笔限额被拒绝。
	qBig := mustQuote(t, s, "alice", 2000, 1_000_000, 100)
	if _, err := s.Submit("alice", "kBig", qBig, "bob", 100); !errors.Is(err, ErrSingleLimit) {
		t.Fatalf("want ErrSingleLimit, got %v", err)
	}
	// 幂等键未记录：同键换参数不冲突。
	mustSubmit(t, s, "alice", "kBig", q, "bob", 100)
	// 超限报价未消耗？不——被拒绝提交不消耗报价，qBig 仍可用但永远超限；
	// 此处验证占用与时钟未被拒绝操作改变。
	if u := usage(t, s, "alice", 100); u.DayUsed != 100 {
		t.Fatalf("want dayUsed 100, got %+v", u)
	}
	// 时钟未推进：now=50（小于被拒操作的 100？不，100 已被接受）——
	// 用另一个系统验证：接受于 now=100，拒绝于 now=200，随后 now=150 仍被接受。
	s2 := newTestSystem(t)
	qa := mustQuote(t, s2, "alice", 100, 1_000_000, 100)
	mustSubmit(t, s2, "alice", "k1", qa, "bob", 100)
	qb := mustQuote(t, s2, "alice", 2000, 1_000_000, 150)
	if _, err := s2.Submit("alice", "k2", qb, "bob", 200); !errors.Is(err, ErrSingleLimit) {
		t.Fatalf("want ErrSingleLimit, got %v", err)
	}
	// 若拒绝操作推进了时钟，now=150 将报时钟回退。
	qc := mustQuote(t, s2, "alice", 100, 1_000_000, 150)
	mustSubmit(t, s2, "alice", "k3", qc, "bob", 150)
}

// 时钟回退：小于上一次被接受操作的 now 报错；恰等可接受。
func TestClockRegression(t *testing.T) {
	s := newTestSystem(t)
	q := mustQuote(t, s, "alice", 100, 1_000_000, 100)
	if _, err := s.RequestQuote("alice", 100, 1_000_000, 99); !errors.Is(err, ErrClockRegression) {
		t.Fatalf("want ErrClockRegression, got %v", err)
	}
	// 恰等上一次 now 可接受。
	mustSubmit(t, s, "alice", "k1", q, "bob", 100)
	if _, err := s.QueryUsage("alice", 99); !errors.Is(err, ErrClockRegression) {
		t.Fatalf("want ErrClockRegression, got %v", err)
	}
	if err := s.Approve("R-1", 50); !errors.Is(err, ErrClockRegression) {
		t.Fatalf("want ErrClockRegression, got %v", err)
	}
	// 时钟回退的被拒操作不改变状态：后续正常操作不受影响。
	if u := usage(t, s, "alice", 100); u.DayUsed != 100 {
		t.Fatalf("want dayUsed 100, got %+v", u)
	}
}

// 错误优先级：只报第一个。
func TestErrorPriority(t *testing.T) {
	s := newTestSystem(t)
	s.AddSanctionedPayee("evil")
	q := mustQuote(t, s, "alice", 2000, 1_000_000, 0) // 超单笔
	qExpired := mustQuote(t, s, "alice", 100, 1_000_000, 0)
	mustSubmit(t, s, "alice", "k1", qExpired, "bob", 0) // 消耗 qExpired？不——qExpired 被消耗
	// 参数非法 > 时钟回退：空键 + 回退。
	if _, err := s.Submit("alice", "", q, "bob", 0-1); !errors.Is(err, ErrInvalidParams) {
		t.Fatalf("want ErrInvalidParams, got %v", err)
	}
	// 时钟回退 > 制裁：now=-1 参数非法已覆盖；改用推进时钟后回退+制裁。
	q2 := mustQuote(t, s, "alice", 100, 1_000_000, 10)
	if _, err := s.Submit("alice", "k2", q2, "evil", 5); !errors.Is(err, ErrClockRegression) {
		t.Fatalf("want ErrClockRegression, got %v", err)
	}
	// 制裁 > 幂等冲突：k1 已记录（bob），用 k1+evil 不同参。
	if _, err := s.Submit("alice", "k1", q2, "evil", 10); !errors.Is(err, ErrSanctioned) {
		t.Fatalf("want ErrSanctioned, got %v", err)
	}
	// 幂等冲突 > 报价不存在：k1 + 不存在的报价 + 不同收款人。
	if _, err := s.Submit("alice", "k1", "Q-999", "carol", 10); !errors.Is(err, ErrIdemConflict) {
		t.Fatalf("want ErrIdemConflict, got %v", err)
	}
	// 报价不存在 > 报价已过期（不存在优先，无法同现，分别验证）。
	if _, err := s.Submit("alice", "k3", "Q-999", "bob", 10); !errors.Is(err, ErrQuoteNotFound) {
		t.Fatalf("want ErrQuoteNotFound, got %v", err)
	}
	// 报价已消耗。
	if _, err := s.Submit("alice", "k4", qExpired, "bob", 10); !errors.Is(err, ErrQuoteConsumed) {
		t.Fatalf("want ErrQuoteConsumed, got %v", err)
	}
	// 报价已过期 > 单笔限额：q 既过期（now=200 > 0+100）又超单笔（2000>1000）。
	if _, err := s.Submit("alice", "k5", q, "bob", 200); !errors.Is(err, ErrQuoteExpired) {
		t.Fatalf("want ErrQuoteExpired, got %v", err)
	}
	// 单笔限额 > 日限额：q 未过期时（now=50）超单笔。
	if _, err := s.Submit("alice", "k6", q, "bob", 50); !errors.Is(err, ErrSingleLimit) {
		t.Fatalf("want ErrSingleLimit, got %v", err)
	}
	// 日限额 > 年度额度：日限额先于年限额报。
	cfg := testConfig()
	cfg.SingleLimit, cfg.DayLimit, cfg.YearLimit = 1000, 100, 50
	s2, _ := NewSystem(cfg)
	qx := mustQuote(t, s2, "alice", 101, 1_000_000, 0)
	if _, err := s2.Submit("alice", "k1", qx, "bob", 0); !errors.Is(err, ErrDayLimit) {
		t.Fatalf("want ErrDayLimit, got %v", err)
	}
}

// 审核阈值边界：恰等阈值进入待审核，低一单位直接出款。
func TestReviewThresholdBoundary(t *testing.T) {
	s := newTestSystem(t) // 阈值 500
	q1 := mustQuote(t, s, "alice", 500, 1_000_000, 0)
	if res := mustSubmit(t, s, "alice", "k1", q1, "bob", 0); res.Status != StatusPendingReview {
		t.Fatalf("target==threshold want pending, got %v", res.Status)
	}
	q2 := mustQuote(t, s, "alice", 499, 1_000_000, 0)
	if res := mustSubmit(t, s, "alice", "k2", q2, "bob", 0); res.Status != StatusSucceeded {
		t.Fatalf("target<threshold want succeeded, got %v", res.Status)
	}
}

// 报价至多使用一次；报价仅属申请人。
func TestQuoteConsumptionAndOwnership(t *testing.T) {
	s := newTestSystem(t)
	q := mustQuote(t, s, "alice", 100, 1_000_000, 0)
	mustSubmit(t, s, "alice", "k1", q, "bob", 0)
	if _, err := s.Submit("alice", "k2", q, "bob", 0); !errors.Is(err, ErrQuoteConsumed) {
		t.Fatalf("want ErrQuoteConsumed, got %v", err)
	}
	// 他人报价：不存在。
	q2 := mustQuote(t, s, "alice", 100, 1_000_000, 0)
	if _, err := s.Submit("carol", "k3", q2, "bob", 0); !errors.Is(err, ErrQuoteNotFound) {
		t.Fatalf("want ErrQuoteNotFound, got %v", err)
	}
}

// 撤回仅针对待审核汇款，等同于拒绝。
func TestWithdraw(t *testing.T) {
	s := newTestSystem(t)
	// 待审核可撤回，释放占用。
	q1 := mustQuote(t, s, "alice", 500, 1_000_000, 0)
	r1 := mustSubmit(t, s, "alice", "k1", q1, "bob", 0)
	if err := s.Withdraw(r1.RemittanceID, 10); err != nil {
		t.Fatalf("withdraw pending: %v", err)
	}
	if u := usage(t, s, "alice", 10); u.DayUsed != 0 {
		t.Fatalf("want dayUsed 0 after withdraw, got %+v", u)
	}
	// 已失败不可再操作。
	if err := s.Withdraw(r1.RemittanceID, 10); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("want ErrInvalidState, got %v", err)
	}
	// 直接出款（低额）不可撤回。
	q2 := mustQuote(t, s, "alice", 100, 1_000_000, 10)
	r2 := mustSubmit(t, s, "alice", "k2", q2, "bob", 10)
	if err := s.Withdraw(r2.RemittanceID, 10); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("want ErrInvalidState, got %v", err)
	}
	// 不存在。
	if err := s.Withdraw("R-999", 10); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	// 逾期后撤回：状态不允许。
	q3 := mustQuote(t, s, "alice", 500, 1_000_000, 10)
	r3 := mustSubmit(t, s, "alice", "k3", q3, "bob", 10)
	if err := s.Withdraw(r3.RemittanceID, 10+101); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("want ErrInvalidState after expiry, got %v", err)
	}
	v, _ := s.GetRemittance(r3.RemittanceID, 111)
	if v.Status != StatusFailed {
		t.Fatalf("want failed, got %v", v.Status)
	}
}

// 审核/撤回错误优先级：参数非法 > 时钟回退 > 不存在 > 状态不允许。
func TestReviewErrorPriority(t *testing.T) {
	s := newTestSystem(t)
	q := mustQuote(t, s, "alice", 500, 1_000_000, 10)
	r := mustSubmit(t, s, "alice", "k1", q, "bob", 10)
	if err := s.Approve("", 5); !errors.Is(err, ErrInvalidParams) {
		t.Fatalf("want ErrInvalidParams, got %v", err)
	}
	if err := s.Approve(r.RemittanceID, 5); !errors.Is(err, ErrClockRegression) {
		t.Fatalf("want ErrClockRegression, got %v", err)
	}
	if err := s.Approve("R-999", 10); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if err := s.Reject(r.RemittanceID, 20); err != nil {
		t.Fatalf("reject: %v", err)
	}
	if err := s.Approve(r.RemittanceID, 20); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("want ErrInvalidState, got %v", err)
	}
}

// 大数乘积：128 位中间计算，溢出必报单笔限额。
func TestMulDivOverflow(t *testing.T) {
	cases := []struct {
		a, b        int64
		floor, ceil int64
		overflow    bool
	}{
		{3, 1_500_000, 4, 5, false},
		{2, 1_000_000, 2, 2, false},
		{1, 1, 0, 1, false},
		{1 << 62, 1 << 30, 0, 0, true},                          // 乘积约 2^92，商超 int64
		{1 << 33, 1 << 30, 9223372036854, 9223372036855, false}, // 2^63/1e6 可表示
	}
	for _, c := range cases {
		f, cl, ov := mulDiv(c.a, c.b)
		if f != c.floor || cl != c.ceil || ov != c.overflow {
			t.Errorf("mulDiv(%d,%d) = (%d,%d,%v), want (%d,%d,%v)",
				c.a, c.b, f, cl, ov, c.floor, c.ceil, c.overflow)
		}
	}
	// 超大金额提交：占用额不可表示，按超单笔限额拒绝。
	s := newTestSystem(t)
	q := mustQuote(t, s, "alice", 1<<62, 1<<30, 0)
	if _, err := s.Submit("alice", "k1", q, "bob", 0); !errors.Is(err, ErrSingleLimit) {
		t.Fatalf("want ErrSingleLimit, got %v", err)
	}
}

// 并发：大量 goroutine 同时提交，限额不变式必须保持，
// 且最终占用恰等于全部被接受提交的占用之和（可串行化）。
func TestConcurrentLinearizable(t *testing.T) {
	cfg := Config{
		SingleLimit:     100,
		DayLimit:        1000,
		YearLimit:       1000,
		QuoteTTL:        100,
		ReviewThreshold: 10_000, // 全部直接出款
		ReviewTimeout:   100,
	}
	s, err := NewSystem(cfg)
	if err != nil {
		t.Fatalf("NewSystem: %v", err)
	}
	var successes atomic.Int64
	var wg sync.WaitGroup
	for g := 0; g < 50; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				q, err := s.RequestQuote("alice", 100, 1_000_000, 0)
				if err != nil {
					continue
				}
				if _, err := s.Submit("alice", fmt.Sprintf("g%d-%d", g, i), q, "bob", 0); err == nil {
					successes.Add(1)
				}
			}
		}(g)
	}
	wg.Wait()
	if got := successes.Load(); got > 10 {
		t.Fatalf("day limit violated under concurrency: %d successes", got)
	}
	u, err := s.QueryUsage("alice", 0)
	if err != nil {
		t.Fatalf("QueryUsage: %v", err)
	}
	if u.DayUsed != successes.Load()*100 || u.RollingYearUsed != u.DayUsed {
		t.Fatalf("usage %v inconsistent with %d successes", u, successes.Load())
	}
	if u.DayUsed > cfg.DayLimit {
		t.Fatalf("day limit exceeded: %v", u)
	}
}
