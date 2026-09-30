package lease

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

var t0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func at(sec int) time.Time { return t0.Add(time.Duration(sec) * time.Second) }

func intPtr(v int) *int { return &v }

func newServer(lo, hi, lease, hold, quar int) *Server {
	return NewServer(Config{
		Lo: lo, Hi: hi,
		Lease:      time.Duration(lease) * time.Second,
		Hold:       time.Duration(hold) * time.Second,
		Quarantine: time.Duration(quar) * time.Second,
	})
}

// logDiscover 执行发现并打印输入、输出与判定依据。
func logDiscover(t *testing.T, s *Server, client string, reqAddr *int, now time.Time, why string) (int, error) {
	t.Helper()
	req := "nil"
	if reqAddr != nil {
		req = fmt.Sprint(*reqAddr)
	}
	addr, err := s.Discover(client, reqAddr, now)
	t.Logf("Discover 输入=(客户端=%q 请求地址=%s 时刻=%s) 输出=(地址=%d 错误=%v) 判定依据=%s",
		client, req, now.Format("15:04:05"), addr, err, why)
	return addr, err
}

// logConfirm 执行确认并打印输入、输出与判定依据。
func logConfirm(t *testing.T, s *Server, client string, addr int, now time.Time, why string) error {
	t.Helper()
	err := s.Confirm(client, addr, now)
	t.Logf("Confirm 输入=(客户端=%q 地址=%d 时刻=%s) 输出=(错误=%v) 判定依据=%s",
		client, addr, now.Format("15:04:05"), err, why)
	return err
}

// logRelease 执行释放并打印输入、输出与判定依据。
func logRelease(t *testing.T, s *Server, client string, addr int, now time.Time, why string) error {
	t.Helper()
	err := s.Release(client, addr, now)
	t.Logf("Release 输入=(客户端=%q 地址=%d 时刻=%s) 输出=(错误=%v) 判定依据=%s",
		client, addr, now.Format("15:04:05"), err, why)
	return err
}

// logDecline 执行拒收并打印输入、输出与判定依据。
func logDecline(t *testing.T, s *Server, client string, addr int, now time.Time, why string) error {
	t.Helper()
	err := s.Decline(client, addr, now)
	t.Logf("Decline 输入=(客户端=%q 地址=%d 时刻=%s) 输出=(错误=%v) 判定依据=%s",
		client, addr, now.Format("15:04:05"), err, why)
	return err
}

func mustDiscover(t *testing.T, s *Server, client string, reqAddr *int, now time.Time, why string) int {
	t.Helper()
	addr, err := logDiscover(t, s, client, reqAddr, now, why)
	if err != nil {
		t.Fatalf("Discover(%q) 意外失败: %v", client, err)
	}
	return addr
}

func mustConfirm(t *testing.T, s *Server, client string, addr int, now time.Time, why string) {
	t.Helper()
	if err := logConfirm(t, s, client, addr, now, why); err != nil {
		t.Fatalf("Confirm(%q, %d) 意外失败: %v", client, addr, err)
	}
}

// TestDiscoverHistoryBeforeRequest 验证客户端回到最近一次持有过的
// 历史地址优先于请求地址（规则 2 优先于规则 3）。
func TestDiscoverHistoryBeforeRequest(t *testing.T) {
	s := newServer(10, 11, 100, 10, 5)

	addr := mustDiscover(t, s, "c1", nil, at(0), "规则4：两地址均未有过租约，并列取小者 10")
	if addr != 10 {
		t.Fatalf("首次发现应得 10，实际 %d", addr)
	}
	mustConfirm(t, s, "c1", 10, at(1), "c1 在 10 上有有效暂留，确认成功")
	if err := logRelease(t, s, "c1", 10, at(2), "c1 是 10 的持有者，释放成功，终止时刻记为 t=2"); err != nil {
		t.Fatalf("Release 意外失败: %v", err)
	}

	got := mustDiscover(t, s, "c1", intPtr(11), at(3),
		"规则2：c1 最近持有过的 10 空闲，优先于请求地址 11")
	if got != 10 {
		t.Fatalf("应回到历史地址 10，实际 %d", got)
	}
}

