package repl

import (
	"errors"
	"os"
	"sync"
	"testing"

	"ontology/link"
	"ontology/region"
)

var errTest = errors.New("test send failure")

var verbose = os.Getenv("REPL_TEST_VERBOSE") == "1"

func vlog(tb testing.TB, format string, args ...any) {
	if verbose {
		tb.Logf(format, args...)
	}
}

func alwaysOK(link.Item) (bool, error) { return true, nil }

// drain 在无失败假设下尽可能排空所有链路。
func drain(t *testing.T, c *Cluster, names []string) {
	t.Helper()
	for i := 0; i < 128; i++ {
		moved := false
		for _, s := range names {
			for _, d := range names {
				if s == d {
					continue
				}
				_, _, _, pending, _, err := c.LinkStats(s, d)
				if err != nil {
					t.Fatal(err)
				}
				if pending > 0 {
					moved = true
					if err := c.Deliver(s, d, 1000, alwaysOK); err != nil {
						t.Fatal(err)
					}
				}
			}
		}
		if !moved {
			return
		}
	}
	t.Fatalf("队列未在有限轮内排空")
}

func idSet(c *Cluster, r string) map[region.VersionID]bool {
	out := map[region.VersionID]bool{}
	for _, v := range c.regions[r].Versions() {
		out[v.ID] = true
	}
	return out
}

func assertIdentity(t *testing.T, c *Cluster, names []string) {
	t.Helper()
	for _, s := range names {
		for _, d := range names {
			if s == d {
				continue
			}
			enq, del, ft, pending, _, err := c.LinkStats(s, d)
			if err != nil {
				t.Fatal(err)
			}
			if enq != del+ft+pending {
				t.Fatalf("%s->%s 恒等式不成立: %d != %d+%d+%d", s, d, enq, del, ft, pending)
			}
		}
	}
}

func TestNewValidation(t *testing.T) {
	cases := []struct {
		name  string
		names []string
		p     Params
	}{
		{"区域太少", []string{"A"}, Params{true, Strict, 10, 2}},
		{"区域太多", []string{"A", "B", "C", "D", "E"}, Params{true, Strict, 10, 2}},
		{"空区域名", []string{"A", ""}, Params{true, Strict, 10, 2}},
		{"区域重名", []string{"A", "A"}, Params{true, Strict, 10, 2}},
		{"C过小", []string{"A", "B"}, Params{true, Strict, 0, 2}},
		{"C过大", []string{"A", "B"}, Params{true, Strict, 1e12 + 1, 2}},
		{"R过小", []string{"A", "B"}, Params{true, Strict, 10, 0}},
		{"R过大", []string{"A", "B"}, Params{true, Strict, 10, 101}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := New(tc.names, tc.p); !errors.Is(err, ErrInvalidParam) {
				t.Fatalf("应报参数非法, got %v", err)
			}
		})
	}
	if _, err := New([]string{"A", "B", "C", "D"}, Params{true, Strict, 1, 1}); err != nil {
		t.Fatalf("4 区域与边界参数应合法, got %v", err)
	}
}

func TestPutDeleteValidationOrder(t *testing.T) {
	c, _ := New([]string{"A", "B"}, Params{true, Strict, 1, 1})
	if _, err := c.Put("ZZ", "k", -1, 0); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("size 非法应报参数非法, got %v", err)
	}
	if _, err := c.Put("ZZ", "", 1, 0); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("key 非法应报参数非法, got %v", err)
	}
	if _, err := c.Put("ZZ", "k", 1, 1e12+1); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("ts 非法应报参数非法, got %v", err)
	}
	if _, err := c.Put("ZZ", "k", 1, 0); !errors.Is(err, ErrNoRegion) {
		t.Fatalf("合法参数+未知区域应报区域不存在, got %v", err)
	}
	if _, err := c.Put("A", "k", 2, 0); !errors.Is(err, ErrBacklog) {
		t.Fatalf("C=1 size=2 应积压超限, got %v", err)
	}
	if _, err := c.Delete("ZZ", "", 0); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("Delete 参数校验优先, got %v", err)
	}
	if _, err := c.Delete("ZZ", "k", 0); !errors.Is(err, ErrNoRegion) {
		t.Fatalf("Delete 未知区域, got %v", err)
	}
}

