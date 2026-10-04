package ontology

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"sync"
	"testing"
)

// naiveManager 是按规则逐步写成的朴素模拟，用 map 与线性扫描实现，
// 与 Manager 的位集实现相互独立，用于随机序列对照。
type naiveManager struct {
	n       int
	nextID  int
	holder  map[int]int
	known   map[int]bool
	active  map[int]bool
	locked  map[int]map[int]bool
	donated map[int]map[int]bool
	wake    map[int]map[int]bool
}

func newNaive(n int) *naiveManager {
	return &naiveManager{
		n:       n,
		nextID:  1,
		holder:  make(map[int]int),
		known:   make(map[int]bool),
		active:  make(map[int]bool),
		locked:  make(map[int]map[int]bool),
		donated: make(map[int]map[int]bool),
		wake:    make(map[int]map[int]bool),
	}
}

func (nm *naiveManager) begin() int {
	id := nm.nextID
	nm.nextID++
	nm.known[id] = true
	nm.active[id] = true
	nm.locked[id] = make(map[int]bool)
	nm.donated[id] = make(map[int]bool)
	nm.wake[id] = make(map[int]bool)
	return id
}

func (nm *naiveManager) check(t, o int, checkObj bool) *RejectError {
	if !nm.known[t] {
		return &RejectError{Reason: ReasonTxNotFound, Tx: t, Object: o}
	}
	if !nm.active[t] {
		return &RejectError{Reason: ReasonTxNotActive, Tx: t, Object: o}
	}
	if checkObj && (o < 0 || o >= nm.n) {
		return &RejectError{Reason: ReasonObjectOutOfRange, Tx: t, Object: o}
	}
	return nil
}

