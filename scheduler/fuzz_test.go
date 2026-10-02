package scheduler

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

// fuzzOp is one randomized operation applied to both schedulers.
type fuzzOp struct {
	kind string
	a, b int
}

func (o fuzzOp) String() string {
	switch o.kind {
	case "spawn":
		return fmt.Sprintf("Spawn(%d,%d)", o.a, o.b)
	case "tick":
		return "Tick()"
	case "sleep":
		return "Sleep()"
	case "wake":
		return fmt.Sprintf("Wake(%d)", o.a)
	case "fork":
		return fmt.Sprintf("Fork(%d,%d)", o.a, o.b)
	}
	return "?"
}

func genSequence(rng *rand.Rand, n int) []fuzzOp {
	ops := 40 + rng.Intn(80)
	seq := make([]fuzzOp, 0, ops)
	for i := 0; i < ops; i++ {
		switch r := rng.Intn(100); {
		case r < 38:
			k := 1 + rng.Intn(4)
			for j := 0; j < k; j++ {
				seq = append(seq, fuzzOp{kind: "tick"})
			}
		case r < 58:
			seq = append(seq, fuzzOp{kind: "spawn", a: rng.Intn(14) - 1, b: rng.Intn(42) - 21})
		case r < 68:
			seq = append(seq, fuzzOp{kind: "sleep"})
		case r < 86:
			seq = append(seq, fuzzOp{kind: "wake", a: rng.Intn(14)})
		default:
			seq = append(seq, fuzzOp{kind: "fork", a: rng.Intn(14) - 1, b: rng.Intn(14) - 1})
		}
	}
	return seq
}

func applyOp(s *Scheduler, o fuzzOp) error {
	switch o.kind {
	case "spawn":
		return s.Spawn(o.a, o.b)
	case "tick":
		s.Tick()
		return nil
	case "sleep":
		return s.Sleep()
	case "wake":
		return s.Wake(o.a)
	case "fork":
		return s.Fork(o.a, o.b)
	}
	return nil
}

func applyOpNaive(s *naiveScheduler, o fuzzOp) error {
	switch o.kind {
	case "spawn":
		return s.spawn(o.a, o.b)
	case "tick":
		s.tick()
		return nil
	case "sleep":
		return s.sleep()
	case "wake":
		return s.wake(o.a)
	case "fork":
		return s.fork(o.a, o.b)
	}
	return nil
}

// equalQueues compares snapshots treating nil and empty slices as equal.
func equalQueues(a, b []PriorityQueue) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Prio != b[i].Prio {
			return false
		}
		if len(a[i].IDs) != len(b[i].IDs) {
			return false
		}
		for j := range a[i].IDs {
			if a[i].IDs[j] != b[i].IDs[j] {
				return false
			}
		}
	}
	return true
}

// checkInvariants verifies the structural invariants of the spec on the
// real scheduler using only its public queries.
func checkInvariants(s *Scheduler) error {
	qs := s.Queues()
	expiredEmpty := len(qs.Expired) == 0
	if (s.ExpiredTs() == 0) != expiredEmpty {
		return fmt.Errorf("expiredTs=%d but expired empty=%v", s.ExpiredTs(), expiredEmpty)
	}
	seen := make(map[int]string)
	mark := func(id int, where string) error {
		if w, ok := seen[id]; ok {
			return fmt.Errorf("task %d appears in %s and %s", id, w, where)
		}
		seen[id] = where
		return nil
	}
	for _, arr := range []struct {
		name string
		qs   []PriorityQueue
	}{{"active", qs.Active}, {"expired", qs.Expired}} {
		for _, pq := range arr.qs {
			for _, id := range pq.IDs {
				if err := mark(id, arr.name); err != nil {
					return err
				}
				info, ok := s.State(id)
				if !ok {
					return fmt.Errorf("queued task %d missing", id)
				}
				if info.State != Queued {
					return fmt.Errorf("task %d in queue but state=%v", id, info.State)
				}
				if info.Prio != pq.Prio {
					return fmt.Errorf("task %d prio=%d but queued at %d", id, info.Prio, pq.Prio)
				}
			}
		}
	}
	if cur, ok := s.Current(); ok {
		if err := mark(cur, "cur"); err != nil {
			return err
		}
		info, _ := s.State(cur)
		if info.State != Running {
			return fmt.Errorf("cur %d state=%v", cur, info.State)
		}
	}
	return nil
}

