package leave_test

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"

	"ontology/leave"
)

func concConfig() leave.Config {
	return leave.Config{
		TenureBounds:  []int{1},
		AnnualQuotas:  []int{60, 80},
		CarryCap:      6,
		CarryDeadline: 100,
	}
}

type scriptedOp struct {
	kind   string
	from   int
	length int
}

func scriptFor(seed int64, rounds int) []scriptedOp {
	rng := rand.New(rand.NewSource(seed))
	ops := make([]scriptedOp, rounds)
	for r := 0; r < rounds; r++ {
		now := r * 3
		switch x := rng.Intn(10); {
		case x < 5:
			ops[r] = scriptedOp{kind: "request", from: now, length: 1 + rng.Intn(6)}
		case x < 7:
			ops[r] = scriptedOp{kind: "approve"}
		case x < 8:
			ops[r] = scriptedOp{kind: "withdraw"}
		case x < 9:
			ops[r] = scriptedOp{kind: "cancel"}
		default:
			ops[r] = scriptedOp{kind: "balance"}
		}
	}
	return ops
}

// empState 是一名员工脚本执行的上下文（供串行/并发两条路径复用）。
type empState struct {
	pending, approved []int64
	reqSeq            int
}

// runRound 执行一名员工一轮的脚本操作，返回结果描述。
func runRound(s *leave.Service, st *empState, emp string, r int, op scriptedOp) string {
	now := r * 3
	var res string
	switch op.kind {
	case "request":
		id, charges, err := s.RequestLeave(now, emp, op.from, op.from+op.length)
		if err != nil {
			res = catOf(err)
		} else {
			st.reqSeq++
			res = fmt.Sprintf("ok req#%d nch=%d", st.reqSeq, len(charges))
			st.pending = append(st.pending, id)
		}
	case "approve":
		if len(st.pending) == 0 {
			res = "skip"
			break
		}
		id := st.pending[0]
		st.pending = st.pending[1:]
		if err := s.Approve(now, emp, id); err != nil {
			res = catOf(err)
			st.pending = append([]int64{id}, st.pending...)
		} else {
			res = "ok"
			st.approved = append(st.approved, id)
		}
	case "withdraw":
		if len(st.pending) == 0 {
			res = "skip"
			break
		}
		id := st.pending[0]
		st.pending = st.pending[1:]
		res = catOf(s.Withdraw(now, emp, id))
	case "cancel":
		if len(st.approved) == 0 {
			res = "skip"
			break
		}
		id := st.approved[0]
		st.approved = st.approved[1:]
		res = catOf(s.CancelLeave(now, emp, id))
	default:
		b, err := s.Balance(now, emp)
		if err != nil {
			res = catOf(err)
		} else {
			res = fmt.Sprintf("%+v", b)
		}
	}
	if res == "" {
		res = "ok"
	}
	return res
}

// 并发等价：多员工并发执行与串行执行结果完全一致。
func TestConcurrentEquivalence(t *testing.T) {
	const emps = 8
	const rounds = 80
	cfg := concConfig()

	// 串行参考
	ref := newService(t, cfg)
	want := make([][]string, emps)
	for i := 0; i < emps; i++ {
		emp := fmt.Sprintf("emp%d", i)
		mustRegister(t, ref, 0, emp, 0)
		want[i] = make([]string, rounds)
	}
	refStates := make([]empState, emps)
	refScripts := make([][]scriptedOp, emps)
	for i := 0; i < emps; i++ {
		refScripts[i] = scriptFor(int64(i), rounds)
	}
	for r := 0; r < rounds; r++ {
		for i := 0; i < emps; i++ {
			want[i][r] = runRound(ref, &refStates[i], fmt.Sprintf("emp%d", i), r, refScripts[i][r])
		}
	}

	// 并发执行：每轮一个屏障，同一轮内所有员工并发（now 相同）
	conc := newService(t, cfg)
	for i := 0; i < emps; i++ {
		mustRegister(t, conc, 0, fmt.Sprintf("emp%d", i), 0)
	}
	got := make([][]string, emps)
	var wg sync.WaitGroup
	type roundSync struct {
		arrive  chan struct{}
		release chan struct{}
	}
	syncs := make([]*roundSync, rounds)
	for r := range syncs {
		syncs[r] = &roundSync{arrive: make(chan struct{}, emps), release: make(chan struct{})}
	}
	for i := 0; i < emps; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			emp := fmt.Sprintf("emp%d", i)
			var st empState
			res := make([]string, rounds)
			for r := 0; r < rounds; r++ {
				syncs[r].arrive <- struct{}{}
				<-syncs[r].release
				res[r] = runRound(conc, &st, emp, r, refScripts[i][r])
			}
			got[i] = res
		}(i)
	}
	for r := 0; r < rounds; r++ {
		for i := 0; i < emps; i++ {
			<-syncs[r].arrive
		}
		close(syncs[r].release)
	}
	wg.Wait()

	for i := 0; i < emps; i++ {
		for r := 0; r < rounds; r++ {
			if got[i][r] != want[i][r] {
				t.Fatalf("emp%d round %d: concurrent=%q serial=%q", i, r, got[i][r], want[i][r])
			}
		}
	}
	// 最终余额一致
	for i := 0; i < emps; i++ {
		emp := fmt.Sprintf("emp%d", i)
		b1 := mustBalance(t, ref, rounds*3, emp)
		b2 := mustBalance(t, conc, rounds*3, emp)
		if b1 != b2 {
			t.Fatalf("final balance %s: concurrent=%+v serial=%+v", emp, b2, b1)
		}
	}
}

// 同一员工高并发相同申请：恰好一个成功，其余区间重叠。
func TestConcurrentSameEmployeeContention(t *testing.T) {
	s := newService(t, concConfig())
	mustRegister(t, s, 0, "e", 0)

	const goroutines = 32
	start := make(chan struct{})
	var wg sync.WaitGroup
	results := make([]error, goroutines)
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, _, err := s.RequestLeave(5, "e", 10, 12)
			results[i] = err
		}(i)
	}
	close(start)
	wg.Wait()

	succeeded := 0
	for i, err := range results {
		if err == nil {
			succeeded++
			continue
		}
		cat, _ := leave.CategoryOf(err)
		if cat != leave.CatOverlap {
			t.Fatalf("goroutine %d: unexpected error %v", i, err)
		}
	}
	if succeeded != 1 {
		t.Fatalf("succeeded = %d, want exactly 1", succeeded)
	}
	b := mustBalance(t, s, 5, "e")
	if b.CurrentPending != 3 {
		t.Fatalf("pending = %d, want 3 (single 3-day leave)", b.CurrentPending)
	}
}
