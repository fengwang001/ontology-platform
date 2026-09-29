package replication

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

func testCluster(t *testing.T) (*Cluster, *bytes.Buffer) {
	t.Helper()
	buf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(io.MultiWriter(buf, testLogWriter{t}), &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	return NewCluster(logger), buf
}

type testLogWriter struct{ t *testing.T }

func (w testLogWriter) Write(p []byte) (int, error) {
	w.t.Log(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}

func mustAdd(t *testing.T, c *Cluster, names ...string) {
	t.Helper()
	for _, name := range names {
		if err := c.AddReplica(name); err != nil {
			t.Fatalf("AddReplica(%q): %v", name, err)
		}
	}
}

func mustAppend(t *testing.T, c *Cluster, leader string, gen int64, data string, followers ...string) {
	t.Helper()
	if err := c.Append(leader, gen, data, followers); err != nil {
		t.Fatalf("Append(%s,g%d,%q): %v", leader, gen, data, err)
	}
}

func snapshot(t *testing.T, c *Cluster, name string) ([]Entry, []GenMark) {
	t.Helper()
	n := mustLen(t, c, name)
	log, err := c.QueryLog(name, 0, n)
	if err != nil {
		t.Fatalf("snapshot log %q: %v", name, err)
	}
	gen, err := c.GenCache(name)
	if err != nil {
		t.Fatalf("snapshot cache %q: %v", name, err)
	}
	return log, gen
}

func mustLen(t *testing.T, c *Cluster, name string) int {
	t.Helper()
	n, err := c.LogLen(name)
	if err != nil {
		t.Fatalf("LogLen %q: %v", name, err)
	}
	return n
}

func dataLog(entries []Entry) string {
	parts := make([]string, len(entries))
	for i, e := range entries {
		parts[i] = e.Data
	}
	return strings.Join(parts, "|")
}

// naiveLCP 朴素逐位点最长公共前缀长度。
func naiveLCP(a, b []Entry) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	return n
}

// buildDivergentCluster 构造跨三个世代、跟随者在世代2分叉的场景：
//
//	leader:   g1 a b | g2 c d e | g3 f
//	follower: g1 a b | g2 x       | g3 y  （F 在世代2当选并写入分叉条目）
func buildDivergentCluster(t *testing.T) (*Cluster, *bytes.Buffer) {
	t.Helper()
	c, buf := testCluster(t)
	mustAdd(t, c, "L", "F", "F2")

	if err := c.BecomeLeader("L", 1); err != nil {
		t.Fatal(err)
	}
	mustAppend(t, c, "L", 1, "a", "F")
	mustAppend(t, c, "L", 1, "b", "F")

	if err := c.BecomeLeader("L", 2); err != nil {
		t.Fatal(err)
	}
	mustAppend(t, c, "L", 2, "c")
	mustAppend(t, c, "L", 2, "d", "F2")
	mustAppend(t, c, "L", 2, "e", "F2")

	// F 以世代 2 在分叉分支当选，写入与 L 冲突的条目 x。
	if err := c.BecomeLeader("F", 2); err != nil {
		t.Fatal(err)
	}
	mustAppend(t, c, "F", 2, "x")

	// 世代 3：L 与 F 各自拥有自己的世代 3 缓存项。
	if err := c.BecomeLeader("L", 3); err != nil {
		t.Fatal(err)
	}
	mustAppend(t, c, "L", 3, "f", "F2")
	if err := c.BecomeLeader("F", 3); err != nil {
		t.Fatal(err)
	}
	mustAppend(t, c, "F", 3, "y")
	return c, buf
}

func TestGenCacheEntries(t *testing.T) {
	c, _ := buildDivergentCluster(t)

	lLog, lGen := snapshot(t, c, "L")
	if got, want := dataLog(lLog), "a|b|c|d|e|f"; got != want {
		t.Fatalf("leader log = %q, want %q", got, want)
	}
	wantMarks := []GenMark{{Gen: 1, Start: 0}, {Gen: 2, Start: 2}, {Gen: 3, Start: 5}}
	if fmt.Sprint(lGen) != fmt.Sprint(wantMarks) {
		t.Fatalf("leader cache = %v, want %v", lGen, wantMarks)
	}

	fLog, fGen := snapshot(t, c, "F")
	if got, want := dataLog(fLog), "a|b|x|y"; got != want {
		t.Fatalf("follower log = %q, want %q", got, want)
	}
	wantFMarks := []GenMark{{Gen: 1, Start: 0}, {Gen: 2, Start: 2}, {Gen: 3, Start: 3}}
	if fmt.Sprint(fGen) != fmt.Sprint(wantFMarks) {
		t.Fatalf("follower cache = %v, want %v", fGen, wantFMarks)
	}

	end, err := c.GenerationEnd("L", 2)
	if err != nil || end != 5 {
		t.Fatalf("GenerationEnd(L,2) = %d,%v, want 5,nil", end, err)
	}
	end, err = c.GenerationEnd("L", 1)
	if err != nil || end != 2 {
		t.Fatalf("GenerationEnd(L,1) = %d,%v, want 2,nil", end, err)
	}
}

func TestRecoverMultiRoundTruncation(t *testing.T) {
	c, buf := buildDivergentCluster(t)

	leaderLog, _ := snapshot(t, c, "L")
	divergentFollower := []Entry{
		{Gen: 1, Data: "a"}, {Gen: 1, Data: "b"},
		{Gen: 2, Data: "x"}, {Gen: 3, Data: "y"},
	}

	// 第一轮：领导者不认识 F 的世代3（起始位点不同），截到 3 并删其缓存项；
	// 第二轮：世代2 在位点 2 冲突（c vs x），截到 2。
	cut, err := c.RecoverFollower("L", "F")
	if err != nil {
		t.Fatalf("RecoverFollower: %v", err)
	}
	wantCut := naiveLCP(leaderLog, divergentFollower)
	if cut != wantCut || cut != 2 {
		t.Fatalf("cut = %d, want naive LCP %d", cut, wantCut)
	}

	fLog, fGen := snapshot(t, c, "F")
	if got := dataLog(fLog); got != "a|b" {
		t.Fatalf("follower log after recover = %q, want a|b", got)
	}
	wantGen := []GenMark{{Gen: 1, Start: 0}}
	if fmt.Sprint(fGen) != fmt.Sprint(wantGen) {
		t.Fatalf("follower cache after recover = %v, want %v", fGen, wantGen)
	}

	// 恢复后复制必成功，且逐位点一致。
	if err := c.Replicate("L", "F", 2); err != nil {
		t.Fatalf("Replicate after recover: %v", err)
	}
	fLog2, _ := snapshot(t, c, "F")
	lLog2, _ := snapshot(t, c, "L")
	if fmt.Sprint(fLog2) != fmt.Sprint(lLog2) {
		t.Fatalf("post-replication mismatch:\nF=%v\nL=%v", fLog2, lLog2)
	}

	// 再恢复为幂等空操作（返回当前 LCP 长度，日志不变）。
	cut2, err := c.RecoverFollower("L", "F")
	if err != nil || cut2 != len(lLog2) {
		t.Fatalf("idempotent recover = %d,%v, want %d,nil", cut2, err, len(lLog2))
	}
	fLog3, _ := snapshot(t, c, "F")
	if fmt.Sprint(fLog3) != fmt.Sprint(lLog2) {
		t.Fatalf("idempotent recover changed log: %v", fLog3)
	}

	// 日志中必须能看到每轮输入、截断点与判定依据。
	logs := buf.String()
	for _, want := range []string{
		"recover start",
		"leader_has_gen=false",
		"leader has no generation 3",
		"leader_has_gen=true",
		"first mismatch in generation 2 at position 2",
		"recover done",
	} {
		if !strings.Contains(logs, want) {
			t.Fatalf("recovery log missing %q\nlogs:\n%s", want, logs)
		}
	}
}

func TestRecoverUnknownLeaderGeneration(t *testing.T) {
	// 跟随者的最新世代完全不被领导者认识：第一轮截到该世代起始位点。
	c, _ := testCluster(t)
	mustAdd(t, c, "L", "F")
	if err := c.BecomeLeader("L", 1); err != nil {
		t.Fatal(err)
	}
	mustAppend(t, c, "L", 1, "a", "F")
	if err := c.BecomeLeader("F", 2); err != nil {
		t.Fatal(err)
	}
	mustAppend(t, c, "F", 2, "z")

	leaderLog, _ := snapshot(t, c, "L")
	beforeF, beforeG := snapshot(t, c, "F")

	cut, err := c.RecoverFollower("L", "F")
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	if want := naiveLCP(leaderLog, beforeF); cut != want || cut != 1 {
		t.Fatalf("cut = %d, want naive LCP %d", cut, want)
	}
	fLog, fGen := snapshot(t, c, "F")
	if dataLog(fLog) != "a" {
		t.Fatalf("follower log = %q, want a", dataLog(fLog))
	}
	if fmt.Sprint(fGen) != fmt.Sprint(beforeG[:1]) {
		t.Fatalf("follower cache = %v, want only gen1 mark", fGen)
	}

	// 复制追平后与领导者一致，且再恢复为空操作。
	if err := c.Replicate("L", "F", 1); err != nil {
		t.Fatalf("replicate: %v", err)
	}
	cut2, err := c.RecoverFollower("L", "F")
	if err != nil || cut2 != 1 {
		t.Fatalf("second recover = %d,%v, want 1,nil", cut2, err)
	}
}

func TestRecoverFollowerExtraTail(t *testing.T) {
	// 共同世代完全匹配，但跟随者多出领导者没有的分叉尾部。
	c, _ := testCluster(t)
	mustAdd(t, c, "L", "F")
	if err := c.BecomeLeader("L", 1); err != nil {
		t.Fatal(err)
	}
	mustAppend(t, c, "L", 1, "a", "F")
	mustAppend(t, c, "L", 1, "b", "F")
	if err := c.BecomeLeader("F", 2); err != nil {
		t.Fatal(err)
	}
	mustAppend(t, c, "F", 2, "c")

	cut, err := c.RecoverFollower("L", "F")
	if err != nil || cut != 2 {
		t.Fatalf("recover = %d,%v, want 2,nil", cut, err)
	}
	fLog, _ := snapshot(t, c, "F")
	if got := dataLog(fLog); got != "a|b" {
		t.Fatalf("follower log = %q, want a|b", got)
	}
}

func TestRecoverEmptyIsNoop(t *testing.T) {
	c, _ := testCluster(t)
	mustAdd(t, c, "L", "F")
	if err := c.BecomeLeader("L", 7); err != nil {
		t.Fatal(err)
	}
	cut, err := c.RecoverFollower("L", "F")
	if err != nil || cut != 0 {
		t.Fatalf("empty recover = %d,%v, want 0,nil", cut, err)
	}
}

func TestDistinctErrorCauses(t *testing.T) {
	c, _ := testCluster(t)
	mustAdd(t, c, "L", "F")
	if err := c.BecomeLeader("L", 1); err != nil {
		t.Fatal(err)
	}
	mustAppend(t, c, "L", 1, "a", "F")

	endUnknownGen := func() error { _, err := c.GenerationEnd("L", 99); return err }
	cases := []struct {
		name string
		call func() error
		want error
	}{
		{"empty name", func() error { return c.AddReplica("") }, ErrInvalidArgument},
		{"duplicate replica", func() error { return c.AddReplica("L") }, ErrInvalidArgument},
		{"bad election gen", func() error { return c.BecomeLeader("L", 0) }, ErrInvalidArgument},
		{"stale election gen", func() error { return c.BecomeLeader("L", 1) }, ErrInvalidArgument},
		{"append unknown gen", func() error { return c.Append("L", 9, "z", nil) }, ErrUnknownGeneration},
		{"end unknown replica", func() error { _, err := c.GenerationEnd("ghost", 1); return err }, ErrUnknownReplica},
		{"end unknown gen", endUnknownGen, ErrUnknownGeneration},
		{"query unknown replica", func() error { _, err := c.QueryLog("ghost", 0, 1); return err }, ErrUnknownReplica},
		{"query bad range", func() error { _, err := c.QueryLog("L", 2, 1); return err }, ErrInvalidArgument},
		{"query beyond log", func() error { _, err := c.QueryLog("L", 0, 50); return err }, ErrInvalidArgument},
		{"replicate same name", func() error { return c.Replicate("L", "L", 0) }, ErrInvalidArgument},
		{"replicate unknown", func() error { return c.Replicate("L", "ghost", 0) }, ErrUnknownReplica},
		{"replicate bad pos", func() error { return c.Replicate("L", "F", 99) }, ErrInvalidArgument},
		{"recover same name", func() error { _, err := c.RecoverFollower("L", "L"); return err }, ErrInvalidArgument},
		{"recover unknown", func() error { _, err := c.RecoverFollower("L", "ghost"); return err }, ErrUnknownReplica},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call()
			if !errors.Is(err, tc.want) {
				t.Fatalf("%s: err = %v, want %v", tc.name, err, tc.want)
			}
		})
	}

	// 制造日志分叉：F 在世代2当选写入冲突，此时领导者复制必须报 ErrLogDiverged。
	if err := c.BecomeLeader("F", 2); err != nil {
		t.Fatal(err)
	}
	mustAppend(t, c, "F", 2, "z")
	if err := c.Replicate("L", "F", 0); !errors.Is(err, ErrLogDiverged) {
		t.Fatalf("replicate over divergence: err = %v, want ErrLogDiverged", err)
	}
	if err := c.Append("L", 1, "b", []string{"F"}); !errors.Is(err, ErrLogDiverged) {
		t.Fatalf("append to divergent follower: err = %v, want ErrLogDiverged", err)
	}
}

