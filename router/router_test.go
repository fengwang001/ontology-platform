package router

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

func mustRouter(t *testing.T, ttd, d int64, ids ...string) *Router {
	t.Helper()
	r, err := NewRouter(ttd, d)
	if err != nil {
		t.Fatalf("NewRouter(%d, %d) 失败: %v", ttd, d, err)
	}
	for _, id := range ids {
		if err := r.AddHost(id); err != nil {
			t.Fatalf("AddHost(%q) 失败: %v", id, err)
		}
	}
	return r
}

func mustRoute(t *testing.T, r *Router, key string, now int64) string {
	t.Helper()
	h, err := r.Route(key, now)
	if err != nil {
		t.Fatalf("Route(%q, %d) 失败: %v", key, now, err)
	}
	return h
}

func mustStatus(t *testing.T, r *Router, id string, now int64) HostStatus {
	t.Helper()
	s, err := r.Status(id, now)
	if err != nil {
		t.Fatalf("Status(%q, %d) 失败: %v", id, now, err)
	}
	return s
}

func mustLive(t *testing.T, r *Router, id string, now int64) int {
	t.Helper()
	n, err := r.Live(id, now)
	if err != nil {
		t.Fatalf("Live(%q, %d) 失败: %v", id, now, err)
	}
	return n
}

// 配置非法：T 或 D 不在 [1, 1e9] 时整体拒绝。
func TestInvalidConfig(t *testing.T) {
	for _, c := range [][2]int64{{0, 10}, {10, 0}, {-1, 10}, {10, -5}, {1e9 + 1, 10}, {10, 1e9 + 1}, {0, 0}} {
		if _, err := NewRouter(c[0], c[1]); !errors.Is(err, ErrInvalidConfig) {
			t.Errorf("NewRouter(%d, %d) 应报 ErrInvalidConfig，实际 %v", c[0], c[1], err)
		}
	}
	for _, c := range [][2]int64{{1, 1}, {1, 1e9}, {1e9, 1}, {1e9, 1e9}} {
		if _, err := NewRouter(c[0], c[1]); err != nil {
			t.Errorf("NewRouter(%d, %d) 应成功，实际 %v", c[0], c[1], err)
		}
	}
}

// AddHost 的拒绝原因可区分：id 为空 vs id 已存在（含已移除）。
func TestAddHostRejections(t *testing.T) {
	r := mustRouter(t, 10, 20, "h0")
	if err := r.AddHost(""); !errors.Is(err, ErrEmptyHostID) {
		t.Errorf("空 id 应报 ErrEmptyHostID，实际 %v", err)
	}
	if err := r.AddHost("h0"); !errors.Is(err, ErrHostExists) {
		t.Errorf("重复 id 应报 ErrHostExists，实际 %v", err)
	}
}

// 已移除主机的 id 不可复用。
func TestRemovedIDNotReusable(t *testing.T) {
	r := mustRouter(t, 10, 20, "h0", "h1")
	if err := r.Drain("h0", 0); err != nil {
		t.Fatalf("Drain 失败: %v", err)
	}
	// h0 排空瞬间无有效绑定，立即被移除。
	if s := mustStatus(t, r, "h0", 0); s != Removed {
		t.Fatalf("h0 应为已移除，实际 %v", s)
	}
	if err := r.AddHost("h0"); !errors.Is(err, ErrHostExists) {
		t.Errorf("已移除 id 再登记应报 ErrHostExists，实际 %v", err)
	}
}