// TestDiscoverEarliestTermination 验证按租约终止时刻最早选址，
// 且区分「租约终点」与「释放时刻」两种终止时刻。
func TestDiscoverEarliestTermination(t *testing.T) {
	s := newServer(10, 11, 10, 10, 5)

	mustDiscover(t, s, "a", nil, at(0), "规则4：均未租约过，取小者 10")
	mustConfirm(t, s, "a", 10, at(0), "a 持有 10 的暂留，确认成功，租约 [0,10)")
	mustDiscover(t, s, "b", nil, at(0), "规则4：10 已租约，仅剩 11")
	mustConfirm(t, s, "b", 11, at(0), "b 持有 11 的暂留，确认成功，租约 [0,10)")

	if err := logRelease(t, s, "b", 11, at(5), "释放：11 的终止时刻记为释放时刻 t=5"); err != nil {
		t.Fatalf("Release 意外失败: %v", err)
	}
	// t=10 时 10 的租约恰在终点失效，终止时刻取租约终点 t=10。
	got := mustDiscover(t, s, "c", nil, at(10),
		"规则4：10 终止于 t=10（租约终点），11 终止于 t=5（释放时刻），t=5 更早，选 11")
	if got != 11 {
		t.Fatalf("应选终止时刻更早的 11，实际 %d", got)
	}
}

// TestDiscoverNeverLeasedFirst 验证从未有过租约的地址视为终止时刻最早。
func TestDiscoverNeverLeasedFirst(t *testing.T) {
	s := newServer(10, 11, 10, 10, 5)

	mustDiscover(t, s, "a", nil, at(0), "规则4：均未租约过，取小者 10")
	mustConfirm(t, s, "a", 10, at(0), "确认成功，租约 [0,10)")
	if err := logRelease(t, s, "a", 10, at(3), "释放：10 的终止时刻记为 t=3"); err != nil {
		t.Fatalf("Release 意外失败: %v", err)
	}

	got := mustDiscover(t, s, "c", nil, at(4),
		"规则4：10 终止于 t=3，11 从未有过租约视为最早，选 11")
	if got != 11 {
		t.Fatalf("应选从未租约过的 11，实际 %d", got)
	}
}

// TestLeaseExpiresExactlyAtEnd 验证租约为左闭右开区间，
// 恰在终点时刻失效。
func TestLeaseExpiresExactlyAtEnd(t *testing.T) {
	s := newServer(10, 10, 10, 10, 5)

	mustDiscover(t, s, "c1", nil, at(0), "规则4：唯一地址 10 空闲")
	mustConfirm(t, s, "c1", 10, at(0), "确认成功，租约 [0,10)")

	if _, err := logDiscover(t, s, "c2", nil, at(9),
		"t=9 租约 [0,10) 仍有效，地址不空闲，池已耗尽"); !errors.Is(err, ErrPoolExhausted) {
		t.Fatalf("t=9 应报池已耗尽，实际 %v", err)
	}
	got := mustDiscover(t, s, "c2", nil, at(10),
		"t=10 租约恰在终点失效（左闭右开），地址空闲")
	if got != 10 {
		t.Fatalf("t=10 应取得 10，实际 %d", got)
	}
	mustConfirm(t, s, "c2", 10, at(10), "c2 持有有效暂留，确认成功")
}

// TestRenewDoesNotStack 验证续租从现在起算，不叠加剩余租期。
func TestRenewDoesNotStack(t *testing.T) {
	s := newServer(10, 10, 10, 10, 5)

	mustDiscover(t, s, "c1", nil, at(0), "规则4：唯一地址空闲")
	mustConfirm(t, s, "c1", 10, at(0), "确认成功，租约 [0,10)")
	mustConfirm(t, s, "c1", 10, at(3), "续租：c1 是持有者，租约终点重算为 3+10=13，不叠加剩余")

	if _, err := logDiscover(t, s, "c2", nil, at(12),
		"t=12 续租后租约 [3,13) 仍有效，池已耗尽"); !errors.Is(err, ErrPoolExhausted) {
		t.Fatalf("t=12 应报池已耗尽，实际 %v", err)
	}
	got := mustDiscover(t, s, "c2", nil, at(13),
		"t=13 续租租约 [3,13) 恰失效；若叠加剩余则终点为 20，此处可区分")
	if got != 10 {
		t.Fatalf("t=13 应取得 10，实际 %d", got)
	}
}