func (nm *naiveManager) activeSorted() []int {
	var ids []int
	for id := range nm.active {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	return ids
}

func (nm *naiveManager) lock(t, o int) *RejectError {
	if rej := nm.check(t, o, true); rej != nil {
		return rej
	}
	if nm.donated[t][o] {
		return &RejectError{Reason: ReasonAlreadyDonated, Tx: t, Object: o}
	}
	if nm.holder[o] == t {
		return nil
	}
	if nm.holder[o] != 0 {
		return &RejectError{Reason: ReasonHeldByOther, Tx: t, Object: o, Blocker: nm.holder[o]}
	}
	sPrime := make(map[int]bool, len(nm.locked[t])+1)
	for x := range nm.locked[t] {
		sPrime[x] = true
	}
	sPrime[o] = true
	for _, id := range nm.activeSorted() {
		if id == t {
			continue
		}
		if !nm.wake[t][id] && !nm.donated[id][o] {
			continue
		}
		subset := true
		for x := range sPrime {
			if !nm.donated[id][x] {
				subset = false
				break
			}
		}
		if !subset {
			return &RejectError{Reason: ReasonWakeViolation, Tx: t, Object: o, Blocker: id}
		}
	}
	nm.holder[o] = t
	nm.locked[t][o] = true
	for _, id := range nm.activeSorted() {
		if id != t && nm.donated[id][o] {
			nm.wake[t][id] = true
		}
	}
	return nil
}

func (nm *naiveManager) donate(t, o int) *RejectError {
	if rej := nm.check(t, o, true); rej != nil {
		return rej
	}
	if nm.holder[o] != t {
		return &RejectError{Reason: ReasonNotHolder, Tx: t, Object: o}
	}
	delete(nm.holder, o)
	nm.donated[t][o] = true
	return nil
}

func (nm *naiveManager) finish(t int) *RejectError {
	if rej := nm.check(t, 0, false); rej != nil {
		return rej
	}
	for _, id := range nm.activeSorted() {
		if nm.wake[t][id] {
			return &RejectError{Reason: ReasonWakePending, Tx: t, Blocker: id}
		}
	}
	for o, h := range nm.holder {
		if h == t {
			delete(nm.holder, o)
		}
	}
	delete(nm.active, t)
	return nil
}

func (nm *naiveManager) abort(t int) ([]int, *RejectError) {
	if rej := nm.check(t, 0, false); rej != nil {
		return nil, rej
	}
	inA := map[int]bool{t: true}
	for changed := true; changed; {
		changed = false
		for _, id := range nm.activeSorted() {
			if inA[id] {
				continue
			}
			for w := range nm.wake[id] {
				if inA[w] {
					inA[id] = true
					changed = true
					break
				}
			}
		}
	}
	out := make([]int, 0, len(inA))
	for id := range inA {
		for o, h := range nm.holder {
			if h == id {
				delete(nm.holder, o)
			}
		}
		delete(nm.active, id)
		out = append(out, id)
	}
	sort.Ints(out)
	return out, nil
}

type opKind int

const (
	opBegin opKind = iota
	opLock
	opDonate
	opFinish
	opAbort
)

type op struct {
	kind opKind
	tx   int
	obj  int
}

func (o op) String() string {
	switch o.kind {
	case opBegin:
		return "Begin()"
	case opLock:
		return fmt.Sprintf("Lock(%d,%d)", o.tx, o.obj)
	case opDonate:
		return fmt.Sprintf("Donate(%d,%d)", o.tx, o.obj)
	case opFinish:
		return fmt.Sprintf("Finish(%d)", o.tx)
	case opAbort:
		return fmt.Sprintf("Abort(%d)", o.tx)
	}
	return "?"
}

func genSequence(r *rand.Rand) (int, []op) {
	n := 1 + r.Intn(8)
	maxTx := 0
	count := 10 + r.Intn(30)
	seq := make([]op, 0, count)
	for i := 0; i < count; i++ {
		tx := 1
		if maxTx > 0 {
			tx = 1 + r.Intn(maxTx+2) // 可能命中不存在的事务号
		}
		obj := -1 + r.Intn(n+2) // 可能越界
		switch r.Intn(8) {
		case 0, 1:
			seq = append(seq, op{kind: opBegin})
			maxTx++
		case 2, 3, 4:
			seq = append(seq, op{kind: opLock, tx: tx, obj: obj})
		case 5:
			seq = append(seq, op{kind: opDonate, tx: tx, obj: obj})
		case 6:
			seq = append(seq, op{kind: opFinish, tx: tx})
		case 7:
			seq = append(seq, op{kind: opAbort, tx: tx})
		}
	}
	return n, seq
}

func describeErr(err error) string {
	if err == nil {
		return "ok"
	}
	var rej *RejectError
	if !asReject(err, &rej) {
		return err.Error()
	}
	return rej.Error()
}

func asReject(err error, out **RejectError) bool {
	if err == nil {
		return false
	}
	rej, ok := err.(*RejectError)
	if !ok {
		return false
	}
	*out = rej
	return true
}

func sameReject(got error, want *RejectError) bool {
	if want == nil {
		return got == nil
	}
	rej, ok := got.(*RejectError)
	if !ok || rej == nil {
		return false
	}
	return rej.Reason == want.Reason && rej.Blocker == want.Blocker
}

// checkInvariants 校验题目要求的结构性不变量。
func checkInvariants(t *testing.T, m *Manager, ctx string) {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	seen := make(map[int]int)
	for o, h := range m.holder {
		if h == 0 {
			continue
		}
		tx := m.txs[h]
		if tx == nil || tx.state != stateActive {
			t.Fatalf("%s: object %d held by non-active tx %d", ctx, o, h)
		}
		if tx.held&(uint64(1)<<uint(o)) == 0 {
			t.Fatalf("%s: holder[%d]=%d but held set disagrees", ctx, o, h)
		}
		if prev, dup := seen[o]; dup {
			t.Fatalf("%s: object %d held by both %d and %d", ctx, o, prev, h)
		}
		seen[o] = h
	}
	for id, tx := range m.txs {
		if tx.state != stateActive {
			continue
		}
		for w := range tx.wake {
			wt := m.txs[w]
			if wt == nil || wt.state != stateActive {
				continue
			}
			if tx.locked&^wt.donated != 0 {
				t.Fatalf("%s: wake invariant violated: locked(%d) not subset of donated(%d)",
					ctx, id, w)
			}
		}
	}
}

// TestRandomSequencesAgainstNaive 用 2000 组随机调用序列对照朴素模拟，
// 并对同一序列做双实例重放以验证确定性。
func TestRandomSequencesAgainstNaive(t *testing.T) {
	const sequences = 2000
	for seed := int64(0); seed < sequences; seed++ {
		r := rand.New(rand.NewSource(seed))
		n, seq := genSequence(r)
		m1, err := NewManager(n)
		if err != nil {
			t.Fatalf("seed %d: NewManager(%d): %v", seed, n, err)
		}
		m2, _ := NewManager(n)
		nm := newNaive(n)
		t.Logf("seed=%d N=%d ops=%d", seed, n, len(seq))
		for i, o := range seq {
			ctx := fmt.Sprintf("seed=%d op[%d]=%s", seed, i, o)
			switch o.kind {
			case opBegin:
				id1, id2, id3 := m1.Begin(), m2.Begin(), nm.begin()
				t.Logf("%s -> %d", ctx, id1)
				if id1 != id2 || id1 != id3 {
					t.Fatalf("%s: begin id mismatch %d/%d/%d", ctx, id1, id2, id3)
				}
			case opLock:
				e1, e2, e3 := m1.Lock(o.tx, o.obj), m2.Lock(o.tx, o.obj), nm.lock(o.tx, o.obj)
				t.Logf("%s -> %s", ctx, describeErr(e1))
				if !sameReject(e1, e3) || !sameReject(e2, e3) {
					t.Fatalf("%s: got %v / %v, naive %v", ctx, e1, e2, e3)
				}
			case opDonate:
				e1, e2, e3 := m1.Donate(o.tx, o.obj), m2.Donate(o.tx, o.obj), nm.donate(o.tx, o.obj)
				t.Logf("%s -> %s", ctx, describeErr(e1))
				if !sameReject(e1, e3) || !sameReject(e2, e3) {
					t.Fatalf("%s: got %v / %v, naive %v", ctx, e1, e2, e3)
				}
			case opFinish:
				e1, e2, e3 := m1.Finish(o.tx), m2.Finish(o.tx), nm.finish(o.tx)
				t.Logf("%s -> %s", ctx, describeErr(e1))
				if !sameReject(e1, e3) || !sameReject(e2, e3) {
					t.Fatalf("%s: got %v / %v, naive %v", ctx, e1, e2, e3)
				}
			case opAbort:
				a1, e1 := m1.Abort(o.tx)
				a2, e2 := m2.Abort(o.tx)
				a3, e3 := nm.abort(o.tx)
				t.Logf("%s -> %v %s", ctx, a1, describeErr(e1))
				if !sameReject(e1, e3) || !sameReject(e2, e3) {
					t.Fatalf("%s: got %v / %v, naive %v", ctx, e1, e2, e3)
				}
				if !reflect.DeepEqual(a1, a3) || !reflect.DeepEqual(a2, a3) {
					t.Fatalf("%s: abort sets %v / %v, naive %v", ctx, a1, a2, a3)
				}
			}
			checkInvariants(t, m1, ctx)
		}
	}
}

// TestConcurrentLinearizable 并发调用下结果等价于某个串行顺序：
// 以 -race 运行，并在结束后校验结构不变量。
func TestConcurrentLinearizable(t *testing.T) {
	m, err := NewManager(8)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var beginMu sync.Mutex
	maxTx := 0
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			r := rand.New(rand.NewSource(seed))
			for i := 0; i < 200; i++ {
				beginMu.Lock()
				cur := maxTx
				beginMu.Unlock()
				tx := 1
				if cur > 0 {
					tx = 1 + r.Intn(cur+1)
				}
				obj := r.Intn(10)
				switch r.Intn(6) {
				case 0:
					id := m.Begin()
					beginMu.Lock()
					if id > maxTx {
						maxTx = id
					}
					beginMu.Unlock()
				case 1, 2:
					_ = m.Lock(tx, obj)
				case 3:
					_ = m.Donate(tx, obj)
				case 4:
					_ = m.Finish(tx)
				case 5:
					_, _ = m.Abort(tx)
				}
			}
		}(int64(g) + 1)
	}
	wg.Wait()
	checkInvariants(t, m, "post-concurrency")
}
