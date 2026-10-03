package altruistic

import (
	"fmt"
	"math/rand"
	"sort"
	"sync"
	"testing"
)

// op 是随机序列中的一条调用。
type op struct {
	kind string
	t    int
	o    int
}

// stateSnapshot 是差分比较用的完整状态快照（仅含结构，不含日志）。
type stateSnapshot struct {
	state   map[int]string
	locked  map[int]map[int]bool
	holding map[int]map[int]bool
	donated map[int]map[int]bool
	wk      map[int]map[int]bool
	holder  map[int]int
}

func snapshotOf(m *Manager) stateSnapshot {
	snap := stateSnapshot{
		state:   map[int]string{},
		locked:  map[int]map[int]bool{},
		holding: map[int]map[int]bool{},
		donated: map[int]map[int]bool{},
		wk:      map[int]map[int]bool{},
		holder:  map[int]int{},
	}
	for i, st := range m.state {
		t := i + 1
		switch st {
		case stateActive:
			snap.state[t] = "active"
		case stateFinished:
			snap.state[t] = "finished"
		case stateAborted:
			snap.state[t] = "aborted"
		}
		bitsToSet := func(bits uint64) map[int]bool {
			out := map[int]bool{}
			for o := 0; o < m.n; o++ {
				if bits&(1<<uint(o)) != 0 {
					out[o] = true
				}
			}
			return out
		}
		snap.locked[t] = bitsToSet(m.locked[i])
		snap.holding[t] = bitsToSet(m.holding[i])
		snap.donated[t] = bitsToSet(m.donated[i])
		snap.wk[t] = map[int]bool{}
		m.wk[i].each(func(u int) bool {
			snap.wk[t][u] = true
			return true
		})
	}
	for o := 0; o < m.n; o++ {
		if h := m.holder[o]; h != -1 {
			snap.holder[o] = h
		}
	}
	return snap
}

func snapshotOfNaive(s *naiveSim) stateSnapshot {
	clone := func(in map[int]bool) map[int]bool {
		out := map[int]bool{}
		for k, v := range in {
			out[k] = v
		}
		return out
	}
	snap := stateSnapshot{
		state:   map[int]string{},
		locked:  map[int]map[int]bool{},
		holding: map[int]map[int]bool{},
		donated: map[int]map[int]bool{},
		wk:      map[int]map[int]bool{},
		holder:  map[int]int{},
	}
	for t, st := range s.state {
		snap.state[t] = st
		snap.locked[t] = clone(s.locked[t])
		snap.holding[t] = clone(s.holding[t])
		snap.donated[t] = clone(s.donated[t])
		snap.wk[t] = clone(s.wk[t])
	}
	for o, h := range s.holder {
		snap.holder[o] = h
	}
	return snap
}

