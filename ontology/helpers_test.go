package ontology

import (
	"reflect"
	"testing"
)

const testInstance = "obj"

func counterOp(id int, priv Privilege, delta int, maxAttempts int) Op {
	return setCounter(id, testInstance, priv, delta, maxAttempts)
}

func setCounter(id int, instance string, priv Privilege, delta int, maxAttempts int) Op {
	return Op{
		ID:          id,
		Instance:    instance,
		Priv:        priv,
		MaxAttempts: maxAttempts,
		Apply: func(a Attrs) Attrs {
			out := Attrs{}
			for k, v := range a {
				out[k] = v
			}
			n, _ := out["x"].(int)
			out["x"] = n + delta
			out["last"] = id
			return out
		},
	}
}

// reads 构造“放行读闸门”的步骤。
func reads(ids ...int) []Step {
	out := make([]Step, 0, len(ids))
	for _, id := range ids {
		out = append(out, Step{OpID: id})
	}
	return out
}

// commits 构造“放行提交闸门（执行 CAS）”的步骤。
func commits(ids ...int) []Step {
	out := make([]Step, 0, len(ids))
	for _, id := range ids {
		out = append(out, Step{OpID: id, Commit: true})
	}
	return out
}

func cat(groups ...[]Step) []Step {
	var out []Step
	for _, g := range groups {
		out = append(out, g...)
	}
	return out
}

func compareWithReference(t *testing.T, ops []Op, results map[int]OpResult, sched *Scheduler) {
	t.Helper()
	exec := sched.executor
	t.Helper()
	ref, refStates, err := ReplayReference(ops, sched.Events())
	if err != nil {
		t.Fatalf("reference replay failed: %v", err)
	}
	if len(ref) != len(results) {
		t.Fatalf("result count mismatch: impl=%d ref=%d", len(results), len(ref))
	}
	statusMap := map[Status]RefStatus{
		StatusCommitted: RefCommitted,
		StatusPreempted: RefPreempted,
		StatusExhausted: RefExhausted,
	}
	for id, impl := range results {
		rf, ok := ref[id]
		if !ok {
			t.Fatalf("op %d missing in reference results", id)
		}
		if rf.Status != statusMap[impl.Status] {
			t.Fatalf("op %d status mismatch: impl=%s ref=%s", id, impl.Status, rf.Status)
		}
		if impl.CommittedAt != rf.CommittedAt {
			t.Fatalf("op %d commit version mismatch: impl=%d ref=%d",
				id, impl.CommittedAt, rf.CommittedAt)
		}
		if len(impl.Attempts) != len(rf.Attempts) {
			t.Fatalf("op %d attempt count mismatch: impl=%d ref=%d",
				id, len(impl.Attempts), len(rf.Attempts))
		}
		for i := range impl.Attempts {
			if impl.Attempts[i].Verdict != rf.Attempts[i].Verdict {
				t.Fatalf("op %d attempt %d verdict mismatch: impl=%s ref=%s",
					id, i+1, impl.Attempts[i].Verdict, rf.Attempts[i].Verdict)
			}
			if impl.Attempts[i].BaseVersion != rf.Attempts[i].BaseVersion {
				t.Fatalf("op %d attempt %d baseline mismatch: impl=%d ref=%d",
					id, i+1, impl.Attempts[i].BaseVersion, rf.Attempts[i].BaseVersion)
			}
		}
	}
	implVer, implHW, implAttrs := exec.FinalState(testInstance)
	rs := refStates[testInstance]
	if implVer != rs.Version || implHW != rs.HighWater || !reflect.DeepEqual(implAttrs, rs.Attrs) {
		t.Fatalf("final state mismatch:\n impl=(v=%d hw=%d %v)\n ref =(v=%d hw=%d %v)",
			implVer, implHW, implAttrs, rs.Version, rs.HighWater, rs.Attrs)
	}
}
