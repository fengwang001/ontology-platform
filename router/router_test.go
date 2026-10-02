package router

import (
	"errors"
	"sync"
	"testing"
)

func mustRouter(t *testing.T, idle, drain int64) *Router {
	t.Helper()
	r, err := NewRouter(idle, drain)
	if err != nil {
		t.Fatalf("NewRouter(%d, %d) 失败: %v", idle, drain, err)
	}
	return r
}

func mustAdd(t *testing.T, r *Router, ids ...string) {
	t.Helper()
	for _, id := range ids {
		if err := r.AddHost(id); err != nil {
			t.Fatalf("AddHost(%q) 失败: %v", id, err)
		}
	}
}

func mustRoute(t *testing.T, r *Router, key string, now int64, want string) {
	t.Helper()
	got, err := r.Route(key, now)
	if err != nil {
		t.Fatalf("Route(%q, %d) 失败: %v", key, now, err)
	}
	if got != want {
		t.Fatalf("Route(%q, %d) = %q, 期望 %q", key, now, got, want)
	}
}

func mustStatus(t *testing.T, r *Router, id string, now int64, want HostStatus) {
	t.Helper()
	got, err := r.Status(id, now)
	if err != nil {
		t.Fatalf("Status(%q, %d) 失败: %v", id, now, err)
	}
	if got != want {
		t.Fatalf("Status(%q, %d) = %v, 期望 %v", id, now, got, want)
	}
}

func mustLive(t *testing.T, r *Router, id string, now int64, want int) {
	t.Helper()
	got, err := r.Live(id, now)
	if err != nil {
		t.Fatalf("Live(%q, %d) 失败: %v", id, now, err)
	}
	if got != want {
		t.Fatalf("Live(%q, %d) = %d, 期望 %d", id, now, got, want)
	}
}

func mustDrain(t *testing.T, r *Router, id string, now int64) {
	t.Helper()
	if err := r.Drain(id, now); err != nil {
		t.Fatalf("Drain(%q, %d) 失败: %v", id, now, err)
	}
}

func TestNewRouterInvalidConfig(t *testing.T) {
	for _, td := range [][2]int64{{0, 1}, {1, 0}, {-5, 10}, {10, -5}, {1_000_000_001, 1}, {1, 1_000_000_001}} {
		if _, err := NewRouter(td[0], td[1]); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("NewRouter(%d, %d) err = %v, 期望 ErrInvalidConfig", td[0], td[1], err)
		}
	}
	for _, td := range [][2]int64{{1, 1}, {1_000_000_000, 1_000_000_000}} {
		if _, err := NewRouter(td[0], td[1]); err != nil {
			t.Fatalf("NewRouter(%d, %d) 应成功, 得到 %v", td[0], td[1], err)
		}
	}
}

func TestAddHostRejections(t *testing.T) {
	r := mustRouter(t, 10, 20)
	if err := r.AddHost(""); !errors.Is(err, ErrEmptyHostID) {
		t.Fatalf("AddHost(空) err = %v, 期望 ErrEmptyHostID", err)
	}
	mustAdd(t, r, "h0")
	if err := r.AddHost("h0"); !errors.Is(err, ErrHostExists) {
		t.Fatalf("AddHost(重复) err = %v, 期望 ErrHostExists", err)
	}
	if errors.Is(ErrEmptyHostID, ErrHostExists) {
		t.Fatal("两种拒绝原因必须可区分")
	}
}

// 绑定恰在 last+T 过期：last+T == now 时已过期，不计入有效绑定数。
func TestBindingExpiresExactlyAtLastPlusT(t *testing.T) {
	r := mustRouter(t, 10, 100)
	mustAdd(t, r, "h0")
	mustRoute(t, r, "a", 5, "h0")
	mustLive(t, r, "h0", 14, 1)
	mustLive(t, r, "h0", 15, 0)
	mustRoute(t, r, "a", 15, "h0")
	mustLive(t, r, "h0", 15, 1)
}

