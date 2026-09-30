package revocation

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// testClock 是可手动推进的时钟。
type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func newTestClock(t time.Time) *testClock { return &testClock{now: t} }

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}

var base = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func at(seconds int) time.Time { return base.Add(time.Duration(seconds) * time.Second) }

func mustSubmit(t *testing.T, c *Cache, resp Response) {
	t.Helper()
	t.Logf("输入响应: %s", resp)
	if err := c.Submit(resp); err != nil {
		t.Fatalf("提交响应 %s 失败: %v", resp, err)
	}
	rec, _ := c.Record(resp.Serial)
	t.Logf("合并结果: 序号:%s -> %s", resp.Serial, rec)
}

func judge(t *testing.T, c *Cache, serial string, want Verdict, basis string) {
	t.Helper()
	got := c.Judge(serial)
	rec, ok := c.Record(serial)
	t.Logf("判定: 序号:%s 记录:%v(存在:%v) 输出:%s 依据:%s", serial, rec, ok, got, basis)
	if got != want {
		t.Fatalf("判定 %s: 得到 %s，期望 %s", serial, got, want)
	}
}

func rejectReason(t *testing.T, c *Cache, resp Response, want RejectReason) {
	t.Helper()
	t.Logf("输入响应: %s", resp)
	err := c.Submit(resp)
	if err == nil {
		t.Fatalf("响应 %s 应被拒绝", resp)
	}
	var re *RejectError
	if !errors.As(err, &re) {
		t.Fatalf("错误类型应为 *RejectError，实际 %T", err)
	}
	t.Logf("输出: 拒绝，原因:%s", re.Reason)
	if re.Reason != want {
		t.Fatalf("拒绝原因: 得到 %s，期望 %s", re.Reason, want)
	}
}

// 先到较新的正常响应，后到较旧的吊销响应：吊销为终态，证书不可信。
func TestGoodThenOlderRevoked(t *testing.T) {
	clock := newTestClock(at(100))
	c := New(4, WithClock(clock.Now))

	mustSubmit(t, c, Response{Serial: "A", Status: StatusGood, A: at(50), B: at(200)})
	judge(t, c, "A", VerdictGood, "正常记录且当前时刻 100 < b=200")

	// 较旧生效时刻的吊销响应后到：吊销为终态，立即覆盖正常记录。
	mustSubmit(t, c, Response{Serial: "A", Status: StatusRevoked, A: at(10), B: at(300)})
	judge(t, c, "A", VerdictRevoked, "吊销为终态，旧吊销覆盖较新正常")

	rec, _ := c.Record("A")
	if rec.Status != StatusRevoked || !rec.A.Equal(at(10)) {
		t.Fatalf("合并记录应为吊销且 a=10，实际 %s", rec)
	}
}

// 先到吊销，后到更新的正常：吊销不可被任何正常响应恢复。
func TestRevokedThenNewerGood(t *testing.T) {
	clock := newTestClock(at(100))
	c := New(4, WithClock(clock.Now))

	mustSubmit(t, c, Response{Serial: "A", Status: StatusRevoked, A: at(20), B: at(300)})
	judge(t, c, "A", VerdictRevoked, "已吊销")

	mustSubmit(t, c, Response{Serial: "A", Status: StatusGood, A: at(90), B: at(400)})
	judge(t, c, "A", VerdictRevoked, "吊销为终态，更新的正常响应不改变记录")

	// 更早生效的吊销响应会把 a 改小。
	mustSubmit(t, c, Response{Serial: "A", Status: StatusRevoked, A: at(5), B: at(300)})
	rec, _ := c.Record("A")
	t.Logf("输出: 合并记录 %s", rec)
	if !rec.A.Equal(at(5)) {
		t.Fatalf("更早吊销应把 a 改小为 5，实际 %s", rec)
	}
	judge(t, c, "A", VerdictRevoked, "吊销为终态")
}

