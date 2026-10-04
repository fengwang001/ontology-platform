package exec

import (
	"errors"
	"testing"

	"ontology/action"
)

func kv(k, v string) action.KV { return action.KV{Key: k, Value: v} }
func plat(kvs ...action.KV) action.Platform {
	return action.Platform(kvs)
}

// tableStep 为用例中的一步；want 字段描述本步的输入输出与判定依据。
type tableStep struct {
	kind   string // exec/reg/poll/complete/lost/cancel
	digest string
	plat   action.Platform
	prio   int
	skip   bool
	worker string
	props  []action.KV
	slots  int
	opW    int // complete 时按“等待者编号”定位其在途操作
	att    int
	exit   int
	infra  bool

	wantID        int
	wantErr       error
	wantOK        bool
	checkQueue    bool
	wantQ         []string
	wantWait      int
	wantOut       action.Outcome
	wantCacheHit  bool
	wantCacheExit int
}

func outcomeNow(s *Scheduler, id int) (action.Outcome, bool) {
	w, ok := s.Waiter(id)
	if !ok {
		return action.Outcome{}, false
	}
	select {
	case o := <-w.Done():
		return o, true
	default:
		return action.Outcome{}, false
	}
}

func runTable(t *testing.T, M int, steps []tableStep) {
	t.Helper()
	s := New(M)
	opByWaiter := map[int]*action.Op{}
	for i, st := range steps {
		var gotErr error
		var gotID, gotAtt int
		var gotOp *action.Op
		var gotOK bool

		switch st.kind {
		case "exec":
			gotID, gotErr = s.Execute(st.digest, st.plat, st.prio, st.skip)
			if gotErr == nil {
				if o, ok := s.registry.Get(st.digest); ok {
					opByWaiter[gotID] = o
				}
			}
		case "reg":
			gotErr = s.Register(st.worker, st.props, st.slots)
		case "poll":
			r, err := s.Poll(st.worker)
			gotErr = err
			if err == nil && r.Op != nil {
				gotOK, gotOp, gotAtt = true, r.Op, r.Attempt
			}
		case "complete":
			o := opByWaiter[st.opW]
			if o == nil {
				t.Fatalf("step %d: no op tracked for waiter %d", i, st.opW)
			}
			gotErr = s.Complete(st.worker, o, st.att, st.exit, st.infra)
		case "lost":
			gotErr = s.WorkerLost(st.worker)
		case "cancel":
			gotErr = s.Cancel(st.wantWait)
		default:
			t.Fatalf("step %d: unknown kind %q", i, st.kind)
		}

		t.Logf("step %d kind=%s input={digest=%q worker=%q prio=%d skip=%v att=%d exit=%d infra=%v} -> id=%d ok=%v att=%d err=%v | 判定: %s",
			i, st.kind, st.digest, st.worker, st.prio, st.skip, st.att, st.exit, st.infra,
			gotID, gotOK, gotAtt, gotErr, judgeBasis(st, gotID, gotOK, gotAtt, gotErr))

		if !errors.Is(gotErr, st.wantErr) {
			t.Fatalf("step %d: err = %v, want %v", i, gotErr, st.wantErr)
		}
		if st.kind == "exec" && gotErr == nil && gotID != st.wantID {
			t.Fatalf("step %d: waiter id = %d, want %d", i, gotID, st.wantID)
		}
		if st.kind == "poll" && gotErr == nil && gotOK != st.wantOK {
			t.Fatalf("step %d: poll ok = %v, want %v", i, gotOK, st.wantOK)
		}
		if st.kind == "poll" && gotOK {
			opByWaiter[firstWaiter(gotOp)] = gotOp
			if gotAtt != pollWantAtt(st, i, t) {
				t.Fatalf("step %d: attempt = %d", i, gotAtt)
			}
		}
		if st.checkQueue {
			want := st.wantQ
			if want == nil {
				want = []string{}
			}
			if got := s.QueueOrder(); !eqStrings(got, want) {
				t.Fatalf("step %d: queue = %v, want %v", i, got, want)
			}
		}
		if st.wantErr == nil && st.wantWait > 0 {
			if o, ok := outcomeNow(s, st.wantWait); !ok || o != st.wantOut {
				t.Fatalf("step %d: waiter %d outcome = %+v ok=%v, want %+v",
					i, st.wantWait, o, ok, st.wantOut)
			}
		}
		if st.wantCacheHit {
			if exit, ok := s.Cached(st.digestOrOpDigest(opByWaiter)); !ok || exit != st.wantCacheExit {
				t.Fatalf("step %d: cache hit=%v exit=%d, want exit %d", i, ok, exit, st.wantCacheExit)
			}
		}
	}
}

func judgeBasis(st tableStep, id int, ok bool, att int, err error) string {
	switch {
	case err != nil:
		return "拒绝：" + err.Error()
	case st.kind == "exec":
		return "等待者编号按全局序号分配，被拒不占号"
	case st.kind == "poll" && ok:
		return "按（有效优先级降序, seq 升序）取首个 platform 子集合匹配的操作，尝试号=失数+1"
	case st.kind == "poll":
		return "无可匹配操作不算错误"
	default:
		return "终局/队列/缓存按规则即时生效"
	}
}

func firstWaiter(o *action.Op) int {
	min := 0
	for id := range o.Waiters {
		if min == 0 || id < min {
			min = id
		}
	}
	return min
}

func pollWantAtt(st tableStep, i int, t *testing.T) int {
	t.Helper()
	return st.att
}

func (st tableStep) digestOrOpDigest(m map[int]*action.Op) string {
	if st.digest != "" {
		return st.digest
	}
	if o := m[st.opW]; o != nil {
		return o.Digest
	}
	return ""
}

func eqStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