// 排空期恰在 s+D 被移除：now < s+D 保持排空，now == s+D 移除。
func TestDrainDeadlineExactlyAtSPlusD(t *testing.T) {
	r := mustRouter(t, 10, 20)
	mustAdd(t, r, "h0", "h1")
	mustRoute(t, r, "a", 0, "h0")
	mustDrain(t, r, "h0", 5)
	mustRoute(t, r, "a", 9, "h0")
	mustRoute(t, r, "a", 18, "h0")
	mustStatus(t, r, "h0", 24, StatusDraining)
	mustStatus(t, r, "h0", 25, StatusRemoved)
}

// 排空瞬间没有有效绑定的主机立即被移除。
func TestDrainInstantRemoval(t *testing.T) {
	r := mustRouter(t, 10, 20)
	mustAdd(t, r, "h0", "h1")
	mustRoute(t, r, "a", 0, "h0")
	// a 的绑定在 now=11 已过期（0+10=10 不大于 11），h0 排空即移除。
	mustDrain(t, r, "h0", 11)
	mustStatus(t, r, "h0", 11, StatusRemoved)
	// h1 从无绑定，排空即移除。
	mustDrain(t, r, "h1", 11)
	mustStatus(t, r, "h1", 11, StatusRemoved)
}

// 排空态主机继续服务有效绑定并刷新 last，因而推迟移除到期限。
func TestDrainingServesValidBindings(t *testing.T) {
	r := mustRouter(t, 10, 20)
	mustAdd(t, r, "h0", "h1")
	mustRoute(t, r, "a", 0, "h0")
	mustDrain(t, r, "h0", 5)
	mustRoute(t, r, "a", 9, "h0")
	mustRoute(t, r, "a", 18, "h0")
	mustRoute(t, r, "a", 24, "h0")
	mustStatus(t, r, "h0", 24, StatusDraining)
	mustStatus(t, r, "h0", 25, StatusRemoved)
	mustRoute(t, r, "a", 25, "h1")
}

// 新会话键从不落到排空主机。
func TestNewKeysNeverLandOnDraining(t *testing.T) {
	r := mustRouter(t, 10, 100)
	mustAdd(t, r, "h0", "h1")
	mustRoute(t, r, "a", 0, "h0")
	mustDrain(t, r, "h0", 1)
	mustRoute(t, r, "b", 2, "h1")
	mustRoute(t, r, "c", 3, "h1")
	mustLive(t, r, "h0", 3, 1)
	mustLive(t, r, "h1", 3, 2)
}

// 有效绑定数并列时取 id 最小（按字节序）。
func TestTieBreakSmallestID(t *testing.T) {
	r := mustRouter(t, 10, 100)
	mustAdd(t, r, "h2", "h0", "h1")
	mustRoute(t, r, "a", 0, "h0")
	mustRoute(t, r, "b", 1, "h1")
	mustRoute(t, r, "c", 2, "h2")
	mustRoute(t, r, "d", 3, "h0")
}

// 已过期绑定不计入有效绑定数。
func TestExpiredBindingsNotCounted(t *testing.T) {
	r := mustRouter(t, 10, 100)
	mustAdd(t, r, "h0", "h1")
	mustRoute(t, r, "a", 0, "h0")
	mustRoute(t, r, "b", 1, "h1")
	mustRoute(t, r, "c", 2, "h0")
	mustLive(t, r, "h0", 15, 0)
	mustLive(t, r, "h1", 15, 0)
	// 若过期绑定被计入，h0 计 2、h1 计 1，d 会落到 h1。
	mustRoute(t, r, "d", 15, "h0")
}

// 无活跃主机但键有有效绑定时仍可路由。
func TestRouteWithValidBindingWhenNoActiveHosts(t *testing.T) {
	r := mustRouter(t, 10, 100)
	mustAdd(t, r, "h0")
	mustRoute(t, r, "a", 0, "h0")
	mustDrain(t, r, "h0", 1)
	mustRoute(t, r, "a", 5, "h0")
	if _, err := r.Route("b", 5); !errors.Is(err, ErrNoActiveHost) {
		t.Fatalf("Route(b, 5) err = %v, 期望 ErrNoActiveHost", err)
	}
}

