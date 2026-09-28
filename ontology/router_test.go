package ontology

import (
	"bytes"
	"strings"
	"sync"
	"testing"
)

func rejectReason(t *testing.T, err error) RejectReason {
	t.Helper()
	if err == nil {
		t.Fatal("expected rejection error, got nil")
	}
	var re *RejectError
	if !asRejectError(err, &re) {
		t.Fatalf("expected *RejectError, got %T: %v", err, err)
	}
	return re.Reason
}

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExactCatchupAndTieBreak(t *testing.T) {
	var log bytes.Buffer
	r := NewRouter(&log)
	mustOK(t, r.AddSession("s1"))
	mustOK(t, r.AddReplica("r-b"))
	mustOK(t, r.AddReplica("r-a"))

	w, err := r.Write("s1")
	mustOK(t, err)
	if w.Sequence != 1 || w.Token != 1 {
		t.Fatalf("write = %+v, want sequence=1 token=1", w)
	}

	// 两个副本都停在 0：没有足够新的副本，立即失败。
	if _, err := r.Read("s1"); rejectReason(t, err) != ReasonReplicaLagging {
		t.Fatalf("want replica_lagging, got %v", err)
	}

	// r-a 恰好追平到 1：必须路由成功到 r-a。
	mustOK(t, r.Advance("r-a", 1))
	rd, err := r.Read("s1")
	mustOK(t, err)
	if rd.Replica != "r-a" || rd.Observed != 1 || rd.Token != 1 {
		t.Fatalf("read = %+v, want replica=r-a observed=1 token=1", rd)
	}

	// 新写入 seq=2，两个副本都落后：立即失败。
	// r-a 试图超前推进到 3（主库只有 2）必须被拒绝，进度保持 1。
	w, err = r.Write("s1")
	mustOK(t, err)
	if w.Sequence != 2 {
		t.Fatalf("sequence = %d, want 2", w.Sequence)
	}
	if _, err := r.Read("s1"); rejectReason(t, err) != ReasonReplicaLagging {
		t.Fatalf("want replica_lagging after new write, got %v", err)
	}
	if err := r.Advance("r-a", 3); rejectReason(t, err) != ReasonAdvanceAhead {
		t.Fatalf("want advance_ahead, got %v", err)
	}
	mustOK(t, r.Advance("r-b", 2))

	rd, err = r.Read("s1")
	mustOK(t, err)
	if rd.Replica != "r-b" || rd.Observed != 2 || rd.Token != 2 {
		t.Fatalf("read = %+v, want replica=r-b observed=2 token=2", rd)
	}

	// 再写入 seq=3；两边恰好都追平到 3 时，按名字字典序选择 r-a。
	w, err = r.Write("s1")
	mustOK(t, err)
	if w.Sequence != 3 {
		t.Fatalf("sequence = %d, want 3", w.Sequence)
	}
	mustOK(t, r.Advance("r-b", 3))
	if _, err := r.Read("s1"); rejectReason(t, err) != ReasonReplicaLagging {
		t.Fatalf("want replica_lagging while only r-b caught up, got %v", err)
	}
	mustOK(t, r.Advance("r-a", 3))
	rd, err = r.Read("s1")
	mustOK(t, err)
	if rd.Replica != "r-a" || rd.Observed != 3 || rd.Token != 3 {
		t.Fatalf("read = %+v, want lexicographically smallest replica r-a at observed=3", rd)
	}

	// 日志必须包含输入、所选副本与判定依据。
	out := log.String()
	for _, want := range []string{
		`op=read session="s1" rejected reason=replica_lagging basis=no_replica_applied_ge_token`,
		`op=read session="s1" accepted reason=none basis=min_applied_ge_token replica="r-a" observed=1`,
		`replica="r-b" observed=2`,
		`replica="r-a" observed=3`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("log missing %q\nfull log:\n%s", want, out)
		}
	}
}

func TestRejectionsDoNotMutateState(t *testing.T) {
	r := NewRouter(nil)
	mustOK(t, r.AddSession("s1"))
	mustOK(t, r.AddReplica("r1"))
	w, err := r.Write("s1")
	mustOK(t, err)
	mustOK(t, r.Advance("r1", w.Sequence))

	snapshot := func() (uint64, uint64, uint64) {
		tok, err := r.Token("s1")
		mustOK(t, err)
		app, err := r.Applied("r1")
		mustOK(t, err)
		return r.Primary(), tok, app
	}

	cases := []struct {
		name   string
		reason RejectReason
		fn     func() error
	}{
		{"write unknown session", ReasonUnknownSession, func() error {
			_, err := r.Write("ghost")
			return err
		}},
		{"read unknown session", ReasonUnknownSession, func() error {
			_, err := r.Read("ghost")
			return err
		}},
		{"advance unknown replica", ReasonUnknownReplica, func() error {
			return r.Advance("ghost", 1)
		}},
		{"advance rollback", ReasonAdvanceRollback, func() error {
			return r.Advance("r1", w.Sequence-1)
		}},
		{"advance ahead of primary", ReasonAdvanceAhead, func() error {
			return r.Advance("r1", w.Sequence+1)
		}},
		{"duplicate session", ReasonDuplicateSession, func() error {
			return r.AddSession("s1")
		}},
		{"duplicate replica", ReasonDuplicateReplica, func() error {
			return r.AddReplica("r1")
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := [3]uint64{}
			before[0], before[1], before[2] = snapshot()
			if got := rejectReason(t, tc.fn()); got != tc.reason {
				t.Fatalf("reason = %s, want %s", got, tc.reason)
			}
			var after [3]uint64
			after[0], after[1], after[2] = snapshot()
			if before != after {
				t.Fatalf("state changed by rejected op: before=%v after=%v", before, after)
			}
		})
	}
}