// TestDeclineQuarantine 验证拒收后地址进入隔离期，隔离期内不可选，
// 隔离期满后可选；同时验证暂留者拒收不改变租约终止时刻。
func TestDeclineQuarantine(t *testing.T) {
	s := newServer(10, 11, 10, 10, 2)

	mustDiscover(t, s, "a", nil, at(0), "规则4：均未租约过，取小者 10")
	mustConfirm(t, s, "a", 10, at(0), "确认成功，租约 [0,10)")
	if err := logRelease(t, s, "a", 10, at(3), "释放：10 的终止时刻记为 t=3"); err != nil {
		t.Fatalf("Release 意外失败: %v", err)
	}
	mustDiscover(t, s, "b", nil, at(3), "规则4：11 从未租约过，优先于终止于 t=3 的 10")
	mustConfirm(t, s, "b", 11, at(3), "确认成功，租约 [3,13)")

	mustDiscover(t, s, "c1", nil, at(4), "规则4：10 空闲（终止 t=3），11 有有效租约，选 10")
	if err := logRelease(t, s, "b", 11, at(4), "释放：11 的终止时刻记为 t=4"); err != nil {
		t.Fatalf("Release 意外失败: %v", err)
	}
	if err := logDecline(t, s, "c1", 10, at(5),
		"暂留者拒收：清除暂留，10 进入隔离期 [5,7)，终止时刻保持 t=3 不变"); err != nil {
		t.Fatalf("Decline 意外失败: %v", err)
	}

	got := mustDiscover(t, s, "c2", nil, at(6),
		"10 在隔离期 [5,7) 内不可选，11 空闲（终止时刻 t=4），选 11")
	if got != 11 {
		t.Fatalf("t=6 应选 11，实际 %d", got)
	}
	got = mustDiscover(t, s, "c3", nil, at(7),
		"隔离期 [5,7) 已满：10 终止时刻 t=3（拒收未改变），早于 11 的 t=4，选 10；"+
			"若暂留者拒收错误地把终止时刻改为 t=5，则会选 11")
	if got != 10 {
		t.Fatalf("应选 10，实际 %d", got)
	}
}

// TestDeclineByLeaseHolder 验证持有者拒收：租约清除、地址隔离、
// 终止时刻记为拒收时刻。
func TestDeclineByLeaseHolder(t *testing.T) {
	s := newServer(10, 11, 10, 10, 3)

	mustDiscover(t, s, "a", nil, at(0), "规则4：取小者 10")
	mustConfirm(t, s, "a", 10, at(0), "确认成功，租约 [0,10)")
	if err := logDecline(t, s, "a", 10, at(2),
		"持有者拒收：清除租约，10 隔离 [2,5)，终止时刻记为拒收时刻 t=2"); err != nil {
		t.Fatalf("Decline 意外失败: %v", err)
	}

	got := mustDiscover(t, s, "b", nil, at(3), "10 在隔离期 [2,5) 内，只能选 11")
	if got != 11 {
		t.Fatalf("应选 11，实际 %d", got)
	}
	mustConfirm(t, s, "b", 11, at(3), "确认成功")
	if err := logRelease(t, s, "b", 11, at(4), "释放：11 的终止时刻记为 t=4"); err != nil {
		t.Fatalf("Release 意外失败: %v", err)
	}

	got = mustDiscover(t, s, "c", nil, at(5),
		"隔离期满：10 终止于拒收时刻 t=2，早于 11 的 t=4，选 10")
	if got != 10 {
		t.Fatalf("应选 10，实际 %d", got)
	}
}