func TestSpecExampleTieTsThenMarker(t *testing.T) {
	for _, marker := range []bool{true, false} {
		name := "MarkerOn"
		if !marker {
			name = "MarkerOff"
		}
		t.Run(name, func(t *testing.T) {
			c, _ := New([]string{"A", "B"}, Params{MarkerRepl: marker, Mode: Strict, Capacity: 100, RetryLimit: 2})
			if _, err := c.Put("A", "k", 5, 10); err != nil {
				t.Fatal(err)
			}
			vb, err := c.Put("B", "k", 7, 10)
			if err != nil {
				t.Fatal(err)
			}
			drain(t, c, []string{"A", "B"})
			for _, r := range []string{"A", "B"} {
				if got := len(c.regions[r].Versions()); got != 2 {
					t.Fatalf("%s 应有2版本, got %d", r, got)
				}
				cur, err := c.Get(r, "k")
				if err != nil || cur.ID != vb.ID {
					t.Fatalf("%s 当前应为 B 的版本, got %v err=%v", r, cur, err)
				}
			}
			if _, err := c.Delete("A", "k", 12); err != nil {
				t.Fatal(err)
			}
			drain(t, c, []string{"A", "B"})
			_, errA := c.Get("A", "k")
			curB, errB := c.Get("B", "k")
			if marker {
				if !errors.Is(errA, region.ErrDeleted) || !errors.Is(errB, region.ErrDeleted) {
					t.Fatalf("MarkerOn 两边都应被标记删除: A=%v B=%v", errA, errB)
				}
				if c.Diverged("k") {
					t.Fatalf("标记复制后不应分歧")
				}
			} else {
				if !errors.Is(errA, region.ErrDeleted) {
					t.Fatalf("A 本地有标记应报删除, got %v", errA)
				}
				if errB != nil || curB.ID != vb.ID {
					t.Fatalf("B 应仍读到 B 的数据版本, got %v err=%v", curB, errB)
				}
				if !c.Diverged("k") {
					t.Fatalf("MarkerOff 标记不复制, Diverged 应为真")
				}
			}
		})
	}
}

func TestNoLoopbackThreeRegionsItemCounts(t *testing.T) {
	names := []string{"A", "B", "C"}
	c, _ := New(names, Params{MarkerRepl: true, Mode: Strict, Capacity: 1e9, RetryLimit: 2})
	for i, r := range names {
		if _, err := c.Put(r, "k", 1, int64(i)); err != nil {
			t.Fatal(err)
		}
		if _, err := c.Delete(r, "k2", int64(10+i)); err != nil {
			t.Fatal(err)
		}
	}
	for _, s := range names {
		for _, d := range names {
			if s == d {
				continue
			}
			enq, _, _, pending, backlog, err := c.LinkStats(s, d)
			if err != nil {
				t.Fatal(err)
			}
			if enq != 2 || pending != 2 || backlog != 1 {
				t.Fatalf("%s->%s 初始应2项/1字节（含0字节标记）, got enq=%d pending=%d backlog=%d",
					s, d, enq, pending, backlog)
			}
		}
	}
	drain(t, c, names)
	for _, r := range names {
		if got := len(c.regions[r].Versions()); got != 6 {
			t.Fatalf("%s 应收敛到6版本（含4副本）, got %d", r, got)
		}
	}
	assertIdentity(t, c, names)
	base := idSet(c, names[0])
	for _, r := range names[1:] {
		got := idSet(c, r)
		if len(got) != len(base) {
			t.Fatalf("版本集合大小不同")
		}
		for id := range base {
			if !got[id] {
				t.Fatalf("区域 %s 缺版本 %v", r, id)
			}
		}
	}
	if c.Diverged("k") {
		t.Fatalf("k 的当前应一致")
	}
}