// 题目主示例：T=10、D=20 的完整路由路径。
func TestExampleScenario(t *testing.T) {
	r := mustRouter(t, 10, 20, "h0", "h1")

	if got := mustRoute(t, r, "a", 0); got != "h0" {
		t.Fatalf("Route(a,0) 应为 h0，实际 %s", got)
	}
	if got := mustRoute(t, r, "b", 1); got != "h1" {
		t.Fatalf("Route(b,1) 应为 h1，实际 %s", got)
	}
	// 两台各 1 个有效绑定，并列取 id 最小者。
	if got := mustRoute(t, r, "c", 2); got != "h0" {
		t.Fatalf("Route(c,2) 应为 h0，实际 %s", got)
	}
	if err := r.Drain("h0", 5); err != nil {
		t.Fatalf("Drain(h0,5) 失败: %v", err)
	}
	if s := mustStatus(t, r, "h0", 5); s != Draining {
		t.Fatalf("h0 应为排空态，实际 %v", s)
	}
	// 排空态主机继续服务有效绑定并刷新 last。
	if got := mustRoute(t, r, "a", 9); got != "h0" {
		t.Fatalf("Route(a,9) 应为 h0，实际 %s", got)
	}
	// 新会话键不落到排空主机。
	if got := mustRoute(t, r, "d", 9); got != "h1" {
		t.Fatalf("Route(d,9) 应为 h1，实际 %s", got)
	}
	// c 的 last+T=12 恰等于 12，已过期，改绑活跃主机。
	if got := mustRoute(t, r, "c", 12); got != "h1" {
		t.Fatalf("Route(c,12) 应为 h1，实际 %s", got)
	}
	// a 的 last+T=19 不大于 20 已过期；落实先把无有效绑定的 h0 移除。
	if got := mustRoute(t, r, "a", 20); got != "h1" {
		t.Fatalf("Route(a,20) 应为 h1，实际 %s", got)
	}
	if s := mustStatus(t, r, "h0", 20); s != Removed {
		t.Fatalf("h0 应为已移除，实际 %v", s)
	}
	if n := mustLive(t, r, "h0", 20); n != 0 {
		t.Fatalf("已移除主机有效绑定数应为 0，实际 %d", n)
	}
}

// 另一路径：排空主机因绑定持续有效而保持，直到 s+D 期限被移除。
func TestDrainDeadlinePath(t *testing.T) {
	r := mustRouter(t, 10, 20, "h0", "h1")
	mustRoute(t, r, "a", 0)
	mustRoute(t, r, "b", 1)
	mustRoute(t, r, "c", 2)
	if err := r.Drain("h0", 5); err != nil {
		t.Fatalf("Drain(h0,5) 失败: %v", err)
	}
	// a 在 9、18、24 都被路由，绑定持续有效，h0 保持排空。
	for _, now := range []int64{9, 18, 24} {
		if got := mustRoute(t, r, "a", now); got != "h0" {
			t.Fatalf("Route(a,%d) 应为 h0，实际 %s", now, got)
		}
		if s := mustStatus(t, r, "h0", now); s != Draining {
			t.Fatalf("now=%d 时 h0 应为排空态，实际 %v", now, s)
		}
	}
	// 25 恰等于 s+D=25，落实把 h0 移除，a 改绑 h1。
	if got := mustRoute(t, r, "a", 25); got != "h1" {
		t.Fatalf("Route(a,25) 应为 h1，实际 %s", got)
	}
	if s := mustStatus(t, r, "h0", 25); s != Removed {
		t.Fatalf("h0 应为已移除，实际 %v", s)
	}
}

// 绑定恰在 last+T 过期：last+T-1 仍有效，last+T 已过期。
func TestBindingExpiresExactlyAtLastPlusT(t *testing.T) {
	r := mustRouter(t, 10, 100, "h0", "h1")
	mustRoute(t, r, "a", 0) // h0，last=0
	mustRoute(t, r, "b", 0) // h1，使 h1 也有 1 个绑定
	// now=9：last+T=10 > 9，仍粘 h0。
	if got := mustRoute(t, r, "a", 9); got != "h0" {
		t.Fatalf("Route(a,9) 应为 h0，实际 %s", got)
	}
	// now=19：last+T=19 恰等于 19，已过期；h0、h1 各有 1 个有效
	// 绑定（b 的 last+T=10 <= 19 也过期，均计 0），并列取 h0。
	if got := mustRoute(t, r, "a", 19); got != "h0" {
		t.Fatalf("Route(a,19) 应为 h0，实际 %s", got)
	}
	// now=29：a 的 last+T=29 恰等于 29 过期；b 也过期；并列取 h0。
	if got := mustRoute(t, r, "a", 29); got != "h0" {
		t.Fatalf("Route(a,29) 应为 h0，实际 %s", got)
	}
}

