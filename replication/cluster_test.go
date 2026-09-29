package replication

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
)

// entries 构造 [from,to) 位点、同一世代与领导者的日志条目。
func entries(epoch, from, to int, leader string) []LogEntry {
	out := make([]LogEntry, 0, to-from)
	for i := from; i < to; i++ {
		out = append(out, LogEntry{Epoch: epoch, ID: int64(i + 1), Leader: leader})
	}
	return out
}

func concat(parts ...[]LogEntry) []LogEntry {
	var out []LogEntry
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// diverge 把 entries 的尾部条目标识改掉，制造分叉。
func diverge(logs []LogEntry, from int) []LogEntry {
	out := append([]LogEntry(nil), logs...)
	for i := from; i < len(out); i++ {
		out[i].ID += 1000
	}
	return out
}

func mustAdd(t *testing.T, c *Cluster, id string, log []LogEntry, epochs []EpochMark) {
	t.Helper()
	if err := c.AddReplica(id, log, epochs); err != nil {
		t.Fatalf("AddReplica(%s): %v", id, err)
	}
}

// 领导者: 世代0 [0,3) 世代1 [3,6) 世代2 [6,9)
func leaderLog() ([]LogEntry, []EpochMark) {
	l := concat(entries(0, 0, 3, "L"), entries(1, 3, 6, "L"), entries(2, 6, 9, "L"))
	return l, []EpochMark{{0, 0}, {1, 3}, {2, 6}}
}

// 跟随者: 与领导者共享 [0,6)，之后是旧领导者留下的分叉残留：
// 世代3 [6,9) 与世代4 [9,11)，领导者都不知道这两个世代。
func followerLog() ([]LogEntry, []EpochMark) {
	shared := concat(entries(0, 0, 3, "L"), entries(1, 3, 6, "L"))
	forked := diverge(entries(3, 6, 9, "L"), 6)
	extra := diverge(entries(4, 9, 11, "L"), 9)
	return concat(shared, forked, extra), []EpochMark{{0, 0}, {1, 3}, {3, 6}, {4, 9}}
}

func TestTruncateRoundsConvergeToNaiveLCP(t *testing.T) {
	c := NewCluster()
	ll, le := leaderLog()
	fl, fe := followerLog()
	mustAdd(t, c, "L", ll, le)
	mustAdd(t, c, "F", fl, fe)

	want := CommonPrefix(ll, fl) // 6
	rounds := 0
	for {
		cut, truncated, err := c.TruncateRound("L", "F")
		if err != nil {
			t.Fatalf("TruncateRound: %v", err)
		}
		t.Logf("round=%d cut=%d truncated=%v", rounds, cut, truncated)
		rounds++
		if !truncated {
			if cut != want {
				t.Fatalf("收敛截断点=%d, 朴素最长公共前缀=%d", cut, want)
			}
			break
		}
		if rounds > 8 {
			t.Fatal("截断轮次过多，未收敛")
		}
	}
	if got := c.Replica("F").End(); got != want {
		t.Fatalf("截断后跟随者结束位点=%d, 期望=%d", got, want)
	}
	// 世代缓存中不得残留起始位点 >= 截断点的表项。
	for _, m := range c.Replica("F").EpochCache() {
		if m.Start >= want {
			t.Fatalf("世代缓存残留分叉表项: %+v", m)
		}
	}
	// 截断只删分叉尾部：前缀与领导者逐位点一致。
	fl2 := c.Replica("F").Log()
	if !reflect.DeepEqual(fl2, ll[:want]) {
		t.Fatalf("截断后前缀与领导者不一致: %v", fl2)
	}
}

func TestRecoverBitIdenticalAndIdempotent(t *testing.T) {
	c := NewCluster()
	ll, le := leaderLog()
	fl, fe := followerLog()
	mustAdd(t, c, "L", ll, le)
	mustAdd(t, c, "F", fl, fe)

	cut, copied, err := c.Recover("L", "F")
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if want := CommonPrefix(ll, fl); cut != want {
		t.Fatalf("截断点=%d, 朴素最长公共前缀=%d", cut, want)
	}
	if copied != len(ll)-cut {
		t.Fatalf("复制条数=%d, 期望=%d", copied, len(ll)-cut)
	}
	if got := c.Replica("F").Log(); !reflect.DeepEqual(got, ll) {
		t.Fatalf("恢复后未逐位点一致: %v", got)
	}
	if got := c.Replica("F").EpochCache(); !reflect.DeepEqual(got, le) {
		t.Fatalf("恢复后世代缓存与领导者不一致: %v", got)
	}
	// 再恢复为幂等空操作。
	cut2, copied2, err := c.Recover("L", "F")
	if err != nil {
		t.Fatalf("再次 Recover: %v", err)
	}
	if copied2 != 0 || cut2 != len(ll) {
		t.Fatalf("再恢复非幂等: cut=%d copied=%d", cut2, copied2)
	}
}

func TestEpochResponseRule(t *testing.T) {
	c := NewCluster()
	ll, le := leaderLog()
	mustAdd(t, c, "L", ll, le)

	cases := []struct {
		epoch, want int
		known       bool
	}{
		{0, 3, true}, {1, 6, true}, {2, 9, true}, // 已知世代：下一世代起始位点或日志末尾
		{5, 0, false}, // 比最新世代新：未知，跟随者应截掉整个未知世代
	}
	for _, tc := range cases {
		end, known, err := c.EpochResponse("L", tc.epoch)
		if err != nil {
			t.Fatalf("EpochResponse(%d): %v", tc.epoch, err)
		}
		if end != tc.want || known != tc.known {
			t.Fatalf("EpochResponse(%d)=(%d,%v), 期望=(%d,%v)", tc.epoch, end, known, tc.want, tc.known)
		}
	}
	if _, _, err := c.EpochResponse("L", -1); !errors.Is(err, ErrUnknownEpoch()) {
		t.Fatalf("请求未知世代应报 ErrUnknownEpoch, 实际 %v", err)
	}
	if _, _, err := c.EpochResponse("nobody", 0); !errors.Is(err, ErrUnknownReplica()) {
		t.Fatalf("未知副本应报 ErrUnknownReplica, 实际 %v", err)
	}
}

func snapshot(t *testing.T, c *Cluster, ids ...string) map[string]struct {
	log    []LogEntry
	epochs []EpochMark
} {
	t.Helper()
	out := map[string]struct {
		log    []LogEntry
		epochs []EpochMark
	}{}
	for _, id := range ids {
		r := c.Replica(id)
		out[id] = struct {
			log    []LogEntry
			epochs []EpochMark
		}{r.Log(), r.EpochCache()}
	}
	return out
}

func TestRejectionsAreDistinctAndLeaveNoTrace(t *testing.T) {
	newCluster := func(t *testing.T) *Cluster {
		c := NewCluster()
		ll, le := leaderLog()
		mustAdd(t, c, "L", ll, le)
		mustAdd(t, c, "F", ll, le)
		return c
	}

	t.Run("invalid-argument", func(t *testing.T) {
		c := newCluster(t)
		before := snapshot(t, c, "L", "F")
		ops := []func() error{
			func() error { return c.AddReplica("", nil, nil) },
			func() error { return c.AddReplica("L", nil, nil) }, // 重复 id
			func() error {
				return c.AddReplica("X", entries(0, 0, 2, "X"), []EpochMark{{1, 0}}) // 条目世代不在缓存
			},
			func() error { return c.BecomeLeader("L", 1) },                                      // 世代不增
			func() error { return c.AppendLeader("L", LogEntry{Epoch: 2, ID: 1, Leader: "F"}) }, // 冒名
			func() error { return c.AppendLeader("L", LogEntry{Epoch: 0, ID: 1, Leader: "L"}) }, // 旧世代
			func() error { _, _, err := c.Recover("L", "L"); return err },                       // 自我恢复
		}
		for i, op := range ops {
			if err := op(); !errors.Is(err, ErrInvalidArgument()) {
				t.Fatalf("op%d 应报 ErrInvalidArgument, 实际 %v", i, err)
			}
		}
		assertUnchanged(t, c, before, "L", "F")
	})

	t.Run("unknown-replica", func(t *testing.T) {
		c := newCluster(t)
		before := snapshot(t, c, "L", "F")
		if _, _, err := c.Recover("ghost", "F"); !errors.Is(err, ErrUnknownReplica()) {
			t.Fatalf("应报 ErrUnknownReplica, 实际 %v", err)
		}
		if _, _, err := c.Recover("L", "ghost"); !errors.Is(err, ErrUnknownReplica()) {
			t.Fatalf("应报 ErrUnknownReplica, 实际 %v", err)
		}
		if _, _, err := c.TruncateRound("L", "ghost"); !errors.Is(err, ErrUnknownReplica()) {
			t.Fatalf("应报 ErrUnknownReplica, 实际 %v", err)
		}
		assertUnchanged(t, c, before, "L", "F")
	})

	t.Run("unknown-epoch", func(t *testing.T) {
		c := newCluster(t)
		before := snapshot(t, c, "L", "F")
		if _, _, err := c.EpochResponse("L", -3); !errors.Is(err, ErrUnknownEpoch()) {
			t.Fatalf("应报 ErrUnknownEpoch, 实际 %v", err)
		}
		assertUnchanged(t, c, before, "L", "F")
	})

	t.Run("log-diverged", func(t *testing.T) {
		c := NewCluster()
		ll, le := leaderLog()
		mustAdd(t, c, "L", ll, le)
		// 与领导者同长但中间位点分叉：前缀校验必须整体拒绝。
		mustAdd(t, c, "F", diverge(ll, 4), le)
		before := snapshot(t, c, "L", "F")
		if _, _, err := c.Recover("L", "F"); !errors.Is(err, ErrLogDiverged()) {
			t.Fatalf("应报 ErrLogDiverged, 实际 %v", err)
		}
		assertUnchanged(t, c, before, "L", "F")
	})

	t.Run("distinct", func(t *testing.T) {
		errs := []error{ErrInvalidArgument(), ErrUnknownReplica(), ErrUnknownEpoch(), ErrLogDiverged()}
		for i := range errs {
			for j := range errs {
				if i != j && errors.Is(errs[i], errs[j]) {
					t.Fatalf("错误类别 %d 与 %d 不可区分", i, j)
				}
			}
		}
	})
}

func assertUnchanged(t *testing.T, c *Cluster, before map[string]struct {
	log    []LogEntry
	epochs []EpochMark
}, ids ...string) {
	t.Helper()
	after := snapshot(t, c, ids...)
	for _, id := range ids {
		if !reflect.DeepEqual(before[id], after[id]) {
			t.Fatalf("拒绝后副本 %s 状态被改变: before=%+v after=%+v", id, before[id], after[id])
		}
	}
}

func TestBecomeLeaderAndAppendAdvanceEpochCache(t *testing.T) {
	c := NewCluster()
	ll, le := leaderLog()
	mustAdd(t, c, "L", ll, le)
	mustAdd(t, c, "F", ll, le)

	if err := c.BecomeLeader("F", 3); err != nil {
		t.Fatalf("BecomeLeader: %v", err)
	}
	if got := c.Replica("F").EpochCache(); !reflect.DeepEqual(got, append(le, EpochMark{3, 9})) {
		t.Fatalf("当选后世代缓存错误: %v", got)
	}
	if err := c.AppendLeader("F", LogEntry{Epoch: 3, ID: 10, Leader: "F"}); err != nil {
		t.Fatalf("AppendLeader: %v", err)
	}
	// 新世代消息写入会推进世代缓存。
	if err := c.AppendLeader("F", LogEntry{Epoch: 4, ID: 11, Leader: "F"}); err != nil {
		t.Fatalf("AppendLeader 新世代: %v", err)
	}
	want := append(append(le, EpochMark{3, 9}), EpochMark{4, 10})
	if got := c.Replica("F").EpochCache(); !reflect.DeepEqual(got, want) {
		t.Fatalf("写入新世代后缓存错误: got=%v want=%v", got, want)
	}
	// 世代响应反映新世代边界。
	if end, known, err := c.EpochResponse("F", 3); err != nil || end != 10 || !known {
		t.Fatalf("EpochResponse(3)=(%d,%v),%v 期望 (10,true),nil", end, known, err)
	}
}

func TestConcurrentEpochResponseLogQueryAndRecover(t *testing.T) {
	c := NewCluster()
	ll, le := leaderLog()
	mustAdd(t, c, "L", ll, le)
	followers := []string{"F1", "F2", "F3", "F4"}
	for _, f := range followers {
		fl, fe := followerLog()
		mustAdd(t, c, f, fl, fe)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 64)
	// 多执行体并发：世代响应、日志查询、对不同跟随者的恢复复制。
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(epoch int) {
			defer wg.Done()
			if _, _, err := c.EpochResponse("L", epoch); err != nil {
				errs <- fmt.Errorf("EpochResponse: %w", err)
			}
		}(i)
	}
	for _, f := range followers {
		wg.Add(2)
		go func(id string) {
			defer wg.Done()
			if _, _, err := c.Recover("L", id); err != nil {
				errs <- fmt.Errorf("Recover(%s): %w", id, err)
			}
		}(f)
		go func(id string) {
			defer wg.Done()
			_ = c.Replica(id).Log()
			_ = c.Replica(id).End()
			_ = c.Replica(id).EpochCache()
		}(f)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	// 并发恢复后每个跟随者都与领导者逐位点一致，且再恢复幂等。
	for _, f := range followers {
		if got := c.Replica(f).Log(); !reflect.DeepEqual(got, ll) {
			t.Fatalf("%s 恢复后不一致: %v", f, got)
		}
		if _, copied, err := c.Recover("L", f); err != nil || copied != 0 {
			t.Fatalf("%s 再恢复非幂等: copied=%d err=%v", f, copied, err)
		}
	}
}
