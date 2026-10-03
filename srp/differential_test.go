package srp

import (
	"fmt"
	"math/rand"
	"testing"
)

const diffSequences = 2000

// genSequence 生成一串混合合法与非法操作。生成时用朴素模拟同步推进，
// 从而既覆盖真实栈路径（取放/结束优先指向当前栈作业），也注入非法输入。
func genSequence(rng *rand.Rand) []op {
	n := newNaive()
	var ops []op
	emit := func(o op) naiveResult {
		ops = append(ops, o)
		return n.run(o)
	}

	nRes := rng.Intn(4) + 1
	resN := map[string]int{}
	resIDs := []string{}
	for i := 0; i < nRes; i++ {
		id := fmt.Sprintf("R%d", i)
		nv := rng.Intn(6) + 1
		resN[id] = nv
		resIDs = append(resIDs, id)
		emit(op{kind: "Declare", a: id, n: nv})
	}
	emit(op{kind: "Declare", a: "", n: 3})
	emit(op{kind: "Declare", a: "RX", n: 0})

	nTasks := rng.Intn(5) + 1
	taskIDs := []string{}
	for i := 0; i < nTasks; i++ {
		id := fmt.Sprintf("T%d", i)
		d := rng.Intn(20) + 1
		var mus []Mu
		for _, ri := range rng.Perm(len(resIDs))[:rng.Intn(len(resIDs)+1)] {
			rid := resIDs[ri]
			mus = append(mus, Mu{Resource: rid, Units: rng.Intn(resN[rid]) + 1})
		}
		taskIDs = append(taskIDs, id)
		emit(op{kind: "Add", a: id, n: d, mus: mus})
	}
	// 注入结构性非法任务。
	emit(op{kind: "Add", a: "BadDup", n: 5, mus: []Mu{{resIDs[0], 1}, {resIDs[0], 1}}})
	emit(op{kind: "Add", a: "BadOver", n: 5, mus: []Mu{{resIDs[0], resN[resIDs[0]] + 1}}})
	emit(op{kind: "Add", a: "BadD", n: 0})
	emit(op{kind: "Add", a: taskIDs[0], n: 9})

	jobSeq := 0
	newJobID := func() string {
		j := fmt.Sprintf("j%d", jobSeq)
		jobSeq++
		return j
	}
	pickTop := func() string {
		st := n.stack
		j := st[len(st)-1]
		if len(st) > 1 && rng.Intn(4) == 0 {
			j = st[rng.Intn(len(st)-1)] // 故意指向非栈顶
		}
		return j
	}

	for step := 0; step < 60+rng.Intn(60); step++ {
		choice := rng.Intn(100)
		switch {
		case choice < 30:
			tid := taskIDs[rng.Intn(len(taskIDs))]
			j := newJobID()
			switch rng.Intn(12) {
			case 0:
				tid = "Ghost"
			case 1:
				j = ""
			case 2:
				tid = ""
			case 3:
				if len(n.stack) > 0 {
					j = n.stack[rng.Intn(len(n.stack))] // 重名
				}
			}
			emit(op{kind: "Start", a: j, b: tid})
		case choice < 55 && len(n.stack) > 0:
			j := pickTop()
			if rng.Intn(10) == 0 {
				j = "GhostJob"
			}
			r := resIDs[rng.Intn(len(resIDs))]
			u := rng.Intn(resN[r]+2) + 1
			if rng.Intn(20) == 0 {
				u = 1001
			}
			kind := "Acquire"
			if rng.Intn(2) == 0 {
				kind = "Release"
			}
			emit(op{kind: kind, a: j, b: r, n: u})
		case choice < 68 && len(n.stack) > 0:
			j := pickTop()
			if rng.Intn(10) == 0 {
				j = "GhostJob"
			}
			emit(op{kind: "Finish", a: j})
		case choice < 78:
			tid := taskIDs[rng.Intn(len(taskIDs))]
			if rng.Intn(8) == 0 {
				tid = "Ghost"
			}
			emit(op{kind: "Remove", a: tid})
		default:
			qs := []string{"QCeil", "QAvail", "QLevel", "QSys", "QStack"}
			k := qs[rng.Intn(len(qs))]
			o := op{kind: k}
			switch k {
			case "QCeil", "QAvail":
				o.a = resIDs[rng.Intn(len(resIDs))]
				if rng.Intn(8) == 0 {
					o.a = "GhostR"
				}
			case "QLevel":
				o.a = taskIDs[rng.Intn(len(taskIDs))]
				if rng.Intn(8) == 0 {
					o.a = "GhostT"
				}
			}
			emit(o)
		}
	}
	return ops
}