func TestDeterministicReplay(t *testing.T) {
	run := func() string {
		var log bytes.Buffer
		r := NewRouter(&log)
		step := func(label string, err error) {
			if err != nil {
				var re *RejectError
				if asRejectError(err, &re) {
					log.WriteString("result " + label + " rejected " + string(re.Reason) + "\n")
					return
				}
				log.WriteString("result " + label + " error " + err.Error() + "\n")
				return
			}
			log.WriteString("result " + label + " ok\n")
		}

		step("add_session_dup", r.AddSession("s1"))
		mustOK(t, r.AddSession("s1"))
		step("add_session_dup", r.AddSession("s1"))
		mustOK(t, r.AddSession("s2"))
		step("add_replica_dup", r.AddReplica("rb"))
		mustOK(t, r.AddReplica("rb"))
		step("add_replica_dup", r.AddReplica("rb"))
		mustOK(t, r.AddReplica("ra"))

		w1, err := r.Write("s1")
		mustOK(t, err)
		log.WriteString("w1=" + uitoa(w1.Sequence) + " token=" + uitoa(w1.Token) + "\n")
		w2, err := r.Write("s2")
		mustOK(t, err)
		log.WriteString("w2=" + uitoa(w2.Sequence) + " token=" + uitoa(w2.Token) + "\n")

		_, err = r.Read("s2")
		step("read_s2_lagging", err)
		mustOK(t, r.Advance("rb", 2))
		rd, err := r.Read("s2")
		mustOK(t, err)
		log.WriteString("read_s2 replica=" + rd.Replica + " observed=" + uitoa(rd.Observed) + " token=" + uitoa(rd.Token) + "\n")

		_, err = r.Read("s1")
		step("read_s1_lagging", err)
		mustOK(t, r.Advance("ra", 1))
		rd, err = r.Read("s1")
		mustOK(t, err)
		log.WriteString("read_s1 replica=" + rd.Replica + " observed=" + uitoa(rd.Observed) + " token=" + uitoa(rd.Token) + "\n")

		step("rollback", r.Advance("ra", 0))
		step("ahead", r.Advance("ra", 3))
		_, err = r.Write("ghost")
		step("write_unknown", err)
		_, err = r.Read("ghost")
		step("read_unknown", err)
		step("advance_unknown", r.Advance("ghost", 0))
		return log.String()
	}

	first := run()
	for i := 0; i < 3; i++ {
		if got := run(); got != first {
			t.Fatalf("replay %d differs\n--- first ---\n%s\n--- got ---\n%s", i, first, got)
		}
	}
}

func TestConcurrentSessionMonotonicity(t *testing.T) {
	r := NewRouter(nil)
	mustOK(t, r.AddReplica("ra"))
	mustOK(t, r.AddReplica("rb"))

	const sessions = 8
	const writesPerSession = 40

	var wg sync.WaitGroup

	// 推进器：持续把两个副本推进到主库当前序号，保证读取最终可追平。
	stop := make(chan struct{})
	for _, name := range []string{"ra", "rb"} {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if err := r.Advance(name, r.Primary()); err != nil {
					if rejectReason(t, err) == ReasonUnknownReplica {
						return
					}
				}
			}
		}(name)
	}

	// 每个会话串行执行“写一次 → 读到成功”，可以严格断言：
	// 观察进度不小于本次写入序号，且同会话内单调不降。
	for s := 0; s < sessions; s++ {
		id := "s" + uitoa(uint64(s))
		mustOK(t, r.AddSession(id))
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			var lastObserved uint64
			for i := 0; i < writesPerSession; i++ {
				w, err := r.Write(id)
				if err != nil {
					t.Errorf("write %s: %v", id, err)
					return
				}
				var rd ReadResult
				for {
					rd, err = r.Read(id)
					if err == nil {
						break
					}
					if rejectReason(t, err) != ReasonReplicaLagging {
						t.Errorf("read %s: unexpected %v", id, err)
						return
					}
				}
				if rd.Observed < w.Sequence {
					t.Errorf("session %s observed %d < own write %d", id, rd.Observed, w.Sequence)
					return
				}
				if rd.Observed < lastObserved {
					t.Errorf("session %s observed went backwards: %d -> %d", id, lastObserved, rd.Observed)
					return
				}
				if rd.Token != rd.Observed || rd.Token < w.Sequence {
					t.Errorf("session %s token=%d observed=%d write=%d", id, rd.Token, rd.Observed, w.Sequence)
					return
				}
				lastObserved = rd.Observed
			}
		}(id)
	}

	wg.Wait()
	close(stop)

	if got := r.Primary(); got != sessions*writesPerSession {
		t.Fatalf("primary = %d, want %d", got, sessions*writesPerSession)
	}
}
