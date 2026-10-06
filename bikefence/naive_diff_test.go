package bikefence

import (
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"testing"
)

type opKind int

const (
	opRegister opKind = iota
	opRegisterBike
	opReturn
	opUnlock
	opClaim
	opComplete
	opClockBack
)

type diffOp struct {
	kind      opKind
	id        string
	id2       string
	p         Point
	at        int64
	fenceKind FenceKind
	verts     []Point
	cap       int
	taskRef   int // >=0 时指向任务序号（按创建顺序），执行时解析为真实 ID
}

func pick(rng *rand.Rand, xs []string) string { return xs[rng.Intn(len(xs))] }

func containsStr(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func opName(o diffOp) string {
	switch o.kind {
	case opRegister:
		return fmt.Sprintf("Register(%s,%s,cap=%d,at=%d)", o.id, o.fenceKind, o.cap, o.at)
	case opRegisterBike:
		return fmt.Sprintf("RegisterBike(%q,at=%d)", o.id, o.at)
	case opReturn:
		return fmt.Sprintf("Return(%s,%s,(%d,%d),at=%d)", o.id, o.id2, o.p.X, o.p.Y, o.at)
	case opUnlock:
		return fmt.Sprintf("Unlock(%s,at=%d)", o.id, o.at)
	case opClaim:
		return fmt.Sprintf("Claim(%s,%s,at=%d)", o.id, o.id2, o.at)
	case opComplete:
		return fmt.Sprintf("Complete(%s,%s,(%d,%d),at=%d)", o.id, o.id2, o.p.X, o.p.Y, o.at)
	case opClockBack:
		return fmt.Sprintf("ClockBackAttempt(at=%d)", o.at)
	}
	return "?"
}

// TestNaiveDifferential 用固定种子的随机操作序列驱动主模型与朴素模型，
// 逐步对照返回值、错误类别与完整状态快照；全部输入/输出/依据写入 differential.log。
func TestNaiveDifferential(t *testing.T) {
	logPath := "differential.log"
	lf, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer lf.Close()

	cfg := testConfig()
	cfg.Log = lf
	main, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	naive, err := NewNaive(cfg)
	if err != nil {
		t.Fatal(err)
	}

	now := int64(1000)
	ops := []diffOp{
		{kind: opRegister, id: "OP1", fenceKind: KindOperating, cap: 6, at: now,
			verts: []Point{{0, 0}, {120, 0}, {120, 120}, {0, 120}}},
		{kind: opRegister, id: "OP2", fenceKind: KindOperating, cap: 4, at: now + 1,
			verts: []Point{{300, 0}, {420, 0}, {420, 120}, {300, 120}}},
		{kind: opRegister, id: "NP1", fenceKind: KindNoParking, cap: 3, at: now + 2,
			verts: []Point{{10, 10}, {40, 10}, {40, 40}, {10, 40}}},
		{kind: opRegister, id: "RW1", fenceKind: KindReward, cap: 3, at: now + 3,
			verts: []Point{{60, 10}, {120, 10}, {120, 50}, {60, 50}}},
		{kind: opRegister, id: "RW2", fenceKind: KindReward, cap: 5, at: now + 4,
			verts: []Point{{300, 10}, {360, 10}, {360, 90}, {300, 90}}},
	}
	now += 5

	rng := rand.New(rand.NewSource(20261006))
	bikes := []string{}
	for i := 0; i < 30; i++ {
		id := "bk" + itoa(i)
		ops = append(ops, diffOp{kind: opRegisterBike, id: id, at: now})
		bikes = append(bikes, id)
		now += int64(1 + rng.Intn(3))
	}

	workers := []string{"w1", "w2", "w3"}
	randPoint := func() Point {
		switch rng.Intn(10) {
		case 0:
			return Point{int64(25 + rng.Intn(10)), int64(25 + rng.Intn(10))}
		case 1:
			return Point{120, int64(20 + rng.Intn(25))}
		case 2, 3:
			return Point{int64(65 + rng.Intn(50)), int64(12 + rng.Intn(35))}
		case 4, 5:
			return Point{int64(5 + rng.Intn(110)), int64(55 + rng.Intn(60))}
		case 6, 7:
			return Point{int64(305 + rng.Intn(110)), int64(5 + rng.Intn(110))}
		default:
			return Point{int64(150 + rng.Intn(140)), int64(rng.Intn(200))}
		}
	}

	const total = 4000
	for len(ops) < total {
		now += int64(rng.Intn(4))
		switch rng.Intn(100) {
		case 0:
			ops = append(ops, diffOp{kind: opRegisterBike, id: "", at: now})
		case 1:
			ops = append(ops, diffOp{kind: opRegisterBike, id: pick(rng, bikes), at: now})
		case 2:
			ops = append(ops, diffOp{kind: opClockBack, id: "bk0", at: now - 1000})
		case 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17,
			18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32,
			33, 34, 35, 36, 37, 38, 39, 40, 41, 42, 43, 44, 45, 46, 47:
			ops = append(ops, diffOp{
				kind: opReturn, id: pick(rng, bikes),
				id2: "u" + itoa(rng.Intn(4)), p: randPoint(), at: now,
			})
		case 48, 49, 50, 51, 52, 53, 54, 55, 56, 57, 58, 59, 60, 61, 62:
			ops = append(ops, diffOp{kind: opUnlock, id: pick(rng, bikes), at: now})
		case 63, 64, 65, 66, 67, 68, 69, 70, 71, 72, 73:
			ops = append(ops, diffOp{
				kind:    opClaim,
				id:      "unknown",
				id2:     pick(rng, workers),
				at:      now,
				taskRef: rng.Intn(20),
			})
		default:
			ops = append(ops, diffOp{
				kind:    opComplete,
				id:      "nope",
				id2:     pick(rng, workers),
				p:       randPoint(),
				at:      now,
				taskRef: rng.Intn(20),
			})
		}
	}

	applyAndCompare(t, main, naive, ops, logPath)
}

type pairResult struct {
	r   Receipt
	err error
}

func resolveTaskID(created []string, o diffOp) string {
	if o.taskRef >= 0 && o.taskRef < len(created) {
		return created[o.taskRef]
	}
	return o.id
}

func execMain(s *Service, o diffOp, tasks []string) pairResult {
	switch o.kind {
	case opRegister:
		return pairResult{err: s.RegisterFence(o.id, o.fenceKind, o.verts, o.cap, o.at)}
	case opRegisterBike:
		return pairResult{err: s.RegisterBike(o.id, o.at)}
	case opReturn:
		r, e := s.ReturnBike(o.id, o.id2, o.p, o.at)
		return pairResult{r, e}
	case opUnlock:
		return pairResult{err: s.Unlock(o.id, o.at)}
	case opClaim:
		return pairResult{err: s.ClaimTask(resolveTaskID(tasks, o), o.id2, o.at)}
	case opComplete:
		return pairResult{err: s.CompleteTask(resolveTaskID(tasks, o), o.id2, o.p, o.at)}
	case opClockBack:
		return pairResult{err: s.Unlock(o.id, o.at)}
	}
	return pairResult{}
}

func execNaive(s *NaiveService, o diffOp, tasks []string) pairResult {
	switch o.kind {
	case opRegister:
		return pairResult{err: s.RegisterFence(o.id, o.fenceKind, o.verts, o.cap, o.at)}
	case opRegisterBike:
		return pairResult{err: s.RegisterBike(o.id, o.at)}
	case opReturn:
		r, e := s.ReturnBike(o.id, o.id2, o.p, o.at)
		return pairResult{r, e}
	case opUnlock:
		return pairResult{err: s.Unlock(o.id, o.at)}
	case opClaim:
		return pairResult{err: s.ClaimTask(resolveTaskID(tasks, o), o.id2, o.at)}
	case opComplete:
		return pairResult{err: s.CompleteTask(resolveTaskID(tasks, o), o.id2, o.p, o.at)}
	case opClockBack:
		return pairResult{err: s.Unlock(o.id, o.at)}
	}
	return pairResult{}
}

func sameResult(a, b pairResult) bool {
	if (a.err == nil) != (b.err == nil) {
		return false
	}
	if a.err != nil {
		return errKind(a.err) == errKind(b.err)
	}
	// Reason 是判定依据的人读诊断（主模型额外含 R 树访问数），不属于业务结果。
	a.r.Reason = ""
	b.r.Reason = ""
	return a.r == b.r
}

func normalizeTasks(in map[string]Task) map[string]Task {
	out := make(map[string]Task, len(in))
	for id, task := range in {
		task.Claims = nil
		out[id] = task
	}
	return out
}

func resultDesc(p pairResult) string {
	if p.err != nil {
		return "ERR " + p.err.Error()
	}
	return fmt.Sprintf("OK fence=%s outside=%v fee=%d reward=%d granted=%v task=%s reason=%q",
		p.r.FenceID, p.r.Outside, p.r.Fee, p.r.Reward, p.r.RewardGranted, p.r.TaskID, p.r.Reason)
}

func applyAndCompare(t *testing.T, main *Service, naive *NaiveService, ops []diffOp, logPath string) {
	t.Helper()
	var mainTasks, naiveTasks []string
	collect := func(snap Snapshot, seen *[]string) {
		for _, id := range tasksInOrder(snap) {
			if !containsStr(*seen, id) {
				*seen = append(*seen, id)
			}
		}
	}
	for i, o := range ops {
		pm := execMain(main, o, mainTasks)
		pn := execNaive(naive, o, naiveTasks)
		sm := main.SnapshotState()
		sn := naive.SnapshotState()
		collect(sm, &mainTasks)
		collect(sn, &naiveTasks)
		main.c.logf("[diff] op=%d %s -> %s", i, opName(o), resultDesc(pm))
		if !sameResult(pm, pn) {
			t.Fatalf("op %d %s result mismatch:\n main: %+v err=%v\n naive: %+v err=%v\n see %s",
				i, opName(o), pm.r, pm.err, pn.r, pn.err, logPath)
		}
		if !reflect.DeepEqual(sm.Counts, sn.Counts) ||
			!reflect.DeepEqual(sm.Bikes, sn.Bikes) ||
			!reflect.DeepEqual(normalizeTasks(sm.Tasks), normalizeTasks(sn.Tasks)) {
			t.Fatalf("op %d %s state mismatch:\n main counts=%v\n naive counts=%v\n see %s",
				i, opName(o), sm.Counts, sn.Counts, logPath)
		}
		if !reflect.DeepEqual(mainTasks, naiveTasks) {
			t.Fatalf("op %d task creation history mismatch: %v vs %v", i, mainTasks, naiveTasks)
		}
	}
	t.Logf("differential run of %d ops matched naive model; log: %s", len(ops), logPath)
}

// tasksInOrder 按创建序号（seq 单调）从任务 ID 无法直接排序，这里快照不含次序，
// 但两模型 ID 生成规则一致且逐 op 收集，故按 ID 字符串（O/E+seq）排序即可稳定复现。
func tasksInOrder(snap Snapshot) []string {
	ids := make([]string, 0, len(snap.Tasks))
	for id := range snap.Tasks {
		ids = append(ids, id)
	}
	for i := 1; i < len(ids); i++ {
		for j := i; j > 0 && taskSeq(ids[j]) < taskSeq(ids[j-1]); j-- {
			ids[j], ids[j-1] = ids[j-1], ids[j]
		}
	}
	return ids
}

func taskSeq(id string) int {
	n := 0
	for i := 1; i < len(id); i++ {
		n = n*10 + int(id[i]-'0')
	}
	return n
}

// TestDeterministicReplay 相同操作序列重放两次，归属/费用/任务历史完全一致。
func TestDeterministicReplay(t *testing.T) {
	run := func() Snapshot {
		s, err := New(testConfig())
		if err != nil {
			t.Fatal(err)
		}
		vs := []Point{{0, 0}, {100, 0}, {100, 100}, {0, 100}}
		if err := s.RegisterFence("OP1", KindOperating, vs, 3, 1); err != nil {
			t.Fatal(err)
		}
		rw := []Point{{80, 80}, {100, 80}, {100, 100}, {80, 100}}
		if err := s.RegisterFence("RW1", KindReward, rw, 2, 2); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 4; i++ {
			if err := s.RegisterBike(bikeName(i), int64(10+i)); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := s.ReturnBike("b0", "u1", Point{90, 90}, 20); err != nil {
			t.Fatal(err)
		}
		if _, err := s.ReturnBike("b1", "u1", Point{90, 90}, 21); err != nil {
			t.Fatal(err)
		}
		if _, err := s.ReturnBike("b2", "u2", Point{150, 150}, 22); err != nil {
			t.Fatal(err)
		}
		if _, err := s.ReturnBike("b3", "u3", Point{50, 50}, 23); err != nil {
			t.Fatal(err)
		}
		return s.SnapshotState()
	}
	a := run()
	b := run()
	if !reflect.DeepEqual(normalizeTasks(a.Tasks), normalizeTasks(b.Tasks)) ||
		!reflect.DeepEqual(a.Counts, b.Counts) || !reflect.DeepEqual(a.Bikes, b.Bikes) {
		t.Fatalf("replay differs:\n%+v\n%+v", a, b)
	}
}