type realRunner struct{ s *SRP }

func (r realRunner) run(o op) naiveResult {
	var err error
	res := naiveResult{ok: true}
	switch o.kind {
	case "Declare":
		err = r.s.DeclareResource(o.a, o.n)
	case "Add":
		err = r.s.AddTask(o.a, o.n, o.mus...)
	case "Remove":
		err = r.s.RemoveTask(o.a)
	case "Start":
		err = r.s.Start(o.a, o.b)
	case "Acquire":
		err = r.s.Acquire(o.a, o.b, o.n)
	case "Release":
		err = r.s.Release(o.a, o.b, o.n)
	case "Finish":
		err = r.s.Finish(o.a)
	case "QCeil":
		res.intVal, err = r.s.Ceil(o.a)
	case "QAvail":
		res.intVal, err = r.s.Avail(o.a)
	case "QLevel":
		res.intVal, err = r.s.Level(o.a)
	case "QSys":
		res.intVal = r.s.SysCeil()
	case "QStack":
		for _, j := range r.s.Stack() {
			res.stackV = append(res.stackV, j.ID)
		}
	}
	if err != nil {
		return naiveResult{ok: false, reason: reasonOf(err)}
	}
	return res
}

type snapshot struct {
	avail map[string]int
	ceil  map[string]int
	level map[string]int
	sys   int
	stack []string
}

func takeSnapshot(s *SRP, res, tasks []string) snapshot {
	sn := snapshot{avail: map[string]int{}, ceil: map[string]int{}, level: map[string]int{}}
	for _, r := range res {
		if a, err := s.Avail(r); err == nil {
			sn.avail[r] = a
		}
		if c, err := s.Ceil(r); err == nil {
			sn.ceil[r] = c
		}
	}
	for _, t := range tasks {
		if l, err := s.Level(t); err == nil {
			sn.level[t] = l
		}
	}
	sn.sys = s.SysCeil()
	for _, j := range s.Stack() {
		sn.stack = append(sn.stack, j.ID)
	}
	return sn
}

func takeNaiveSnapshot(nm *naiveSim, res, tasks []string) snapshot {
	sn := snapshot{avail: map[string]int{}, ceil: map[string]int{}, level: map[string]int{}}
	for _, r := range res {
		if _, ok := nm.res[r]; ok {
			sn.avail[r] = nm.avail[r]
			sn.ceil[r] = nm.ceil(r)
		}
	}
	for _, t := range tasks {
		if _, ok := nm.taskD[t]; ok {
			sn.level[t] = nm.level(t)
		}
	}
	sn.sys = nm.sysCeil()
	sn.stack = append([]string(nil), nm.stack...)
	return sn
}

func snapshotsEqual(a, b snapshot) (string, bool) {
	if fmt.Sprint(a.avail) != fmt.Sprint(b.avail) {
		return fmt.Sprintf("avail %v != %v", a.avail, b.avail), false
	}
	if fmt.Sprint(a.ceil) != fmt.Sprint(b.ceil) {
		return fmt.Sprintf("ceil %v != %v", a.ceil, b.ceil), false
	}
	if fmt.Sprint(a.level) != fmt.Sprint(b.level) {
		return fmt.Sprintf("level %v != %v", a.level, b.level), false
	}
	if a.sys != b.sys {
		return fmt.Sprintf("sys %d != %d", a.sys, b.sys), false
	}
	if !eqStrings(a.stack, b.stack) {
		return fmt.Sprintf("stack %v != %v", a.stack, b.stack), false
	}
	return "", true
}