func compareSchedulers(s *Scheduler, ns *naiveScheduler, ids []int) string {
	if s.Now() != ns.now {
		return fmt.Sprintf("now: got %d want %d", s.Now(), ns.now)
	}
	c1, ok1 := s.Current()
	c2, ok2 := ns.current()
	if c1 != c2 || ok1 != ok2 {
		return fmt.Sprintf("current: got (%d,%v) want (%d,%v)", c1, ok1, c2, ok2)
	}
	if s.ExpiredTs() != ns.expiredTs {
		return fmt.Sprintf("expiredTs: got %d want %d", s.ExpiredTs(), ns.expiredTs)
	}
	q1, q2 := s.Queues(), ns.queues()
	if !equalQueues(q1.Active, q2.Active) {
		return fmt.Sprintf("active queues: got %+v want %+v", q1.Active, q2.Active)
	}
	if !equalQueues(q1.Expired, q2.Expired) {
		return fmt.Sprintf("expired queues: got %+v want %+v", q1.Expired, q2.Expired)
	}
	for _, id := range ids {
		i1, ok1 := s.State(id)
		i2, ok2 := ns.state(id)
		if ok1 != ok2 || i1 != i2 {
			return fmt.Sprintf("state(%d): got (%+v,%v) want (%+v,%v)", id, i1, ok1, i2, ok2)
		}
	}
	return ""
}

// TestFuzzAgainstNaive replays 2000 random operation sequences on both
// the bitmap scheduler and the naive reference, comparing the full
// observable state after every operation and logging inputs, outputs and
// the deciding rule for each sequence.
func TestFuzzAgainstNaive(t *testing.T) {
	const sequences = 2000
	for seed := int64(0); seed < sequences; seed++ {
		rng := rand.New(rand.NewSource(seed))
		n := 1 + rng.Intn(12)
		ops := genSequence(rng, n)
		s := New(n)
		ns := newNaive(n)
		ids := map[int]bool{}
		var idList []int
		runLog := []int{}
		mismatch := ""
		badIdx := -1
		for i, o := range ops {
			e1 := applyOp(s, o)
			e2 := applyOpNaive(ns, o)
			if e1 != e2 {
				mismatch = fmt.Sprintf("error: got %v want %v", e1, e2)
				badIdx = i
				break
			}
			if o.kind == "spawn" && e1 == nil && !ids[o.a] {
				ids[o.a] = true
				idList = append(idList, o.a)
			}
			if o.kind == "tick" {
				if cur, ok := s.Current(); ok {
					runLog = append(runLog, cur)
				} else {
					runLog = append(runLog, -1)
				}
			}
			if msg := compareSchedulers(s, ns, idList); msg != "" {
				mismatch = msg
				badIdx = i
				break
			}
			if err := checkInvariants(s); err != nil {
				mismatch = "invariant: " + err.Error()
				badIdx = i
				break
			}
		}
		if mismatch != "" {
			sort.Ints(idList)
			t.Errorf("seed=%d n=%d op[%d]=%s mismatch: %s\n判定依据: 相同操作序列下位图调度器与朴素模拟的可观察状态必须完全一致\n输入序列: %v",
				seed, n, badIdx, ops[badIdx], mismatch, ops)
			continue
		}
		t.Logf("seed=%d n=%d ops=%d ticks=%d run=%v 判定: 每操作后 Current/Queues/ExpiredTs/State 与朴素模拟一致",
			seed, n, len(ops), len(runLog), runLog)
	}
}

// TestDeterministicReplay runs the same fixed pseudo-random sequence on
// two fresh schedulers and requires identical per-tick run sequences.
func TestDeterministicReplay(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	n := 8
	ops := genSequence(rng, n)
	runs := make([][]int, 2)
	for rep := 0; rep < 2; rep++ {
		s := New(n)
		for _, o := range ops {
			applyOp(s, o)
			if o.kind == "tick" {
				if cur, ok := s.Current(); ok {
					runs[rep] = append(runs[rep], cur)
				} else {
					runs[rep] = append(runs[rep], -1)
				}
			}
		}
	}
	if !reflect.DeepEqual(runs[0], runs[1]) {
		t.Fatalf("replay diverged:\n%v\n%v", runs[0], runs[1])
	}
}