// 排空瞬间无有效绑定即移除（含绑定恰在排空时刻过期的情况）。
func TestDrainImmediateRemoval(t *testing.T) {
	// 无任何绑定：Drain 后立即移除。
	r := mustRouter(t, 10, 20, "h0", "h1")
	if err := r.Drain("h0", 3); err != nil {
		t.Fatalf("Drain(h0,3) 失败: %v", err)
	}
	if s := mustStatus(t, r, "h0", 3); s != Removed {
		t.Fatalf("无绑定时 Drain 后应立即移除，实际 %v", s)
	}

	// 绑定在排空时刻恰好过期：last=2、T=10、now=12，last+T=12 不大于 12。
	r2 := mustRouter(t, 10, 20, "h0", "h1")
	mustRoute(t, r2, "a", 2) // h0
	mustRoute(t, r2, "b", 2) // h1
	if err := r2.Drain("h0", 12); err != nil {
		t.Fatalf("Drain(h0,12) 失败: %v", err)
	}
	if s := mustStatus(t, r2, "h0", 12); s != Removed {
		t.Fatalf("绑定恰过期时 Drain 后应立即移除，实际 %v", s)
	}
}

// 已过期绑定不计入有效绑定数。
func TestExpiredBindingsNotCounted(t *testing.T) {
	r := mustRouter(t, 10, 100, "h0", "h1")
	mustRoute(t, r, "old", 0)  // h0，last=0
	mustRoute(t, r, "new", 15) // old 已过期，h0 计 0，h1 计 0，并列取 h0
	// 让 h1 有一个有效绑定：再绑一个键，h0 有 new（有效），h1 无 → 落 h1。
	mustRoute(t, r, "x", 16) // h1
	// 现在 h0: new(last=15) 有效计 1；h1: x(last=16) 有效计 1。
	// 等 new 过期而 x 仍有效：now=26，new 的 last+T=25 <= 26 过期，x 的 last+T=26 恰等于 26 也过期。
	// 取 now=25：new 恰过期（25 不大于 25 不成立？25>25 否 → 无效），x 的 last+T=26 > 25 有效。
	if got := mustRoute(t, r, "y", 25); got != "h0" {
		t.Fatalf("h0 有效绑定 0 < h1 有效绑定 1，应落 h0，实际 %s", got)
	}
	if n := mustLive(t, r, "h0", 25); n != 1 {
		t.Fatalf("h0 有效绑定数应为 1（y），实际 %d", n)
	}
	if n := mustLive(t, r, "h1", 25); n != 1 {
		t.Fatalf("h1 有效绑定数应为 1（x），实际 %d", n)
	}
}

// 有效绑定数并列时取 id 最小者。
func TestTieBreakSmallestID(t *testing.T) {
	r := mustRouter(t, 100, 100, "h2", "h0", "h1")
	// 三台均为 0 个绑定，应取 id 最小的 h0。
	if got := mustRoute(t, r, "a", 0); got != "h0" {
		t.Fatalf("并列应取 h0，实际 %s", got)
	}
	// h0 计 1，h1、h2 计 0，并列取 h1。
	if got := mustRoute(t, r, "b", 0); got != "h1" {
		t.Fatalf("并列应取 h1，实际 %s", got)
	}
	if got := mustRoute(t, r, "c", 0); got != "h2" {
		t.Fatalf("仅剩 h2 计 0，应取 h2，实际 %s", got)
	}
	// 各计 1，并列再取 h0。
	if got := mustRoute(t, r, "d", 0); got != "h0" {
		t.Fatalf("并列应取 h0，实际 %s", got)
	}
}