// 暂扣被生效时刻更新的正常响应解除。
func TestSuspendedLiftedByNewerGood(t *testing.T) {
	clock := newTestClock(at(100))
	c := New(4, WithClock(clock.Now))

	mustSubmit(t, c, Response{Serial: "A", Status: StatusSuspended, A: at(30), B: at(150)})
	judge(t, c, "A", VerdictSuspended, "暂扣不可信")

	mustSubmit(t, c, Response{Serial: "A", Status: StatusGood, A: at(60), B: at(500)})
	judge(t, c, "A", VerdictGood, "a=60 的正常响应胜过 a=30 的暂扣")
}

// a 相等时暂扣优先于正常，与到达顺序无关。
func TestEqualASuspendedWins(t *testing.T) {
	for _, order := range []string{"正常先到", "暂扣先到"} {
		t.Run(order, func(t *testing.T) {
			clock := newTestClock(at(100))
			c := New(4, WithClock(clock.Now))
			good := Response{Serial: "A", Status: StatusGood, A: at(40), B: at(200)}
			susp := Response{Serial: "A", Status: StatusSuspended, A: at(40), B: at(300)}
			if order == "正常先到" {
				mustSubmit(t, c, good)
				mustSubmit(t, c, susp)
			} else {
				mustSubmit(t, c, susp)
				mustSubmit(t, c, good)
			}
			judge(t, c, "A", VerdictSuspended, "a 相等时暂扣胜正常")
			rec, _ := c.Record("A")
			if !rec.B.Equal(at(300)) {
				t.Fatalf("b 应取胜出响应（暂扣）自身的 b=300，实际 %s", rec)
			}
		})
	}
}

// 状态与 a 都相同时取较小的 b。
func TestSameStatusSameASmallerBWins(t *testing.T) {
	clock := newTestClock(at(100))
	c := New(4, WithClock(clock.Now))

	mustSubmit(t, c, Response{Serial: "A", Status: StatusGood, A: at(40), B: at(300)})
	mustSubmit(t, c, Response{Serial: "A", Status: StatusGood, A: at(40), B: at(200)})
	rec, _ := c.Record("A")
	t.Logf("输出: 合并记录 %s", rec)
	if !rec.B.Equal(at(200)) {
		t.Fatalf("状态与 a 相同应取较小 b=200，实际 %s", rec)
	}
}

// 恰在 b 时刻判定：正常记录已过期。
func TestExpiredExactlyAtB(t *testing.T) {
	clock := newTestClock(at(100))
	c := New(4, WithClock(clock.Now))

	mustSubmit(t, c, Response{Serial: "A", Status: StatusGood, A: at(40), B: at(200)})

	clock.Set(at(199))
	judge(t, c, "A", VerdictGood, "当前时刻 199 < b=200，仍可信")

	clock.Set(at(200))
	judge(t, c, "A", VerdictExpired, "当前时刻等于 b=200，状态已过期")
}

// 无记录时报未知。
func TestUnknownSerial(t *testing.T) {
	c := New(4, WithClock(newTestClock(at(100)).Now))
	judge(t, c, "不存在", VerdictUnknown, "无记录")
}

