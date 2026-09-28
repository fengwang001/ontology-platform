package sessionroute

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
)

func rejectReason(t *testing.T, err error) Reason {
	t.Helper()
	var re *RejectError
	if !errors.As(err, &re) {
		t.Fatalf("want *RejectError, got %v", err)
	}
	return re.Reason
}

func newTestRouter(t *testing.T, names ...string) *Router {
	t.Helper()
	r, err := NewRouter(names, nil)
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	return r
}

func TestWriteThenExactCatchUpRoutesToLeastProgress(t *testing.T) {
	log := &bytes.Buffer{}
	r, err := NewRouter([]string{"r-a", "r-b", "r-c"}, log)
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	if err := r.RegisterSession("s1"); err != nil {
		t.Fatalf("RegisterSession: %v", err)
	}

	w1, err := r.Write("s1")
	if err != nil || w1.Sequence != 1 || w1.Token != 1 {
		t.Fatalf("write#1 = %+v, %v", w1, err)
	}
	w2, err := r.Write("s1")
	if err != nil || w2.Sequence != 2 || w2.Token != 2 {
		t.Fatalf("write#2 = %+v, %v", w2, err)
	}

	// 另一个会话产生序号 3，使主库到 3，但 s1 的令牌仍为 2。
	if err := r.RegisterSession("s2"); err != nil {
		t.Fatalf("RegisterSession s2: %v", err)
	}
	if w3, err := r.Write("s2"); err != nil || w3.Sequence != 3 {
		t.Fatalf("write s2 = %+v, %v", w3, err)
	}

	// 两个副本恰好追平到 s1 的令牌 2，另一个应用了 s2 的写入到达 3。
	if err := r.Advance("r-b", 2); err != nil {
		t.Fatalf("advance r-b: %v", err)
	}
	if err := r.Advance("r-a", 2); err != nil {
		t.Fatalf("advance r-a: %v", err)
	}
	if err := r.Advance("r-c", 3); err != nil {
		t.Fatalf("advance r-c: %v", err)
	}

	// 候选为 r-a、r-b（恰好追平，进度 2）与 r-c（进度 3），
	// 取进度最小者 2，并列时按字典序选 r-a。
	got, err := r.Read("s1")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got.Replica != "r-a" || got.Observed != 2 || got.Token != 2 {
		t.Fatalf("read = %+v, want replica=r-a observed=2 token=2", got)
	}

	// 日志必须包含输入、所选副本与判定依据。
	fragment := log.String()
	for _, want := range []string{
		"write session=s1 seq=2",
		"read session=s1",
		"replica=r-a",
		"observed=2",
		"字典序",
	} {
		if !strings.Contains(fragment, want) {
			t.Fatalf("log missing %q:\n%s", want, fragment)
		}
	}
}