func TestRejectionLeavesNoTrace(t *testing.T) {
	c, _ := buildDivergentCluster(t)

	snapshots := map[string][]Entry{}
	genSnap := map[string][]GenMark{}
	for _, name := range []string{"L", "F", "F2"} {
		snapshots[name], genSnap[name] = snapshot(t, c, name)
	}

	// 对 F 的追加因分叉失败：任何副本（含其他跟随者 F2）的日志与缓存都必须原样保留。
	if err := c.Append("L", 3, "more", []string{"F", "F2"}); !errors.Is(err, ErrLogDiverged) {
		t.Fatalf("append: err = %v, want ErrLogDiverged", err)
	}
	for _, name := range []string{"L", "F", "F2"} {
		log, gen := snapshot(t, c, name)
		if fmt.Sprint(log) != fmt.Sprint(snapshots[name]) {
			t.Fatalf("%s log changed after rejected append:\nbefore=%v\nafter =%v", name, snapshots[name], log)
		}
		if fmt.Sprint(gen) != fmt.Sprint(genSnap[name]) {
			t.Fatalf("%s cache changed after rejected append:\nbefore=%v\nafter =%v", name, genSnap[name], gen)
		}
	}

	// 非法世代查询也不得改变任何状态。
	if _, err := c.GenerationEnd("F", 42); !errors.Is(err, ErrUnknownGeneration) {
		t.Fatalf("GenerationEnd: err = %v", err)
	}
	for _, name := range []string{"L", "F", "F2"} {
		log, gen := snapshot(t, c, name)
		if fmt.Sprint(log) != fmt.Sprint(snapshots[name]) || fmt.Sprint(gen) != fmt.Sprint(genSnap[name]) {
			t.Fatalf("%s state changed after rejected query", name)
		}
	}
}