// 无可用主机报错后，落实与最大 now 的推进保留。
func TestNoActiveHostErrorKeepsSettle(t *testing.T) {
	r := mustRouter(t, 10, 20)
	mustAdd(t, r, "h0", "h1")
	mustRoute(t, r, "a", 0, "h0")
	mustDrain(t, r, "h0", 1)
	mustDrain(t, r, "h1", 2)
	mustStatus(t, r, "h1", 2, StatusRemoved)
	if _, err := r.Route("b", 15); !errors.Is(err, ErrNoActiveHost) {
		t.Fatalf("Route(b, 15) err = %v, 期望 ErrNoActiveHost", err)
	}
	// 落实保留：h0 已在 now=15 被移除（a 的绑定 0+10=10 不大于 15）。
	mustStatus(t, r, "h0", 15, StatusRemoved)
	// 最大 now 推进保留：now=14 构成时钟回退。
	if _, err := r.Route("b", 14); !errors.Is(err, ErrClockRegression) {
		t.Fatalf("Route(b, 14) err = %v, 期望 ErrClockRegression", err)
	}
}

// 已移除主机的 id 不可再登记。
func TestRemovedIDNotReusable(t *testing.T) {
	r := mustRouter(t, 10, 20)
	mustAdd(t, r, "h0")
	mustDrain(t, r, "h0", 0)
	mustStatus(t, r, "h0", 0, StatusRemoved)
	if err := r.AddHost("h0"); !errors.Is(err, ErrHostExists) {
		t.Fatalf("AddHost(已移除) err = %v, 期望 ErrHostExists", err)
	}
}

// 规格示例路径一：T=10、D=20，h0 排空后绑定过期被移除。
func TestSpecExamplePath1(t *testing.T) {
	r := mustRouter(t, 10, 20)
	mustAdd(t, r, "h0", "h1")
	mustRoute(t, r, "a", 0, "h0")
	mustRoute(t, r, "b", 1, "h1")
	mustRoute(t, r, "c", 2, "h0")
	mustDrain(t, r, "h0", 5)
	mustStatus(t, r, "h0", 5, StatusDraining)
	mustRoute(t, r, "a", 9, "h0")
	mustRoute(t, r, "d", 9, "h1")
	mustRoute(t, r, "c", 12, "h1")
	mustRoute(t, r, "a", 20, "h1")
	mustStatus(t, r, "h0", 20, StatusRemoved)
	mustLive(t, r, "h0", 20, 0)
	// b、d 的绑定在 now=20 已过期，c(last=12)、a(last=20) 有效。
	mustLive(t, r, "h1", 20, 2)
}

// 规格示例路径二：绑定持续有效使 h0 保持排空，直到 25 == s+D 被移除。
func TestSpecExamplePath2(t *testing.T) {
	r := mustRouter(t, 10, 20)
	mustAdd(t, r, "h0", "h1")
	mustRoute(t, r, "a", 0, "h0")
	mustRoute(t, r, "b", 1, "h1")
	mustRoute(t, r, "c", 2, "h0")
	mustDrain(t, r, "h0", 5)
	mustRoute(t, r, "a", 9, "h0")
	mustRoute(t, r, "a", 18, "h0")
	mustRoute(t, r, "a", 24, "h0")
	mustStatus(t, r, "h0", 24, StatusDraining)
	mustRoute(t, r, "a", 25, "h1")
	mustStatus(t, r, "h0", 25, StatusRemoved)
}