// TestHoldExpiryReusable 验证暂留过期后可被他人取得。
func TestHoldExpiryReusable(t *testing.T) {
	s := newServer(10, 10, 10, 5, 5)

	mustDiscover(t, s, "c1", nil, at(0), "规则4：唯一地址空闲，暂留 [0,5)")
	if _, err := logDiscover(t, s, "c2", nil, at(4),
		"t=4 暂留 [0,5) 仍有效，池已耗尽"); !errors.Is(err, ErrPoolExhausted) {
		t.Fatalf("t=4 应报池已耗尽，实际 %v", err)
	}
	got := mustDiscover(t, s, "c2", nil, at(5),
		"t=5 暂留恰在终点失效（左闭右开），c2 可取得")
	if got != 10 {
		t.Fatalf("t=5 应取得 10，实际 %d", got)
	}
	mustConfirm(t, s, "c2", 10, at(5), "c2 持有有效暂留，确认成功")
	if err := logConfirm(t, s, "c1", 10, at(6),
		"c1 的暂留已过期且地址被 c2 持有，报地址被他人持有"); !errors.Is(err, ErrAddrBusy) {
		t.Fatalf("c1 确认应报 ErrAddrBusy，实际 %v", err)
	}
}

// TestDiscoverExistingLeaseOrHold 验证规则 1：已有有效租约或暂留的
// 地址优先，且已有暂留沿用、不刷新起点。
func TestDiscoverExistingLeaseOrHold(t *testing.T) {
	s := newServer(10, 11, 10, 5, 5)

	mustDiscover(t, s, "c1", nil, at(0), "规则4：取小者 10，暂留 [0,5)")
	got := mustDiscover(t, s, "c1", intPtr(11), at(3),
		"规则1：c1 在 10 上有有效暂留，沿用（不刷新起点），忽略请求地址 11")
	if got != 10 {
		t.Fatalf("应沿用暂留地址 10，实际 %d", got)
	}
	// 若暂留起点被刷新到 t=3，则暂留终点为 8，t=6 时 10 仍被 c1 暂留。
	got = mustDiscover(t, s, "c2", nil, at(6),
		"暂留 [0,5) 已过期（未刷新），10 空闲，c2 取得")
	if got != 10 {
		t.Fatalf("t=6 c2 应取得 10，实际 %d", got)
	}

	mustDiscover(t, s, "c3", nil, at(6), "规则4：10 被 c2 暂留，选 11")
	mustConfirm(t, s, "c3", 11, at(6), "确认成功，租约 [6,16)")
	got = mustDiscover(t, s, "c3", nil, at(8),
		"规则1：c3 已有有效租约（租约优先于暂留），直接返回 11")
	if got != 11 {
		t.Fatalf("应返回租约地址 11，实际 %d", got)
	}
}

// TestDiscoverRejections 验证发现的拒绝顺序，且被拒绝的操作不改变状态。
func TestDiscoverRejections(t *testing.T) {
	s := newServer(10, 11, 10, 10, 5)

	mustDiscover(t, s, "c1", nil, at(10), "规则4：取小者 10，暂留 [10,20)")

	if _, err := logDiscover(t, s, "", intPtr(99), at(9),
		"时刻 9 早于已见时刻 10，报时钟回拨（优先于客户端为空与地址不在池内）"); !errors.Is(err, ErrClockRollback) {
		t.Fatalf("应报时钟回拨，实际 %v", err)
	}
	if _, err := logDiscover(t, s, "", nil, at(10), "客户端为空"); !errors.Is(err, ErrEmptyClient) {
		t.Fatalf("应报客户端为空，实际 %v", err)
	}
	if _, err := logDiscover(t, s, "c2", intPtr(99), at(10),
		"请求地址 99 不在池 [10,11] 内"); !errors.Is(err, ErrAddrOutOfPool) {
		t.Fatalf("应报地址不在池内，实际 %v", err)
	}
	mustDiscover(t, s, "c2", nil, at(10), "规则4：10 被暂留，选 11")
	if _, err := logDiscover(t, s, "c3", nil, at(10),
		"10、11 均被暂留，无空闲地址，池已耗尽"); !errors.Is(err, ErrPoolExhausted) {
		t.Fatalf("应报池已耗尽，实际 %v", err)
	}

	// 被拒绝的操作不改变状态：c1 的暂留应仍然有效。
	got := mustDiscover(t, s, "c1", nil, at(11), "规则1：c1 的暂留未被任何拒绝操作影响，沿用 10")
	if got != 10 {
		t.Fatalf("应沿用暂留地址 10，实际 %d", got)
	}
}