func TestConcurrentAccess(t *testing.T) {
	c, _ := testCluster(t)
	mustAdd(t, c, "L", "F1", "F2", "F3")
	if err := c.BecomeLeader("L", 1); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	// 持续写入并复制。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			if err := c.Append("L", 1, fmt.Sprintf("e%d", i), []string{"F1", "F2", "F3"}); err != nil {
				t.Errorf("append %d: %v", i, err)
				return
			}
		}
	}()
	// 并发只读：世代响应、日志查询。
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				if _, err := c.GenerationEnd("L", 1); err != nil {
					t.Errorf("GenerationEnd: %v", err)
					return
				}
				n, err := c.LogLen("L")
				if err != nil {
					t.Errorf("LogLen: %v", err)
					return
				}
				if n > 0 {
					if _, err := c.QueryLog("L", 0, n); err != nil {
						t.Errorf("QueryLog: %v", err)
						return
					}
				}
			}
		}()
	}
	// 并发恢复不同跟随者（此时无分叉，应为空操作）。
	for _, f := range []string{"F1", "F2", "F3"} {
		f := f
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				if _, err := c.RecoverFollower("L", f); err != nil {
					t.Errorf("recover %s: %v", f, err)
					return
				}
			}
		}()
	}
	wg.Wait()

	lLog, _ := snapshot(t, c, "L")
	if len(lLog) != 50 {
		t.Fatalf("leader has %d entries, want 50", len(lLog))
	}
	for _, f := range []string{"F1", "F2", "F3"} {
		fLog, _ := snapshot(t, c, f)
		if fmt.Sprint(fLog) != fmt.Sprint(lLog) {
			t.Fatalf("%s diverged from leader after concurrent ops", f)
		}
	}
}