// 无活跃主机但键有有效绑定时仍可路由（粘到排空主机）。
func TestRouteWithValidBindingWhenNoActiveHosts(t *testing.T) {
	r := mustRouter(t, 10, 100, "h0")
	mustRoute(t, r, "a", 0)
	if err := r.Drain("h0", 1); err != nil {
		t.Fatalf("Drain 失败: %v", err)
	}
	// 没有活跃主机，但 a 的绑定仍有效。
	if got := mustRoute(t, r, "a", 5); got != "h0" {
		t.Fatalf("有效绑定应仍路由到 h0，实际 %s", got)
	}
	// 新键无活跃主机可用。
	if _, err := r.Route("b", 6); !errors.Is(err, ErrNoActiveHost) {
		t.Fatalf("应报 ErrNoActiveHost，实际 %v", err)
	}
}

// 无可用主机报错后落实仍保留：排空主机在报错调用中被移除。
func TestSettleRetainedAfterNoActiveHostError(t *testing.T) {
	r := mustRouter(t, 10, 20, "h0")
	mustRoute(t, r, "a", 0)
	if err := r.Drain("h0", 1); err != nil {
		t.Fatalf("Drain 失败: %v", err)
	}
	// now=30 >= s+D=21，且 a 的绑定 last+T=10 <= 30 已过期：
	// Route(b,30) 无活跃主机报错，但落实应把 h0 移除。
	if _, err := r.Route("b", 30); !errors.Is(err, ErrNoActiveHost) {
		t.Fatalf("应报 ErrNoActiveHost，实际 %v", err)
	}
	if s := mustStatus(t, r, "h0", 30); s != Removed {
		t.Fatalf("落实应保留，h0 应为已移除，实际 %v", s)
	}
	// 最大 now 的推进也保留：now=29 应报时钟回退。
	if _, err := r.Route("c", 29); !errors.Is(err, ErrClockRollback) {
		t.Fatalf("应报 ErrClockRollback，实际 %v", err)
	}
}

// Drain 对非活跃主机报错时，落实与最大 now 推进保留。
func TestDrainNotActiveRetainsSettle(t *testing.T) {
	r := mustRouter(t, 10, 20, "h0", "h1")
	mustRoute(t, r, "a", 0) // h0
	if err := r.Drain("h0", 1); err != nil {
		t.Fatalf("Drain(h0,1) 失败: %v", err)
	}
	// now=30：a 的绑定已过期，h0 落实被移除；对 h0 再 Drain 报非活跃。
	if err := r.Drain("h0", 30); !errors.Is(err, ErrHostNotActive) {
		t.Fatalf("应报 ErrHostNotActive，实际 %v", err)
	}
	if s := mustStatus(t, r, "h0", 30); s != Removed {
		t.Fatalf("h0 应为已移除，实际 %v", s)
	}
	// 对已移除主机 Drain 同样报非活跃（主机仍存在）。
	if err := r.Drain("h0", 31); !errors.Is(err, ErrHostNotActive) {
		t.Fatalf("已移除主机 Drain 应报 ErrHostNotActive，实际 %v", err)
	}
	// 对排空中的主机 Drain 也报非活跃。
	r2 := mustRouter(t, 10, 100, "h0", "h1")
	mustRoute(t, r2, "a", 0)
	if err := r2.Drain("h0", 1); err != nil {
		t.Fatalf("Drain 失败: %v", err)
	}
	if err := r2.Drain("h0", 2); !errors.Is(err, ErrHostNotActive) {
		t.Fatalf("排空中 Drain 应报 ErrHostNotActive，实际 %v", err)
	}
}

