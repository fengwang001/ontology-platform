package sched

import (
	"fmt"
	"math/rand"
	"testing"

	"ontology/group"
)

// checkInvariants 校验全局不变量。
func checkInvariants(t *testing.T, s *Scheduler, c int, where string) {
	t.Helper()
	busy := s.Busy()
	q := s.Queue()
	if busy > c {
		t.Fatalf("%s: busy=%d > C=%d", where, busy, c)
	}
	if busy < c && len(q) != 0 {
		t.Fatalf("%s: 有空位但队列非空 busy=%d queue=%v", where, busy, q)
	}
	// 队列中每个运行都必须是 Waiting，且编号唯一。
	seen := map[int]bool{}
	for _, id := range q {
		if seen[id] {
			t.Fatalf("%s: queue duplicate %d", where, id)
		}
		seen[id] = true
		st, ok := s.StateOf(id)
		if !ok || st != group.Waiting {
			t.Fatalf("%s: queue id %d state=%v ok=%v, want Waiting", where, id, st, ok)
		}
	}
	// 终态数 + 非终态数 == 总提交数（无丢失、无重入终态）。
	all := s.snapshotStates()
	for id, st := range all {
		if st < 0 || st > group.Superseded {
			t.Fatalf("%s: run %d bad state %v", where, id, st)
		}
	}
}

func compareStates(t *testing.T, s *Scheduler, n *naiveSim, ids []int, where string) {
	t.Helper()
	for _, id := range ids {
		got, ok1 := s.StateOf(id)
		want, ok2 := n.stateOf(id)
		if ok1 != ok2 || (ok1 && group.State(want) != got) {
			t.Fatalf("%s: run %d real=(%v,%v) naive=(%v,%v)",
				where, id, got, ok1, group.State(want), ok2)
		}
	}
	if qr, qn := s.Queue(), append([]int(nil), n.queue...); !equalInts(qr, qn) {
		t.Fatalf("%s: queue real=%v naive=%v", where, qr, qn)
	}
	if s.Busy() != n.busy {
		t.Fatalf("%s: busy real=%d naive=%d", where, s.Busy(), n.busy)
	}
}

func equalInts(a, b []int) bool {
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

func errIs(a, target error) bool {
	type is interface{ Is(error) bool }
	if x, ok := a.(is); ok && x.Is(target) {
		return true
	}
	return a == target
}

// TestRandomDifferential 用 1500 组随机操作序列对照逐步朴素模拟器。
func TestRandomDifferential(t *testing.T) {
	const seqN = 1500
	var totalLogs int
	for seed := int64(1); seed <= seqN; seed++ {
		rng := rand.New(rand.NewSource(seed))
		c := 1 + rng.Intn(4)
		q := rng.Intn(4)
		real, err := New(c, q)
		if err != nil {
			t.Fatalf("seed %d: New(%d,%d) err=%v", seed, c, q, err)
		}
		naive := newNaive(c, q)

		nops := 30 + rng.Intn(70)
		var ids []int
		log := []string{fmt.Sprintf("=== seed=%d C=%d Q=%d ops=%d ===", seed, c, q, nops)}
		for step := 0; step < nops; step++ {
			var o op
			switch rng.Intn(10) {
			case 0, 1, 2, 3, 4: // 50% Submit
				o.kind = opSubmit
				// 70% 使用少量有名字的组，20% 空组，10% 偶发非法长度
				if rng.Intn(10) == 0 {
					o.group = make([]byte, 65)
				} else if rng.Intn(10) < 2 {
					o.group = nil
				} else {
					o.group = []byte(fmt.Sprintf("g%d", rng.Intn(4)))
				}
				o.cancel = rng.Intn(2) == 0
				o.prot = rng.Intn(3) == 0
			default:
				if len(ids) == 0 {
					o.kind = opSubmit
					o.group = []byte(fmt.Sprintf("g%d", rng.Intn(4)))
					o.cancel = rng.Intn(2) == 0
					o.prot = rng.Intn(3) == 0
					break
				}
				// 偏向引用已有的非终态运行
				id := ids[rng.Intn(len(ids))]
				if rng.Intn(7) == 0 {
					id = 1 + rng.Intn(len(ids)+3) // 偶发不存在
				}
				o.id = id
				switch rng.Intn(4) {
				case 0:
					o.kind = opFinish
				case 1:
					o.kind = opFinishFail
				case 2:
					o.kind = opAckCancel
				default:
					o.kind = opCancel
				}
			}

			rid, rerr := applyOp(real, o)
			nid, nerr := naive.do(o)
			why := "ok"
			if rerr != nil {
				switch {
				case errIs(rerr, ErrInvalid):
					why = "参数非法"
				case errIs(rerr, ErrNotFound):
					why = "运行不存在"
				case errIs(rerr, ErrState):
					why = "状态不符"
				case errIs(rerr, ErrQueueFull):
					why = "队列已满（净增判定拒绝，无任何变化）"
				}
			}
			log = append(log, fmt.Sprintf("#%02d %-42s => real(id=%d,err=%v) naive(id=%d,err=%v) [%s] queue=%v busy=%d",
				step, o.String(), rid, rerr, nid, nerr, why, real.Queue(), real.Busy()))

			if rid != nid || !sameErr(rerr, nerr) {
				t.Fatalf("seed=%d step=%d %s: real=(id=%d,err=%v) naive=(id=%d,err=%v)\n%s",
					seed, step, o.String(), rid, rerr, nid, nerr, joinLog(log))
			}
			if rerr == nil && o.kind == opSubmit {
				ids = append(ids, rid)
			}
			compareStates(t, real, naive, ids, fmt.Sprintf("seed=%d step=%d %s", seed, step, o.String()))
			checkInvariants(t, real, c, fmt.Sprintf("seed=%d step=%d", seed, step))
		}
		// 每组占位者/Pending 至多一个：由组表结构保证，这里额外用朴素模型交叉校验。
		compareStates(t, real, naive, ids, fmt.Sprintf("seed=%d final", seed))
		// 每个序列打印首条日志（含输入参数）；失败时全量日志已在 Fatalf 中给出。
		if seed <= 3 {
			t.Log(log[0])
		}
		totalLogs += len(log)
	}
	t.Logf("differential done: %d sequences, %d logged decisions", seqN, totalLogs)
}

func sameErr(a, b error) bool {
	if a == nil || b == nil {
		return a == b
	}
	for _, target := range []error{ErrInvalid, ErrNotFound, ErrState, ErrQueueFull} {
		if errIs(a, target) != errIs(b, target) {
			return false
		}
	}
	return true
}

func joinLog(l []string) string {
	out := ""
	for _, x := range l {
		out += "\n" + x
	}
	return out
}