func TestReadFailureWhenNoReplicaCaughtUp(t *testing.T) {
	r := newTestRouter(t, "r-a")
	if err := r.RegisterSession("s1"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Write("s1"); err != nil {
		t.Fatal(err)
	}
	// 副本停留在 0，读请求需要令牌 1。
	if _, err := r.Read("s1"); err == nil ||
		rejectReason(t, err) != ReasonReplicaBehind {
		t.Fatalf("read = %v, want replica_behind", err)
	}
	// 推进到 1 恰好追平后立即可读。
	if err := r.Advance("r-a", 1); err != nil {
		t.Fatalf("advance: %v", err)
	}
	got, err := r.Read("s1")
	if err != nil || got.Replica != "r-a" || got.Observed != 1 {
		t.Fatalf("read after catch-up = %+v, %v", got, err)
	}
}

func TestReadAdvancesTokenToObservedProgress(t *testing.T) {
	r := newTestRouter(t, "r-a")
	if err := r.RegisterSession("s1"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Write("s1"); err != nil {
		t.Fatal(err)
	}
	// 其他会话把主库推到 3，r-a 应用到 3。
	if err := r.RegisterSession("s2"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Write("s2"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Write("s2"); err != nil {
		t.Fatal(err)
	}
	if err := r.Advance("r-a", 3); err != nil {
		t.Fatal(err)
	}
	got, err := r.Read("s1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Observed != 3 || got.Token != 3 {
		t.Fatalf("read = %+v, want observed=3 token=3", got)
	}
}

func TestUnknownSessionAndReplica(t *testing.T) {
	r := newTestRouter(t, "r-a")

	if _, err := r.Write("ghost"); err == nil ||
		rejectReason(t, err) != ReasonUnknownSession {
		t.Fatalf("write ghost = %v, want unknown_session", err)
	}
	if _, err := r.Read("ghost"); err == nil ||
		rejectReason(t, err) != ReasonUnknownSession {
		t.Fatalf("read ghost = %v, want unknown_session", err)
	}
	if err := r.Advance("ghost", 0); err == nil ||
		rejectReason(t, err) != ReasonUnknownReplica {
		t.Fatalf("advance ghost = %v, want unknown_replica", err)
	}
}

func TestAdvanceRollbackAndAheadRejected(t *testing.T) {
	r := newTestRouter(t, "r-a")
	if err := r.RegisterSession("s1"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Write("s1"); err != nil {
		t.Fatal(err)
	}
	if err := r.Advance("r-a", 1); err != nil {
		t.Fatal(err)
	}

	if err := r.Advance("r-a", 0); err == nil ||
		rejectReason(t, err) != ReasonProgressRollback {
		t.Fatalf("rollback = %v, want progress_rollback", err)
	}
	// 主库只有 1，推进到 2 属于超前。
	if err := r.Advance("r-a", 2); err == nil ||
		rejectReason(t, err) != ReasonProgressAhead {
		t.Fatalf("ahead = %v, want progress_ahead", err)
	}
	// 被拒绝后进度仍为 1，且重复推进相同序号幂等成功。
	if err := r.Advance("r-a", 1); err != nil {
		t.Fatalf("idempotent advance: %v", err)
	}
}

func TestRejectedOperationsChangeNothing(t *testing.T) {
	r := newTestRouter(t, "r-a")
	if err := r.RegisterSession("s1"); err != nil {
		t.Fatal(err)
	}
	if w, err := r.Write("s1"); err != nil || w.Sequence != 1 {
		t.Fatalf("write = %+v, %v", w, err)
	}

	for _, op := range []func() error{
		func() error { _, e := r.Write("ghost"); return e },
		func() error { _, e := r.Read("ghost"); return e },
		func() error { return r.Advance("ghost", 1) },
		func() error { return r.Advance("r-a", 2) }, // 超前
	} {
		if err := op(); err == nil {
			t.Fatal("illegal op unexpectedly succeeded")
		}
	}

	// 主库序号未被非法写入推进，因此下一次合法写入仍是序号 2。
	w, err := r.Write("s1")
	if err != nil || w.Sequence != 2 {
		t.Fatalf("write after rejects = %+v, %v, want seq=2", w, err)
	}
	// 副本仍停留在 0：读必须因落后失败，令牌也不被改动。
	if _, err := r.Read("s1"); err == nil ||
		rejectReason(t, err) != ReasonReplicaBehind {
		t.Fatalf("read = %v, want replica_behind", err)
	}
}

func TestInvalidConstructionAndRegistration(t *testing.T) {
	if _, err := NewRouter([]string{"", "r"}, io.Discard); err == nil {
		t.Fatal("empty replica name should fail")
	}
	if _, err := NewRouter([]string{"r", "r"}, io.Discard); err == nil {
		t.Fatal("duplicate replica name should fail")
	}
	r := newTestRouter(t, "r")
	if err := r.RegisterSession(""); err == nil {
		t.Fatal("empty session name should fail")
	}
	if err := r.RegisterSession("s1"); err != nil {
		t.Fatal(err)
	}
	if err := r.RegisterSession("s1"); err == nil {
		t.Fatal("duplicate session should fail")
	}
}

// 记录一个操作序列，在全新路由器上重放应得到字节级一致的输出。
func TestDeterministicReplay(t *testing.T) {
	script := []string{
		"reg s1", "reg s2",
		"w s1", "w s2",
		"adv r-b 2", "adv r-a 1", "adv r-a 2",
		"r s1", "w s2", "adv r-a 3", "r s2", "r s1",
		"w ghost", "adv ghost 1", "adv r-a 9", "r ghost",
	}
	run := func() string {
		log := &bytes.Buffer{}
		r, err := NewRouter([]string{"r-a", "r-b", "r-c"}, log)
		if err != nil {
			t.Fatal(err)
		}
		out := &strings.Builder{}
		for _, line := range script {
			parts := strings.Fields(line)
			switch parts[0] {
			case "reg":
				out.WriteString(fmt.Sprintf("reg %s => %v\n", parts[1],
					r.RegisterSession(parts[1])))
			case "w":
				res, e := r.Write(parts[1])
				out.WriteString(fmt.Sprintf("w %s => %+v %v\n", parts[1], res, e))
			case "r":
				res, e := r.Read(parts[1])
				out.WriteString(fmt.Sprintf("r %s => %+v %v\n", parts[1], res, e))
			case "adv":
				var p int64
				fmt.Sscan(parts[2], &p)
				out.WriteString(fmt.Sprintf("adv %s %d => %v\n", parts[1], p,
					r.Advance(parts[1], p)))
			}
		}
		return log.String() + "---OUT---\n" + out.String()
	}
	first := run()
	for i := 0; i < 3; i++ {
		if got := run(); got != first {
			t.Fatalf("replay %d differs:\n%s\n----\n%s", i, got, first)
		}
	}
}

func TestConcurrentMonotonicPerSession(t *testing.T) {
	r := newTestRouter(t, "r-a", "r-b")
	const sessions = 8
	for i := 0; i < sessions; i++ {
		if err := r.RegisterSession(fmt.Sprintf("s%d", i)); err != nil {
			t.Fatal(err)
		}
	}

	var wg sync.WaitGroup

	// 每个会话：反复写入（记录自己最后一次写入序号）并尝试读取，
	// 读到的进度必须单调不降且不小于此前最后一次写入序号。
	for i := 0; i < sessions; i++ {
		name := fmt.Sprintf("s%d", i)
		wg.Add(1)
		go func() {
			defer wg.Done()
			var lastWrite, lastRead int64
			for k := 0; k < 200; k++ {
				w, err := r.Write(name)
				if err != nil {
					t.Errorf("write: %v", err)
					return
				}
				lastWrite = w.Sequence
				if w.Token < w.Sequence {
					t.Errorf("token %d < seq %d", w.Token, w.Sequence)
					return
				}
				if got, err := r.Read(name); err == nil {
					if got.Observed < lastRead {
						t.Errorf("observed went backwards: %d -> %d",
							lastRead, got.Observed)
						return
					}
					if got.Observed < lastWrite {
						t.Errorf("observed %d < last write %d",
							got.Observed, lastWrite)
						return
					}
					if got.Token < lastWrite {
						t.Errorf("token %d < last write %d after read",
							got.Token, lastWrite)
						return
					}
					lastRead = got.Observed
				} else if rejectReason(t, err) != ReasonReplicaBehind {
					t.Errorf("unexpected read error: %v", err)
					return
				}
			}
		}()
	}

	// 持续把两个副本推进到主库最新进度（滞后或超前推进均可能出现，
	// 这两类拒绝都必须是安全且可区分的）。
	advancer := func(replica string) {
		defer wg.Done()
		var at int64
		for k := 0; k < 400; k++ {
			err := r.Advance(replica, at)
			switch {
			case err == nil:
				at++
			case rejectReason(t, err) == ReasonProgressAhead:
				// 主库还没产生该序号，稍后重试。
			default:
				t.Errorf("advance %s: %v", replica, err)
				return
			}
		}
	}
	wg.Add(2)
	go advancer("r-a")
	go advancer("r-b")

	wg.Wait()
}