// 被拒绝的参数、时间、时钟三类操作不改变任何状态（含最大 now）。
func TestRejectedOpsDoNotChangeState(t *testing.T) {
	r := mustRouter(t, 10, 20, "h0", "h1")
	mustRoute(t, r, "a", 5) // maxNow=5，h0 有绑定 a(last=5)

	// 参数非法：Route 空 key。
	if _, err := r.Route("", 6); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("应报 ErrEmptyKey，实际 %v", err)
	}
	// 参数非法：Drain/Status/Live 主机不存在。
	if err := r.Drain("ghost", 6); !errors.Is(err, ErrHostNotFound) {
		t.Fatalf("应报 ErrHostNotFound，实际 %v", err)
	}
	if _, err := r.Status("ghost", 6); !errors.Is(err, ErrHostNotFound) {
		t.Fatalf("应报 ErrHostNotFound，实际 %v", err)
	}
	if _, err := r.Live("ghost", 6); !errors.Is(err, ErrHostNotFound) {
		t.Fatalf("应报 ErrHostNotFound，实际 %v", err)
	}
	// 时间非法：now < 0 或 now > 1e15。
	if _, err := r.Route("b", -1); !errors.Is(err, ErrInvalidTime) {
		t.Fatalf("应报 ErrInvalidTime，实际 %v", err)
	}
	if _, err := r.Route("b", 1e15+1); !errors.Is(err, ErrInvalidTime) {
		t.Fatalf("应报 ErrInvalidTime，实际 %v", err)
	}
	// 时钟回退：now < maxNow=5。
	if _, err := r.Route("b", 4); !errors.Is(err, ErrClockRollback) {
		t.Fatalf("应报 ErrClockRollback，实际 %v", err)
	}
	// 以上拒绝均不得推进最大 now：now=5 仍应被接受（5 < 5 不成立）。
	// a 的绑定 last+T=15 > 5 仍有效，应粘 h0。
	if got := mustRoute(t, r, "a", 5); got != "h0" {
		t.Fatalf("maxNow 不应被推进，Route(a,5) 应得 h0，实际 %s", got)
	}
	// 绑定状态未被改变：a 仍粘 h0，h0 有效绑定数为 1。
	if n := mustLive(t, r, "h0", 5); n != 1 {
		t.Fatalf("h0 有效绑定数应为 1，实际 %d", n)
	}
	// 参数非法优先于时间非法与时钟回退。
	if _, err := r.Route("", -1); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("参数非法应优先，实际 %v", err)
	}
	if err := r.Drain("ghost", -1); !errors.Is(err, ErrHostNotFound) {
		t.Fatalf("参数非法应优先，实际 %v", err)
	}
	// 时间非法优先于时钟回退。
	if _, err := r.Route("b", -2); !errors.Is(err, ErrInvalidTime) {
		t.Fatalf("时间非法应优先于时钟回退，实际 %v", err)
	}
}

// 并发调用：结果等价于某个串行顺序（配合 -race 检测数据竞争），
// 且不变量保持：已移除主机无绑定、新绑定只落活跃主机。
func TestConcurrentCalls(t *testing.T) {
	r := mustRouter(t, 5, 50, "h0", "h1", "h2", "h3")
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				now := int64(i)
				key := fmt.Sprintf("k%d-%d", g, i%10)
				host := fmt.Sprintf("h%d", i%4)
				_, _ = r.Route(key, now)
				_ = r.Drain(host, now)
				_, _ = r.Status(host, now)
				_, _ = r.Live(host, now)
			}
		}(g)
	}
	wg.Wait()
	// 最终状态可通过 Status/Live 正常读取（串行化未破坏内部结构）。
	for _, id := range []string{"h0", "h1", "h2", "h3"} {
		if _, err := r.Status(id, 199); err != nil {
			t.Errorf("Status(%s) 失败: %v", id, err)
		}
	}
}