// 参数、时间、时钟三类被拒绝的操作不得改变任何状态（含最大 now）。
func TestRejectedOpsDoNotChangeState(t *testing.T) {
	r := mustRouter(t, 10, 100)
	mustAdd(t, r, "h0", "h1")
	mustRoute(t, r, "a", 3, "h0")

	// 参数非法：不推进最大 now（否则 now=9 会推进）。
	if _, err := r.Route("", 9); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("Route(空键) err = %v, 期望 ErrEmptyKey", err)
	}
	if err := r.Drain("nope", 9); !errors.Is(err, ErrHostNotFound) {
		t.Fatalf("Drain(不存在) err = %v, 期望 ErrHostNotFound", err)
	}
	if _, err := r.Status("nope", 9); !errors.Is(err, ErrHostNotFound) {
		t.Fatalf("Status(不存在) err = %v, 期望 ErrHostNotFound", err)
	}
	if _, err := r.Live("nope", 9); !errors.Is(err, ErrHostNotFound) {
		t.Fatalf("Live(不存在) err = %v, 期望 ErrHostNotFound", err)
	}
	mustRoute(t, r, "b", 5, "h1") // 若 9 已推进，此处必报时钟回退

	// 时间非法：now < 0 或 now > 1e15，状态不变。
	if _, err := r.Route("c", -1); !errors.Is(err, ErrInvalidTime) {
		t.Fatalf("Route(now=-1) err = %v, 期望 ErrInvalidTime", err)
	}
	if _, err := r.Route("c", 1_000_000_000_000_001); !errors.Is(err, ErrInvalidTime) {
		t.Fatalf("Route(now=1e15+1) err = %v, 期望 ErrInvalidTime", err)
	}
	mustLive(t, r, "h0", 5, 1)
	mustLive(t, r, "h1", 5, 1)

	// 时钟回退：绑定不建立、最大 now 不后退。
	mustRoute(t, r, "c", 10, "h0")
	if _, err := r.Route("d", 9); !errors.Is(err, ErrClockRegression) {
		t.Fatalf("Route(回退) err = %v, 期望 ErrClockRegression", err)
	}
	mustLive(t, r, "h0", 10, 2)
	mustLive(t, r, "h1", 10, 1)
	mustRoute(t, r, "d", 10, "h1")
}

// 校验顺序：参数非法优先于时间非法，时间非法优先于时钟回退。
func TestRejectionOrder(t *testing.T) {
	r := mustRouter(t, 10, 100)
	mustAdd(t, r, "h0")
	mustRoute(t, r, "a", 5, "h0")
	if _, err := r.Route("", -1); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("参数+时间同时非法 err = %v, 期望 ErrEmptyKey", err)
	}
	if err := r.Drain("nope", -1); !errors.Is(err, ErrHostNotFound) {
		t.Fatalf("Drain 参数+时间同时非法 err = %v, 期望 ErrHostNotFound", err)
	}
	if _, err := r.Route("k", -1); !errors.Is(err, ErrInvalidTime) {
		t.Fatalf("时间非法+回退同时成立 err = %v, 期望 ErrInvalidTime", err)
	}
}

// Drain 的状态类拒绝（主机不是活跃态）保留落实与最大 now 推进。
func TestDrainNotActiveKeepsSettle(t *testing.T) {
	r := mustRouter(t, 10, 100)
	mustAdd(t, r, "h0")
	mustRoute(t, r, "a", 0, "h0")
	mustDrain(t, r, "h0", 5)
	if err := r.Drain("h0", 6); !errors.Is(err, ErrHostNotActive) {
		t.Fatalf("重复 Drain err = %v, 期望 ErrHostNotActive", err)
	}
	// 最大 now 已推进到 6。
	if _, err := r.Route("b", 5); !errors.Is(err, ErrClockRegression) {
		t.Fatalf("Route(5) err = %v, 期望 ErrClockRegression", err)
	}
	mustStatus(t, r, "h0", 6, StatusDraining)
}

// 并发调用等价于某个串行顺序：竞态检测下结果自洽。
func TestConcurrent(t *testing.T) {
	r := mustRouter(t, 10, 100)
	mustAdd(t, r, "h0", "h1", "h2", "h3")
	const workers = 8
	const keys = 20
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				key := string(rune('a' + (w+i)%keys))
				if _, err := r.Route(key, 0); err != nil {
					t.Errorf("Route(%q, 0) 失败: %v", key, err)
				}
				if _, err := r.Live("h0", 0); err != nil {
					t.Errorf("Live 失败: %v", err)
				}
				if _, err := r.Status("h1", 0); err != nil {
					t.Errorf("Status 失败: %v", err)
				}
			}
		}(w)
	}
	wg.Wait()
	// 活跃主机上的有效绑定数之和不超过会话键总数。
	total := 0
	for _, id := range []string{"h0", "h1", "h2", "h3"} {
		n, err := r.Live(id, 0)
		if err != nil {
			t.Fatalf("Live(%q, 0) 失败: %v", id, err)
		}
		total += n
	}
	if total > keys {
		t.Fatalf("有效绑定数之和 %d 超过会话键总数 %d", total, keys)
	}
}