func TestDifferentialNaive2000(t *testing.T) {
	for seq := 0; seq < diffSequences; seq++ {
		ops := genSequence(rand.New(rand.NewSource(int64(seq) + 1)))
		s := New()
		nm := newNaive()
		verbose := seq < 3
		var declaredRes, declaredTasks []string
		for i, o := range ops {
			rr := realRunner{s}.run(o)
			nr := nm.run(o)
			if verbose {
				out := "OK"
				if !nr.ok {
					out = "REJECT " + string(nr.reason)
				} else if len(o.kind) > 0 && o.kind[0] == 'Q' {
					out = fmt.Sprintf("OK int=%d stack=%v", nr.intVal, nr.stackV)
				}
				t.Logf("[seq=%d step=%d] INPUT %s | OUTPUT %s | JUDGE 与朴素模拟逐字段比对",
					seq, i, o, out)
			}
			if rr.ok != nr.ok || rr.reason != nr.reason || rr.intVal != nr.intVal ||
				!eqStrings(rr.stackV, nr.stackV) {
				t.Fatalf("seq=%d step=%d op=%s\nreal=%+v\nnaive=%+v", seq, i, o, rr, nr)
			}
			if o.kind == "Declare" && rr.ok {
				declaredRes = append(declaredRes, o.a)
			}
			if o.kind == "Add" && rr.ok {
				declaredTasks = append(declaredTasks, o.a)
			}
			sn1 := takeSnapshot(s, declaredRes, declaredTasks)
			sn2 := takeNaiveSnapshot(nm, declaredRes, declaredTasks)
			if msg, ok := snapshotsEqual(sn1, sn2); !ok {
				t.Fatalf("seq=%d step=%d op=%s snapshot mismatch: %s", seq, i, o, msg)
			}
			assertInvariant(t, s, o.String())
		}
		if c := s.shortageRejectionsLocked(); c != nm.short {
			t.Fatalf("seq=%d shortage counter real=%d naive=%d", seq, c, nm.short)
		} else if c != 0 {
			t.Fatalf("seq=%d: 合法 μ 序列下单元不足 %d 次（SRP 定理要求 0）", seq, c)
		}
		if seq%400 == 399 {
			t.Logf("progress: %d/%d 随机序列与朴素模拟完全一致，单元不足计数恒为 0", seq+1, diffSequences)
		}
	}
}

func TestReplayDeterminism(t *testing.T) {
	for seq := 0; seq < 50; seq++ {
		ops := genSequence(rand.New(rand.NewSource(int64(seq) + 777)))
		s1, s2 := New(), New()
		n1, n2 := newNaive(), newNaive()
		var res, tasks []string
		for i, o := range ops {
			r1 := realRunner{s1}.run(o)
			r2 := realRunner{s2}.run(o)
			na := n1.run(o)
			n2.run(o)
			if r1.ok != r2.ok || r1.reason != r2.reason || r1.intVal != r2.intVal ||
				!eqStrings(r1.stackV, r2.stackV) {
				t.Fatalf("seq=%d step=%d op=%s replay differs: %+v vs %+v", seq, i, o, r1, r2)
			}
			if r1.ok != na.ok || r1.reason != na.reason {
				t.Fatalf("seq=%d step=%d op=%s real/naive differs", seq, i, o)
			}
			if o.kind == "Declare" && r1.ok {
				res = append(res, o.a)
			}
			if o.kind == "Add" && r1.ok {
				tasks = append(tasks, o.a)
			}
			if msg, ok := snapshotsEqual(takeSnapshot(s1, res, tasks), takeSnapshot(s2, res, tasks)); !ok {
				t.Fatalf("seq=%d step=%d replay snapshot mismatch: %s", seq, i, msg)
			}
		}
	}
	t.Log("REPLAY: 50 组序列各重放两次，操作结果与全部查询快照逐字段相同")
}
