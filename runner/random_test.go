package runner_test

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"testing"

	"ontology/flow"
	"ontology/grants"
	"ontology/runner"
)

func sameErr(got, want error) bool {
	if want == nil {
		return got == nil
	}
	return errors.Is(got, want)
}

func randMask(rng *rand.Rand) uint64 {
	switch rng.Intn(5) {
	case 0:
		return 0
	case 1:
		return grants.ApproveBit
	case 2:
		return uint64(1) << uint(rng.Intn(64))
	case 3:
		return uint64(rng.Intn(8))
	default:
		return rng.Uint64()
	}
}

func randReqs(rng *rand.Rand, ceil uint64) []uint64 {
	reqs := make([]uint64, rng.Intn(18))
	for i := range reqs {
		switch rng.Intn(4) {
		case 0:
			reqs[i] = 0
		case 1:
			reqs[i] = ceil & uint64(rng.Intn(16))
		case 2:
			reqs[i] = uint64(rng.Intn(16))
		default:
			reqs[i] = rng.Uint64()
		}
	}
	return reqs
}

type instSnap struct {
	audit []runner.Event
	st    runner.StatusView
}

// drive 用同一种子在真实实现与朴素模拟上重放随机操作序列并逐步对照。
func drive(t *testing.T, seed int64) map[string]instSnap {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))
	g := grants.New()
	f := flow.New()
	r, err := runner.New(g, f, 7)
	if err != nil {
		t.Fatalf("new runner: %v", err)
	}
	s := newSim(7)
	names := []string{"", "p", "q", "a"}
	defs := []string{"", "d0", "d1"}
	insts := []string{"", "i0", "i1", "i2"}
	pick := func(xs []string) string { return xs[rng.Intn(len(xs))] }
	for step := 0; step < 3000; step++ {
		now := s.clock + int64(rng.Intn(4))
		switch rng.Intn(20) {
		case 0:
			now = -1
		case 1:
			now = runner.MaxNow + 1
		case 2:
			if s.clock > 0 {
				now = s.clock - 1
			}
		}
		var got, want error
		desc := ""
		switch rng.Intn(9) {
		case 0:
			p, m := pick(names), randMask(rng)
			got, want, desc = g.Grant(p, m), s.grant(p, m), fmt.Sprintf("Grant(%q,%#x)", p, m)
		case 1:
			p, m := pick(names), randMask(rng)
			got, want, desc = g.Revoke(p, m), s.revoke(p, m), fmt.Sprintf("Revoke(%q,%#x)", p, m)
		case 2:
			d, ceil, reqs := pick(defs), randMask(rng), []uint64(nil)
			reqs = randReqs(rng, ceil)
			got, want = f.Define(d, ceil, reqs), s.define(d, ceil, reqs)
			desc = fmt.Sprintf("Define(%q,%#x,%x)", d, ceil, reqs)
		case 3:
			in, d, p := pick(insts), pick(defs), pick(names)
			got, want = r.Launch(in, d, p, now), s.launch(in, d, p, now)
			desc = fmt.Sprintf("Launch(%q,%q,%q,%d)", in, d, p, now)
		case 4:
			in := pick(insts)
			o1, e1 := r.StartStep(in, now)
			o2, e2 := s.startStep(in, now)
			got, want, desc = e1, e2, fmt.Sprintf("StartStep(%q,%d)", in, now)
			if sameErr(got, want) && got == nil && o1 != o2 {
				t.Fatalf("step %d %s: outcome %+v != sim %+v", step, desc, o1, o2)
			}
		case 5:
			in := pick(insts)
			i1, e1 := r.FinishStep(in, now)
			i2, e2 := s.finishStep(in, now)
			got, want, desc = e1, e2, fmt.Sprintf("FinishStep(%q,%d)", in, now)
			if sameErr(got, want) && got == nil && i1 != i2 {
				t.Fatalf("step %d %s: index %d != sim %d", step, desc, i1, i2)
			}
		case 6:
			in, a := pick(insts), pick(names)
			got, want = r.Approve(in, a, now), s.decide(in, a, now, true)
			desc = fmt.Sprintf("Approve(%q,%q,%d)", in, a, now)
		case 7:
			in, a := pick(insts), pick(names)
			got, want = r.Reject(in, a, now), s.decide(in, a, now, false)
			desc = fmt.Sprintf("Reject(%q,%q,%d)", in, a, now)
		case 8:
			in := pick(insts)
			v1, e1 := r.Status(in, now)
			v2, e2 := s.status(in, now)
			got, want, desc = e1, e2, fmt.Sprintf("Status(%q,%d)", in, now)
			if sameErr(got, want) && got == nil && !reflect.DeepEqual(v1, v2) {
				t.Fatalf("step %d %s: view %+v != sim %+v", step, desc, v1, v2)
			}
		}
		t.Logf("step %d: %s -> got=%v want=%v", step, desc, got, want)
		if !sameErr(got, want) {
			t.Fatalf("step %d %s: got %v, sim want %v", step, desc, got, want)
		}
		compareAll(t, r, s, insts[1:], step)
	}
	snap := map[string]instSnap{}
	for _, in := range insts[1:] {
		if evs, err := r.Audit(in); err == nil {
			st, _ := r.Status(in, s.clock)
			snap[in] = instSnap{audit: evs, st: st}
		}
	}
	return snap
}

// compareAll 每步后逐实例对照审计日志与状态视图。
func compareAll(t *testing.T, r *runner.Runner, s *sim, insts []string, step int) {
	t.Helper()
	for _, in := range insts {
		sin, ok := s.insts[in]
		evs, err := r.Audit(in)
		if ok != (err == nil) {
			t.Fatalf("step %d: audit existence mismatch on %s", step, in)
		}
		if ok && !reflect.DeepEqual(evs, sin.audit) {
			t.Fatalf("step %d: audit mismatch on %s:\n got %+v\nwant %+v", step, in, evs, sin.audit)
		}
		v1, e1 := r.Status(in, s.clock)
		v2, e2 := s.status(in, s.clock)
		if !sameErr(e1, e2) || (e1 == nil && !reflect.DeepEqual(v1, v2)) {
			t.Fatalf("step %d: status mismatch on %s: %+v/%v != %+v/%v", step, in, v1, e1, v2, e2)
		}
	}
}

func TestRandomAgainstSimulator(t *testing.T) {
	drive(t, 20261004)
}

// TestReplayDeterminism 相同操作序列重放得到相同审计与终局。
func TestReplayDeterminism(t *testing.T) {
	if a, b := drive(t, 42), drive(t, 42); !reflect.DeepEqual(a, b) {
		t.Fatalf("replay diverged:\n %+v\n %+v", a, b)
	}
}