// 容量满时：吊销与暂扣记录不被淘汰，新证书被拒绝；
// 只有已过期的正常记录可被淘汰（取 b 最小者，并列取序号字典序小）。
func TestEviction(t *testing.T) {
	clock := newTestClock(at(100))
	c := New(3, WithClock(clock.Now))

	mustSubmit(t, c, Response{Serial: "revoked", Status: StatusRevoked, A: at(10), B: at(50)})
	mustSubmit(t, c, Response{Serial: "suspended", Status: StatusSuspended, A: at(10), B: at(60)})
	mustSubmit(t, c, Response{Serial: "good-live", Status: StatusGood, A: at(10), B: at(500)})

	// 三条记录都不可淘汰（吊销、暂扣、未过期正常），新证书被拒绝。
	rejectReason(t, c, Response{Serial: "new", Status: StatusGood, A: at(10), B: at(100)}, RejectCacheFull)
	if c.Len() != 3 {
		t.Fatalf("拒绝后缓存数量应保持 3，实际 %d", c.Len())
	}
	judge(t, c, "revoked", VerdictRevoked, "吊销记录不被淘汰")
	judge(t, c, "suspended", VerdictSuspended, "暂扣记录不被淘汰")
	judge(t, c, "good-live", VerdictGood, "未过期正常记录不被淘汰")

	// 已有记录的证书在容量满时仍可合并。
	mustSubmit(t, c, Response{Serial: "good-live", Status: StatusGood, A: at(20), B: at(600)})
	rec, _ := c.Record("good-live")
	if !rec.A.Equal(at(20)) {
		t.Fatalf("容量满时已有证书应仍可合并，实际 %s", rec)
	}

	// 让 good-live 过期，并加入另一条更晚过期的正常记录，
	// 淘汰应选 b 最小（即最早过期）者。
	mustSubmit(t, c, Response{Serial: "good-live", Status: StatusGood, A: at(30), B: at(150)})
	clock.Set(at(160)) // good-live 已过期（b=150）
	mustSubmit(t, c, Response{Serial: "new", Status: StatusGood, A: at(100), B: at(300)})
	if _, ok := c.Record("good-live"); ok {
		t.Fatal("应淘汰 b 最小的已过期正常记录 good-live")
	}
	judge(t, c, "new", VerdictGood, "淘汰过期正常记录后新证书入库")
	judge(t, c, "revoked", VerdictRevoked, "吊销记录仍不被淘汰")
	judge(t, c, "suspended", VerdictSuspended, "暂扣记录仍不被淘汰")
}

// 并列 b 时淘汰序号字典序较小者。
func TestEvictionTieBreakBySerial(t *testing.T) {
	clock := newTestClock(at(100))
	c := New(2, WithClock(clock.Now))

	mustSubmit(t, c, Response{Serial: "b-serial", Status: StatusGood, A: at(10), B: at(50)})
	mustSubmit(t, c, Response{Serial: "a-serial", Status: StatusGood, A: at(10), B: at(50)})
	clock.Set(at(60)) // 两条都已过期且 b 相同

	mustSubmit(t, c, Response{Serial: "new", Status: StatusGood, A: at(55), B: at(300)})
	if _, ok := c.Record("a-serial"); ok {
		t.Fatal("b 并列时应淘汰序号字典序较小的 a-serial")
	}
	if _, ok := c.Record("b-serial"); !ok {
		t.Fatal("b-serial 应被保留")
	}
}

// 拒绝原因按固定顺序只报第一个，且被拒绝的响应不改变任何记录。
func TestRejectOrder(t *testing.T) {
	clock := newTestClock(at(100))
	c := New(1, WithClock(clock.Now))
	mustSubmit(t, c, Response{Serial: "existing", Status: StatusGood, A: at(10), B: at(500)})

	future := Response{Serial: "x", Status: StatusGood, A: at(200), B: at(100)} // a>=b 且 a 在未来
	future.Serial = ""
	rejectReason(t, c, future, RejectEmptySerial) // 空序号优先于其他一切原因

	bad := Response{Serial: "x", Status: Status(99), A: at(200), B: at(100)}
	rejectReason(t, c, bad, RejectInvalidStatus) // 非法状态优先于区间与未来检查

	inv := Response{Serial: "x", Status: StatusGood, A: at(200), B: at(100)}
	rejectReason(t, c, inv, RejectInvalidInterval) // a>=b 优先于未来检查

	fut := Response{Serial: "x", Status: StatusGood, A: at(200), B: at(300)}
	rejectReason(t, c, fut, RejectFutureResponse) // a 晚于当前时刻

	// 容量满排在最后：合法但缓存已满的新证书。
	rejectReason(t, c, Response{Serial: "new", Status: StatusGood, A: at(10), B: at(300)}, RejectCacheFull)

	// 被拒绝的响应不得改变任何记录。
	if c.Len() != 1 {
		t.Fatalf("被拒绝的响应不应入库，当前数量 %d", c.Len())
	}
	rec, _ := c.Record("existing")
	if rec.Status != StatusGood || !rec.A.Equal(at(10)) || !rec.B.Equal(at(500)) {
		t.Fatalf("被拒绝的响应不应改变已有记录，实际 %s", rec)
	}
	if _, ok := c.Record("x"); ok {
		t.Fatal("被拒绝的响应不应留下记录")
	}
}