func TestRecoverDifferentFollowersConcurrently(t *testing.T) {
	c, _ := testCluster(t)
	mustAdd(t, c, "L", "F1", "F2")
	if err := c.BecomeLeader("L", 1); err != nil {
		t.Fatal(err)
	}
	mustAppend(t, c, "L", 1, "a", "F1", "F2")

	// 两个跟随者分别在世代2分叉。
	if err := c.BecomeLeader("F1", 2); err != nil {
		t.Fatal(err)
	}
	mustAppend(t, c, "F1", 2, "x")
	if err := c.BecomeLeader("F2", 2); err != nil {
		t.Fatal(err)
	}
	mustAppend(t, c, "F2", 2, "y")

	var wg sync.WaitGroup
	for _, f := range []string{"F1", "F2"} {
		f := f
		wg.Add(1)
		go func() {
			defer wg.Done()
			cut, err := c.RecoverFollower("L", f)
			if err != nil || cut != 1 {
				t.Errorf("recover %s = %d,%v, want 1,nil", f, cut, err)
			}
		}()
	}
	wg.Wait()

	for _, f := range []string{"F1", "F2"} {
		if err := c.Replicate("L", f, 1); err != nil {
			t.Fatalf("replicate %s: %v", f, err)
		}
		cut, err := c.RecoverFollower("L", f)
		if err != nil || cut != 1 {
			t.Fatalf("idempotent recover %s = %d,%v", f, cut, err)
		}
	}
}
