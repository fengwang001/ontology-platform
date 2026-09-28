package session

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

func newTestRouter(t *testing.T, buf *bytes.Buffer) *Router {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(buf, nil))
	return NewRouter(logger)
}

func mustReason(t *testing.T, err error, want Reason) {
	t.Helper()
	var rerr *Error
	if !errors.As(err, &rerr) {
		t.Fatalf("err = %v, want *Error with reason %s", err, want)
	}
	if rerr.Reason != want {
		t.Fatalf("reason = %s, want %s (err: %v)", rerr.Reason, want, err)
	}
}

// 恰好追平（applied == token）的副本必须可被选中。
func TestReadRoutesToExactlyCaughtUpReplica(t *testing.T) {
	var buf bytes.Buffer
	r := newTestRouter(t, &buf)
	if err := r.RegisterSession("s1"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"r1", "r2"} {
		if err := r.RegisterReplica(name); err != nil {
			t.Fatal(err)
		}
	}
	seq, err := r.Write("s1")
	if err != nil {
		t.Fatal(err)
	}
	if seq != 1 {
		t.Fatalf("seq = %d, want 1", seq)
	}
	// r1 恰好追平令牌 1，r2 落后。
	if err := r.Advance("r1", 1); err != nil {
		t.Fatal(err)
	}
	res, err := r.Read("s1")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if res.Replica != "r1" || res.Progress != 1 || res.Token != 1 {
		t.Fatalf("res = %+v, want {r1 1 1}", res)
	}
	log := buf.String()
	for _, want := range []string{"op=read", "session=s1", "replica=r1", "reason="} {
		if !strings.Contains(log, want) {
			t.Fatalf("log missing %q:\n%s", want, log)
		}
	}
}