// 同一批响应的任意到达顺序（容量足够时）得到完全相同的合并结果。
func TestPermutationConsistency(t *testing.T) {
	batch := []Response{
		{Serial: "A", Status: StatusGood, A: at(80), B: at(300)},
		{Serial: "A", Status: StatusRevoked, A: at(20), B: at(100)},
		{Serial: "A", Status: StatusGood, A: at(90), B: at(400)},
		{Serial: "B", Status: StatusSuspended, A: at(50), B: at(200)},
		{Serial: "B", Status: StatusGood, A: at(50), B: at(250)},
		{Serial: "B", Status: StatusGood, A: at(70), B: at(260)},
	}
	serials := []string{"A", "B"}

	want := map[string]Record{
		"A": {Status: StatusRevoked, A: at(20), B: at(100)},
		"B": {Status: StatusGood, A: at(70), B: at(260)},
	}

	perm := make([]int, len(batch))
	for i := range perm {
		perm[i] = i
	}
	count := 0
	var visit func(k int)
	visit = func(k int) {
		if k == len(perm) {
			count++
			clock := newTestClock(at(100))
			c := New(10, WithClock(clock.Now))
			for _, idx := range perm {
				if err := c.Submit(batch[idx]); err != nil {
					t.Fatalf("排列 %v 中响应 %s 被意外拒绝: %v", perm, batch[idx], err)
				}
			}
			for _, serial := range serials {
				got, ok := c.Record(serial)
				if !ok || got != want[serial] {
					t.Fatalf("排列 %v 合并结果不一致: 序号 %s 得到 %v(存在:%v)，期望 %v",
						perm, serial, got, ok, want[serial])
				}
			}
			return
		}
		for i := k; i < len(perm); i++ {
			perm[k], perm[i] = perm[i], perm[k]
			visit(k + 1)
			perm[k], perm[i] = perm[i], perm[k]
		}
	}
	visit(0)
	t.Logf("输入: %d 条响应的全排列共 %d 种顺序", len(batch), count)
	for serial, rec := range want {
		t.Logf("输出: 序号 %s 合并结果 %s（与到达顺序无关）", serial, rec)
	}
	if count != 720 {
		t.Fatalf("应覆盖 6! = 720 种排列，实际 %d", count)
	}
}

// 并发提交与判定：已吊销的证书之后所有判定都不可信。
func TestConcurrentSubmitAndJudge(t *testing.T) {
	clock := newTestClock(at(100))
	c := New(64, WithClock(clock.Now))

	mustSubmit(t, c, Response{Serial: "hot", Status: StatusRevoked, A: at(10), B: at(50)})

	const workers = 8
	const rounds = 200
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				serial := fmt.Sprintf("cert-%d", (id+i)%16)
				resp := Response{
					Serial: serial,
					Status: Status(int(StatusGood) + (id+i)%3),
					A:      at(10 + (id+i)%50),
					B:      at(500 + (id+i)%100),
				}
				_ = c.Submit(resp)
				_ = c.Judge(serial)
				if v := c.Judge("hot"); v != VerdictRevoked {
					t.Errorf("已吊销证书判定应恒为已吊销，得到 %s", v)
				}
			}
		}(w)
	}
	wg.Wait()
	judge(t, c, "hot", VerdictRevoked, "并发下吊销终态保持")
}