// TestConfirmRejections 验证确认的拒绝顺序，且被拒绝的操作不改变状态。
func TestConfirmRejections(t *testing.T) {
	s := newServer(10, 11, 10, 10, 5)

	mustDiscover(t, s, "c1", nil, at(10), "规则4：取小者 10，暂留 [10,20)")

	if err := logConfirm(t, s, "c1", 99, at(9),
		"时钟回拨优先于地址不在池内"); !errors.Is(err, ErrClockRollback) {
		t.Fatalf("应报时钟回拨，实际 %v", err)
	}
	if err := logConfirm(t, s, "c1", 99, at(10), "地址 99 不在池内"); !errors.Is(err, ErrAddrOutOfPool) {
		t.Fatalf("应报地址不在池内，实际 %v", err)
	}
	if err := logConfirm(t, s, "c2", 11, at(10),
		"11 空闲，但 c2 在其上既无暂留也无租约"); !errors.Is(err, ErrNoHoldOrLease) {
		t.Fatalf("应报无有效暂留或租约，实际 %v", err)
	}
	mustDiscover(t, s, "c2", nil, at(10), "规则4：10 被暂留，选 11")
	if err := logConfirm(t, s, "c1", 11, at(10),
		"11 被 c2 暂留，报地址被他人持有或暂留"); !errors.Is(err, ErrAddrBusy) {
		t.Fatalf("应报地址被他人持有或暂留，实际 %v", err)
	}
	if err := logConfirm(t, s, "c3", 10, at(10),
		"10 被 c1 暂留，优先报地址被他人持有或暂留（而非无暂留或租约）"); !errors.Is(err, ErrAddrBusy) {
		t.Fatalf("应报地址被他人持有或暂留，实际 %v", err)
	}
	mustConfirm(t, s, "c1", 10, at(10), "c1 在 10 上有有效暂留，确认成功")

	// 被拒绝的确认不改变状态：c2 的暂留仍有效，可正常确认。
	mustConfirm(t, s, "c2", 11, at(11), "c2 的暂留未被拒绝操作影响，确认成功")
}

// TestReleaseDeclineRejections 验证释放与拒收的拒绝顺序，
// 且被拒绝的操作不改变状态。
func TestReleaseDeclineRejections(t *testing.T) {
	s := newServer(10, 11, 10, 10, 5)

	mustDiscover(t, s, "c1", nil, at(10), "规则4：取小者 10")
	mustConfirm(t, s, "c1", 10, at(10), "确认成功，租约 [10,20)")
	mustDiscover(t, s, "c2", nil, at(10), "规则4：仅剩 11，暂留 [10,20)")

	// 释放：时钟回拨、地址不在池内、非持有者。
	if err := logRelease(t, s, "c1", 99, at(9), "时钟回拨优先"); !errors.Is(err, ErrClockRollback) {
		t.Fatalf("应报时钟回拨，实际 %v", err)
	}
	if err := logRelease(t, s, "c1", 99, at(10), "地址不在池内"); !errors.Is(err, ErrAddrOutOfPool) {
		t.Fatalf("应报地址不在池内，实际 %v", err)
	}
	if err := logRelease(t, s, "c2", 10, at(10), "c2 不是 10 的持有者"); !errors.Is(err, ErrNotHolder) {
		t.Fatalf("应报非持有者，实际 %v", err)
	}
	if err := logRelease(t, s, "c2", 11, at(10),
		"c2 仅暂留 11，释放仅限持有者"); !errors.Is(err, ErrNotHolder) {
		t.Fatalf("应报非持有者，实际 %v", err)
	}

	// 拒收：时钟回拨、地址不在池内、非持有者（含暂留者）。
	if err := logDecline(t, s, "c1", 99, at(9), "时钟回拨优先"); !errors.Is(err, ErrClockRollback) {
		t.Fatalf("应报时钟回拨，实际 %v", err)
	}
	if err := logDecline(t, s, "c1", 99, at(10), "地址不在池内"); !errors.Is(err, ErrAddrOutOfPool) {
		t.Fatalf("应报地址不在池内，实际 %v", err)
	}
	if err := logDecline(t, s, "c2", 10, at(10), "c2 既非 10 的持有者也非暂留者"); !errors.Is(err, ErrNotHolder) {
		t.Fatalf("应报非持有者，实际 %v", err)
	}

	// 被拒绝的操作不改变状态：租约与暂留均保持有效。
	mustConfirm(t, s, "c1", 10, at(11), "c1 的租约未被拒绝操作影响，续租成功")
	mustConfirm(t, s, "c2", 11, at(11), "c2 的暂留未被拒绝操作影响，确认成功")

	// 暂留者可以拒收。
	mustDiscover(t, s, "c3", nil, at(21), "规则4：两租约 [11,21) 恰同时失效，终止时刻并列，取小者 10")
	if err := logDecline(t, s, "c3", 10, at(22),
		"暂留者拒收成功：清除暂留并隔离 [22,27)"); err != nil {
		t.Fatalf("暂留者拒收应成功: %v", err)
	}
}