func setEqual(a, b map[int]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

func (s stateSnapshot) equal(o stateSnapshot) (string, bool) {
	if len(s.state) != len(o.state) {
		return fmt.Sprintf("txn count %d != %d", len(s.state), len(o.state)), false
	}
	for t, st := range s.state {
		if o.state[t] != st {
			return fmt.Sprintf("state[%d]=%s != %s", t, st, o.state[t]), false
		}
		if !setEqual(s.locked[t], o.locked[t]) {
			return fmt.Sprintf("locked[%d] %v != %v", t, s.locked[t], o.locked[t]), false
		}
		if !setEqual(s.holding[t], o.holding[t]) {
			return fmt.Sprintf("holding[%d] %v != %v", t, s.holding[t], o.holding[t]), false
		}
		if !setEqual(s.donated[t], o.donated[t]) {
			return fmt.Sprintf("donated[%d] %v != %v", t, s.donated[t], o.donated[t]), false
		}
		if !setEqual(s.wk[t], o.wk[t]) {
			return fmt.Sprintf("wk[%d] %v != %v", t, s.wk[t], o.wk[t]), false
		}
	}
	if len(s.holder) != len(o.holder) {
		return fmt.Sprintf("holder count %v != %v", s.holder, o.holder), false
	}
	for ob, h := range s.holder {
		if o.holder[ob] != h {
			return fmt.Sprintf("holder[%d]=%d != %d", ob, h, o.holder[ob]), false
		}
	}
	return "", true
}

// TestDifferentialAgainstNaive 用 2000 组随机调用序列对照朴素模型。
func TestDifferentialAgainstNaive(t *testing.T) {
	const sequences = 2000
	const maxCalls = 60
	const maxTxns = 8
	rng := rand.New(rand.NewSource(20261003))

	for seq := 0; seq < sequences; seq++ {
		n := 1 + rng.Intn(8)
		m, err := New(n)
		if err != nil {
			t.Fatalf("seq %d: New: %v", seq, err)
		}
		ns := newNaive(n)
		var ops []op
		var callLog []string

		callCount := 5 + rng.Intn(maxCalls)
		for ci := 0; ci < callCount; ci++ {
			// 以较高概率先 Begin，保证大部分调用有事务可用。
			existing := ns.nextID - 1
			if existing < 2 || rng.Intn(10) < 3 {
				rb := ns.Begin()
				got := m.Begin()
				if got != rb.beginID {
					t.Fatalf("seq %d: Begin id %d != %d", seq, got, rb.beginID)
				}
				callLog = append(callLog, fmt.Sprintf("Begin() -> %d", got))
				ops = append(ops, op{kind: "begin"})
				continue
			}
			tid := 1 + rng.Intn(existing+2) // 偶尔引用不存在的事务号
			choice := rng.Intn(10)
			var o int
			if rng.Intn(8) == 0 {
				o = rng.Intn(n+3) - 1 // 偶尔越界
			} else {
				o = rng.Intn(n)
			}
			var cur op
			var gotErr error
			var gotAbort []int
			var expect naiveResult
			switch {
			case choice < 4:
				cur = op{kind: "lock", t: tid, o: o}
				expect = ns.Lock(tid, o)
				gotErr = m.Lock(tid, o)
			case choice < 6:
				cur = op{kind: "donate", t: tid, o: o}
				expect = ns.Donate(tid, o)
				gotErr = m.Donate(tid, o)
			case choice < 8:
				cur = op{kind: "finish", t: tid}
				expect = ns.Finish(tid)
				gotErr = m.Finish(tid)
			default:
				cur = op{kind: "abort", t: tid}
				expect = ns.Abort(tid)
				gotAbort, gotErr = m.Abort(tid)
			}
			ops = append(ops, cur)

			// 比较拒绝原因与附带事务/对象。
			if expect.ok {
				if gotErr != nil {
					t.Fatalf("seq %d op %v: unexpected error %v\nlog:\n%s", seq, cur, gotErr, joinLog(callLog, ns.log))
				}
			} else {
				var le *LockError
				if !asLockError(gotErr, &le) {
					t.Fatalf("seq %d op %v: want reject %s got %v\nlog:\n%s", seq, cur, expect.kind, gotErr, joinLog(callLog, ns.log))
				}
				if le.Kind != expect.kind || le.Transaction != expect.txn || le.Object != expect.obj {
					t.Fatalf("seq %d op %v: got (%s,t=%d,o=%d) want (%s,t=%d,o=%d)\nlog:\n%s",
						seq, cur, le.Kind, le.Transaction, le.Object, expect.kind, expect.txn, expect.obj, joinLog(callLog, ns.log))
				}
			}
			if cur.kind == "abort" && expect.ok {
				if !intSliceEqual(gotAbort, expect.aborted) {
					t.Fatalf("seq %d op %v: abort set %v != %v", seq, cur, gotAbort, expect.aborted)
				}
				callLog = append(callLog, fmt.Sprintf("Abort(%d) -> %v", tid, gotAbort))
			} else {
				callLog = append(callLog, ns.log[len(ns.log)-1])
			}

			// 每次调用后比较完整状态。
			gotSnap := snapshotOf(m)
			wantSnap := snapshotOfNaive(ns)
			if msg, eq := gotSnap.equal(wantSnap); !eq {
				t.Fatalf("seq %d op %v: state mismatch: %s\nlog:\n%s", seq, cur, msg, joinLog(callLog, ns.log))
			}
		}

		// 每 50 组打印一条输入/输出摘要（判定依据在失败时随日志输出）。
		if seq%50 == 0 {
			t.Logf("seq %d: n=%d calls=%d finalActiveTxns=%d log_sample=%q",
				seq, n, callCount, len(ns.state), ns.log[len(ns.log)-1])
		}
	}
}

func asLockError(err error, target **LockError) bool {
	if err == nil {
		return false
	}
	le, ok := err.(*LockError)
	if ok {
		*target = le
	}
	return ok
}

func intSliceEqual(a, b []int) bool {
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

func joinLog(prefix []string, naiveLog []string) string {
	out := ""
	for _, l := range prefix {
		out += "  " + l + "\n"
	}
	return out
}

// TestConcurrentSerializability 并发调用必须可串行化：并发出一堆彼此独立的
// Begin/Lock（不同对象），结果与某一合法串行顺序等价，且持有集合两两不交。
func TestConcurrentSerializability(t *testing.T) {
	const n = 64
	m, err := New(n)
	if err != nil {
		t.Fatal(err)
	}
	const workers = 32
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	ids := make([]int, workers)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			tid := m.Begin()
			ids[w] = tid
			if e := m.Lock(tid, w); e != nil {
				errs <- fmt.Errorf("worker %d lock: %w", w, e)
				return
			}
			if e := m.Donate(tid, w); e != nil {
				errs <- fmt.Errorf("worker %d donate: %w", w, e)
				return
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
	sort.Ints(ids)
	for i := 0; i < workers; i++ {
		if ids[i] != i+1 {
			t.Fatalf("transaction ids not a permutation of 1..%d: %v", workers, ids)
		}
	}
	// 所有对象都已捐赠：每个对象无持有者，全部事务活跃。
	for o := 0; o < workers; o++ {
		if m.holder[o] != -1 {
			t.Fatalf("object %d still held by %d", o, m.holder[o])
		}
	}
	for i := 0; i < workers; i++ {
		if m.state[i] != stateActive {
			t.Fatalf("txn %d not active", i+1)
		}
	}
}