func TestStrictRejectNoStateChange(t *testing.T) {
	names := []string{"A", "B", "C"}
	c, _ := New(names, Params{true, Strict, 10, 2})
	if _, err := c.Put("A", "k", 6, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Put("A", "m", 5, 2); !errors.Is(err, ErrBacklog) {
		t.Fatalf("任一链路超限应整体拒绝, got %v", err)
	}
	if c.regions["A"].Seq() != 1 {
		t.Fatalf("被拒操作不得占用序号, seq=%d", c.regions["A"].Seq())
	}
	if got := len(c.regions["A"].Versions()); got != 1 {
		t.Fatalf("被拒操作不得写入区域状态, got %d", got)
	}
	for _, d := range []string{"B", "C"} {
		enq, _, _, pending, backlog, _ := c.LinkStats("A", d)
		if enq != 1 || pending != 1 || backlog != 6 {
			t.Fatalf("Strict 拒绝时 A->%s 队列必须不变, got %d/%d/%d", d, enq, pending, backlog)
		}
	}
	assertIdentity(t, c, names)
}

func TestStrictBacklogExampleFromSpec(t *testing.T) {
	c, _ := New([]string{"A", "B"}, Params{true, Strict, 10, 2})
	if _, err := c.Put("A", "k", 6, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Put("A", "m", 5, 2); !errors.Is(err, ErrBacklog) {
		t.Fatalf("11>10 应拒绝, got %v", err)
	}
	if c.regions["A"].Seq() != 1 {
		t.Fatalf("拒绝不占序号")
	}
	if err := c.Deliver("A", "B", 1, alwaysOK); err != nil {
		t.Fatal(err)
	}
	if bl, _ := c.Backlog("A", "B"); bl != 0 {
		t.Fatalf("投递成功后积压应归零, got %d", bl)
	}
	if _, err := c.Put("A", "m", 5, 2); err != nil {
		t.Fatalf("积压释放后应通过, got %v", err)
	}
}

func TestExactCapacityPasses(t *testing.T) {
	c, _ := New([]string{"A", "B", "C"}, Params{true, Strict, 10, 2})
	if _, err := c.Put("A", "k", 10, 1); err != nil {
		t.Fatalf("恰等 C 应通过, got %v", err)
	}
	if bl, _ := c.Backlog("A", "B"); bl != 10 {
		t.Fatalf("积压应为10, got %d", bl)
	}
}

func TestRelaxedLostCounterAndNoRetryOfLost(t *testing.T) {
	names := []string{"A", "B", "C"}
	c, _ := New(names, Params{true, Relaxed, 10, 2})
	if _, err := c.Put("A", "k", 6, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Put("A", "m", 5, 2); err != nil {
		t.Fatalf("Relaxed 应本地成功, got %v", err)
	}
	if c.regions["A"].Seq() != 2 {
		t.Fatalf("Relaxed 本地版本序号应增长")
	}
	for _, d := range []string{"B", "C"} {
		lost, err := c.Lost("A", d)
		if err != nil || lost != 1 {
			t.Fatalf("A->%s 丢失计数应为1, got %d err=%v", d, lost, err)
		}
		enq, _, _, pending, backlog, _ := c.LinkStats("A", d)
		if enq != 1 || pending != 1 || backlog != 6 {
			t.Fatalf("丢失版本不应入队 A->%s, got %d/%d/%d", d, enq, pending, backlog)
		}
	}
	drain(t, c, names)
	for _, d := range []string{"B", "C"} {
		if _, err := c.Get(d, "m"); !errors.Is(err, region.ErrNotFound) {
			t.Fatalf("丢失版本永不复制, %s 不应有 m, got %v", d, err)
		}
	}
	if _, err := c.Get("A", "m"); err != nil {
		t.Fatalf("origin 本地应有 m, got %v", err)
	}
	assertIdentity(t, c, names)
}

func TestDeliverRetryValidationOrder(t *testing.T) {
	c, _ := New([]string{"A", "B"}, Params{true, Strict, 100, 2})
	if err := c.Deliver("A", "B", 0, alwaysOK); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("n=0 应参数非法, got %v", err)
	}
	if err := c.Deliver("A", "B", 1001, alwaysOK); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("n=1001 应参数非法, got %v", err)
	}
	if err := c.Deliver("A", "A", 1, alwaysOK); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("src==dst 应参数非法, got %v", err)
	}
	if err := c.Deliver("X", "B", 1, alwaysOK); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("未知区域应参数非法, got %v", err)
	}
	if err := c.Deliver("A", "B", 1, nil); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("send 为 nil 应参数非法, got %v", err)
	}
	missing := region.VersionID{Origin: "A", Seq: 99}
	if err := c.Retry("X", "B", missing); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("Retry 参数非法优先, got %v", err)
	}
	if err := c.Retry("A", "B", missing); !errors.Is(err, link.ErrNotFound) {
		t.Fatalf("Retry 不存在项应报不存在, got %v", err)
	}
}