// 并列进度时选进度最小者，再按名字字典序打破并列。
func TestReadPicksLeastProgressThenLexicographic(t *testing.T) {
	var buf bytes.Buffer
	r := newTestRouter(t, &buf)
	if err := r.RegisterSession("s1"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"beta", "alpha", "gamma"} {
		if err := r.RegisterReplica(name); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.Write("s1"); err != nil {
		t.Fatal(err)
	}
	// beta/alpha 并列于 1（恰好够用），gamma 进度更高。
	for _, name := range []string{"beta", "alpha", "gamma"} {
		if err := r.Advance(name, 1); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Advance("gamma", 1); err == nil {
		// gamma 已到主库序号 1，不能再推进；用第二次写入拉开差距。
	}
	res, err := r.Read("s1")
	if err != nil {
		t.Fatal(err)
	}
	if res.Replica != "alpha" {
		t.Fatalf("replica = %q, want alpha (tie broken by name)", res.Replica)
	}

	// 再写一次并只推进 gamma，读必须选唯一追上的 gamma。
	if _, err := r.Write("s1"); err != nil {
		t.Fatal(err)
	}
	if err := r.Advance("gamma", 2); err != nil {
		t.Fatal(err)
	}
	res, err = r.Read("s1")
	if err != nil {
		t.Fatal(err)
	}
	if res.Replica != "gamma" || res.Progress != 2 {
		t.Fatalf("res = %+v, want gamma@2", res)
	}
}

// 没有副本追上到令牌时立即返回 replica_lagging，且不改变任何状态。
func TestReadFailsWhenNoReplicaCaughtUp(t *testing.T) {
	var buf bytes.Buffer
	r := newTestRouter(t, &buf)
	if err := r.RegisterSession("s1"); err != nil {
		t.Fatal(err)
	}
	if err := r.RegisterReplica("r1"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Write("s1"); err != nil {
		t.Fatal(err)
	}
	_, err := r.Read("s1")
	mustReason(t, err, ReasonReplicaLagging)
	if !strings.Contains(buf.String(), "no replica applied sequence >= token") {
		t.Fatalf("log missing rationale:\n%s", buf.String())
	}
	// 失败后令牌不变：推进副本到恰好 1 后读应成功且令牌为 1。
	if err := r.Advance("r1", 1); err != nil {
		t.Fatal(err)
	}
	res, err := r.Read("s1")
	if err != nil {
		t.Fatal(err)
	}
	if res.Token != 1 {
		t.Fatalf("token = %d, want 1 (failed read must not move token)", res.Token)
	}
}

// 各类非法输入必须以可区分原因拒绝，且不得改变主库序号、副本进度或令牌。
func TestInvalidInputsRejected(t *testing.T) {
	var buf bytes.Buffer
	r := newTestRouter(t, &buf)
	if err := r.RegisterSession("s1"); err != nil {
		t.Fatal(err)
	}
	if err := r.RegisterReplica("r1"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Write("s1"); err != nil { // 主库序号 -> 1
		t.Fatal(err)
	}
	if err := r.Advance("r1", 1); err != nil { // r1 进度 -> 1
		t.Fatal(err)
	}
	if _, err := r.Read("s1"); err != nil { // s1 令牌 -> 1
		t.Fatal(err)
	}

	cases := []struct {
		name string
		op   func() error
		want Reason
	}{
		{"write unknown session", func() error { _, err := r.Write("ghost"); return err }, ReasonUnknownSession},
		{"read unknown session", func() error { _, err := r.Read("ghost"); return err }, ReasonUnknownSession},
		{"advance unknown replica", func() error { return r.Advance("ghost", 1) }, ReasonUnknownReplica},
		{"advance regression", func() error { return r.Advance("r1", 0) }, ReasonSeqRegression},
		{"advance ahead of primary", func() error { return r.Advance("r1", 2) }, ReasonSeqAhead},
		{"register empty session", func() error { return r.RegisterSession("") }, ReasonEmptyID},
		{"register empty replica", func() error { return r.RegisterReplica("") }, ReasonEmptyID},
		{"register duplicate session", func() error { return r.RegisterSession("s1") }, ReasonDuplicate},
		{"register duplicate replica", func() error { return r.RegisterReplica("r1") }, ReasonDuplicate},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mustReason(t, tc.op(), tc.want)
		})
	}

	// 状态未被任何拒绝操作改变：主库序号仍为 1，r1 进度仍为 1，s1 令牌仍为 1。
	seq, err := r.Write("s1")
	if err != nil {
		t.Fatal(err)
	}
	if seq != 2 {
		t.Fatalf("seq = %d, want 2 (rejected ops must not bump primary)", seq)
	}
	if err := r.Advance("r1", 2); err != nil {
		t.Fatalf("advance r1 to 2: %v (rejected ops must not move replica)", err)
	}
	res, err := r.Read("s1")
	if err != nil {
		t.Fatal(err)
	}
	if res.Token != 2 || res.Progress != 2 {
		t.Fatalf("res = %+v, want token/progress 2 (rejected ops must not move token)", res)
	}
}

// 并发写、读、推进下，每个会话读到的进度单调不减且不小于此前最后一次写入。
func TestConcurrentMonotonicReads(t *testing.T) {
	var buf bytes.Buffer
	r := newTestRouter(t, &buf)
	const sessions = 8
	const rounds = 200
	replicas := []string{"r1", "r2", "r3"}
	for _, name := range replicas {
		if err := r.RegisterReplica(name); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < sessions; i++ {
		if err := r.RegisterSession(fmt.Sprintf("s%d", i)); err != nil {
			t.Fatal(err)
		}
	}

	var wg sync.WaitGroup
	errs := make(chan error, sessions*rounds)

	// 推进器：不断把所有副本向主库进度推进（逐步，模拟复制延迟）。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for next := uint64(1); ; next++ {
			done := true
			for _, name := range replicas {
				err := r.Advance(name, next)
				if err == nil {
					done = false
					continue
				}
				var rerr *Error
				if !errors.As(err, &rerr) || rerr.Reason != ReasonSeqAhead {
					errs <- fmt.Errorf("advance %s to %d: %w", name, next, err)
					return
				}
			}
			if done { // 所有副本都已追平主库
				return
			}
		}
	}()

	// 每个会话一个 goroutine：写后读，校验单调性与读己之写。
	for i := 0; i < sessions; i++ {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			var lastProgress, lastWrite uint64
			for n := 0; n < rounds; n++ {
				seq, err := r.Write(id)
				if err != nil {
					errs <- fmt.Errorf("write %s: %w", id, err)
					return
				}
				if seq < lastWrite {
					errs <- fmt.Errorf("%s: seq regressed %d -> %d", id, lastWrite, seq)
					return
				}
				lastWrite = seq
				res, err := r.Read(id)
				if err != nil {
					var rerr *Error
					if errors.As(err, &rerr) && rerr.Reason == ReasonReplicaLagging {
						continue // 允许未追上：立即失败而非阻塞
					}
					errs <- fmt.Errorf("read %s: %w", id, err)
					return
				}
				if res.Progress < lastProgress {
					errs <- fmt.Errorf("%s: progress regressed %d -> %d", id, lastProgress, res.Progress)
					return
				}
				if res.Progress < lastWrite {
					errs <- fmt.Errorf("%s: progress %d < last write %d", id, res.Progress, lastWrite)
					return
				}
				lastProgress = res.Progress
			}
		}(fmt.Sprintf("s%d", i))
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

// 同一输入序列反复计算必须得到完全相同的输出。
func TestDeterministicReplay(t *testing.T) {
	run := func() string {
		var buf bytes.Buffer
		r := newTestRouter(t, &buf)
		out := &strings.Builder{}
		step := func(format string, args ...any) {
			fmt.Fprintf(out, format+"\n", args...)
		}
		for _, name := range []string{"c", "a", "b"} {
			step("register replica %s: %v", name, r.RegisterReplica(name))
		}
		for _, id := range []string{"s2", "s1"} {
			step("register session %s: %v", id, r.RegisterSession(id))
		}
		type op struct {
			kind    string
			session string
			replica string
			seq     uint64
		}
		script := []op{
			{"write", "s1", "", 0},
			{"write", "s2", "", 0},
			{"advance", "", "b", 1},
			{"advance", "", "a", 2},
			{"read", "s1", "", 0},
			{"read", "s2", "", 0},
			{"advance", "", "c", 2},
			{"read", "s1", "", 0},
			{"write", "s1", "", 0},
			{"read", "s1", "", 0},
			{"advance", "", "b", 3},
			{"read", "s1", "", 0},
		}
		for _, o := range script {
			switch o.kind {
			case "write":
				seq, err := r.Write(o.session)
				step("write %s -> seq=%d err=%v", o.session, seq, err)
			case "read":
				res, err := r.Read(o.session)
				step("read %s -> res=%+v err=%v", o.session, res, err)
			case "advance":
				step("advance %s %d -> err=%v", o.replica, o.seq, r.Advance(o.replica, o.seq))
			}
		}
		return out.String()
	}
	first := run()
	for i := 0; i < 5; i++ {
		if got := run(); got != first {
			t.Fatalf("run %d differs:\nfirst:\n%s\ngot:\n%s", i, first, got)
		}
	}
}