// TestConcurrentExclusivity 并发执行发现与确认，验证同一地址
// 任一时刻至多一份有效租约，且不与他人的有效暂留并存。
func TestConcurrentExclusivity(t *testing.T) {
	const poolSize = 4
	const clients = 32
	s := newServer(0, poolSize-1, 60, 60, 10)

	type result struct {
		addr int
		ok   bool
	}
	results := make(chan result, clients)
	var wg sync.WaitGroup
	for i := 0; i < clients; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			client := fmt.Sprintf("c%d", id)
			addr, err := s.Discover(client, nil, t0)
			if err != nil {
				results <- result{}
				return
			}
			if err := s.Confirm(client, addr, t0); err != nil {
				t.Errorf("Confirm(%q, %d) 持有有效暂留却被拒: %v", client, addr, err)
			}
			results <- result{addr: addr, ok: true}
		}(i)
	}
	wg.Wait()
	close(results)

	leased := make(map[int]string)
	confirmed := 0
	for r := range results {
		if !r.ok {
			continue
		}
		confirmed++
		if _, dup := leased[r.addr]; dup {
			t.Errorf("地址 %d 被确认多于一次，违反互斥", r.addr)
		}
		leased[r.addr] = "taken"
	}
	t.Logf("并发确认：%d 个客户端竞争 %d 个地址，成功 %d 个，地址两两不同", clients, poolSize, confirmed)
	if confirmed > poolSize {
		t.Fatalf("成功确认数 %d 超过池大小 %d", confirmed, poolSize)
	}
	if confirmed != len(leased) {
		t.Fatalf("确认成功的地址不唯一")
	}
}

// TestDeterministicReplay 验证相同操作序列重放结果完全相同。
func TestDeterministicReplay(t *testing.T) {
	type op struct {
		kind   string
		client string
		addr   int
		sec    int
	}
	script := []op{
		{"discover", "a", -1, 0},
		{"discover", "b", -1, 0},
		{"confirm", "a", 10, 1},
		{"confirm", "b", 11, 1},
		{"release", "a", 10, 3},
		{"discover", "c", -1, 4},
		{"decline", "b", 11, 5},
		{"discover", "a", 11, 8},
		{"confirm", "a", 11, 9},
		{"release", "a", 11, 12},
		{"discover", "d", -1, 13},
		{"confirm", "d", 11, 14},
	}

	run := func() []string {
		s := newServer(10, 11, 10, 10, 3)
		out := make([]string, 0, len(script))
		for _, o := range script {
			var res string
			switch o.kind {
			case "discover":
				var req *int
				if o.addr >= 0 {
					req = intPtr(o.addr)
				}
				addr, err := s.Discover(o.client, req, at(o.sec))
				res = fmt.Sprintf("addr=%d err=%v", addr, err)
			case "confirm":
				res = fmt.Sprintf("err=%v", s.Confirm(o.client, o.addr, at(o.sec)))
			case "release":
				res = fmt.Sprintf("err=%v", s.Release(o.client, o.addr, at(o.sec)))
			case "decline":
				res = fmt.Sprintf("err=%v", s.Decline(o.client, o.addr, at(o.sec)))
			}
			out = append(out, fmt.Sprintf("%s(%s,%d)@%d -> %s", o.kind, o.client, o.addr, o.sec, res))
		}
		return out
	}

	first := run()
	second := run()
	for i := range first {
		t.Logf("重放[%d] 第一次=%s 第二次=%s", i, first[i], second[i])
		if first[i] != second[i] {
			t.Fatalf("第 %d 步重放结果不一致：%q != %q", i, first[i], second[i])
		}
	}
}