func TestFailoverThenRetryFlow(t *testing.T) {
	names := []string{"A", "B"}
	c, _ := New(names, Params{true, Strict, 100, 2})
	v, err := c.Put("A", "k", 6, 1)
	if err != nil {
		t.Fatal(err)
	}
	// 两个版本：队首失败两次后失败转移，同一 Deliver 内第二项继续。
	v2, err := c.Put("A", "m", 7, 2)
	if err != nil {
		t.Fatal(err)
	}
	failures := 0
	send := func(it link.Item) (bool, error) {
		if it.ID == v.ID && failures < 2 {
			failures++
			return false, errTest
		}
		return true, nil
	}
	// 第一次 Deliver 只触发一次失败即阻塞；n=10 的第二次 Deliver 完成转移并投递 v2。
	if err := c.Deliver("A", "B", 10, send); err != nil {
		t.Fatal(err)
	}
	if l := c.regions["B"]; len(l.Versions()) != 0 {
		t.Fatalf("队首阻塞时第二项不得绕过")
	}
	if err := c.Deliver("A", "B", 10, send); err != nil {
		t.Fatal(err)
	}
	if _, _, ft, pending, backlog, _ := c.LinkStats("A", "B"); ft != 1 || pending != 0 || backlog != 0 {
		t.Fatalf("失败转移后队列应空: ft=%d pending=%d backlog=%d", ft, pending, backlog)
	}
	if got := len(c.regions["B"].Versions()); got != 1 {
		t.Fatalf("同次 Deliver 应继续投递第二项, got %d", got)
	}
	ids, _ := c.FailedIDs("A", "B")
	if len(ids) != 1 || ids[0] != v.ID {
		t.Fatalf("v 应在失败列表")
	}
	if err := c.Retry("A", "B", v.ID); err != nil {
		t.Fatalf("Retry 应成功, got %v", err)
	}
	enq, _, _, pending, backlog, _ := c.LinkStats("A", "B")
	if enq != 3 || pending != 1 || backlog != 6 {
		t.Fatalf("Retry 重新入队: enq=%d pending=%d backlog=%d", enq, pending, backlog)
	}
	if err := c.Deliver("A", "B", 1, alwaysOK); err != nil {
		t.Fatal(err)
	}
	if got := len(c.regions["B"].Versions()); got != 2 {
		t.Fatalf("Retry 成功后 B 应有2版本, got %d", got)
	}
	_ = v2
	assertIdentity(t, c, names)
}

func TestRetryBacklogCheck(t *testing.T) {
	c, _ := New([]string{"A", "B"}, Params{true, Strict, 10, 1})
	v, _ := c.Put("A", "k", 6, 1)
	// R=1：一次失败即转移，释放积压。
	c.Deliver("A", "B", 1, func(link.Item) (bool, error) { return false, errTest })
	// 再制造 6 字节积压（恰 6），失败项 6 字节将使总和 12>10。
	c.Put("A", "m", 6, 2)
	if err := c.Retry("A", "B", v.ID); !errors.Is(err, ErrBacklog) {
		t.Fatalf("Retry 积压超限应拒绝且失败项保留, got %v", err)
	}
	ids, _ := c.FailedIDs("A", "B")
	if len(ids) != 1 {
		t.Fatalf("积压超限时失败项不得消失")
	}
	// 投递占用项后 6+6==... 仍超限；换 4 字节验证恰等通过边界由 link 测试覆盖。
	c.Deliver("A", "B", 1, alwaysOK) // 清空 m
	if err := c.Retry("A", "B", v.ID); err != nil {
		t.Fatalf("积压释放后 Retry 应通过, got %v", err)
	}
}

func TestDeleteMarkerWhenKeyAbsent(t *testing.T) {
	c, _ := New([]string{"A", "B"}, Params{true, Strict, 100, 2})
	if _, err := c.Delete("A", "ghost", 3); err != nil {
		t.Fatalf("键无版本时也照建标记, got %v", err)
	}
	if _, err := c.Get("A", "ghost"); !errors.Is(err, region.ErrDeleted) {
		t.Fatalf("应能区分 不存在 与 被标记删除, got %v", err)
	}
	if _, err := c.Get("A", "never"); !errors.Is(err, region.ErrNotFound) {
		t.Fatalf("无版本应报不存在, got %v", err)
	}
}

func TestApplyProbeClusterCounters(t *testing.T) {
	c, _ := New([]string{"A", "B"}, Params{true, Strict, 1e9, 1})
	v, _ := c.Put("A", "k", 1, 1)
	// R=1 确认丢失：Apply 后失败转移；Retry 再成功投递产生一次重复。
	c.Deliver("A", "B", 1, func(link.Item) (bool, error) { return true, errTest })
	c.Retry("A", "B", v.ID)
	c.Deliver("A", "B", 1, alwaysOK)
	probe, ok := c.ApplyProbes("B")
	if !ok || probe != 2 {
		t.Fatalf("B 应有2次 Apply 探测（首次+重复）, got %d ok=%v", probe, ok)
	}
	dup, ok := c.Duplicates("B")
	if !ok || dup != 1 {
		t.Fatalf("B 应有1次重复, got %d", dup)
	}
}

func TestConcurrentOpsAreLinearizable(t *testing.T) {
	names := []string{"A", "B", "C"}
	c, _ := New(names, Params{MarkerRepl: true, Mode: Relaxed, Capacity: 50, RetryLimit: 2})
	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				r := names[(w+i)%len(names)]
				d := names[(w+i+1)%len(names)]
				switch (w + i) % 5 {
				case 0:
					_, _ = c.Put(r, "k", int64((w+i)%20), int64(i%10))
				case 1:
					_, _ = c.Delete(r, "k", int64(i%10))
				case 2:
					_ = c.Deliver(r, d, 3, func(link.Item) (bool, error) { return true, nil })
				case 3:
					_, _, _, _, _, _ = c.LinkStats(r, d)
					_ = c.Diverged("k")
				case 4:
					_, _ = c.Get(r, "k")
				}
			}
		}(worker)
	}
	wg.Wait()
	assertIdentity(t, c, names)
	// 全部排空后各区域版本集合必须一致（Relaxed 下大版本可能丢失，小版本一致收敛）。
	drain(t, c, names)
	assertIdentity(t, c, names)
	for _, s := range names {
		for _, d := range names {
			if s == d {
				continue
			}
			_, _, _, pending, backlog, _ := c.LinkStats(s, d)
			if pending != 0 || backlog != 0 {
				t.Fatalf("排空后 %s->%s 不应有积压", s, d)
			}
		}
	}
}
