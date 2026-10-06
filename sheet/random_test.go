package sheet

// 随机对照测试：用同一随机操作序列同时驱动正式实现与朴素模型，
// 逐操作比对返回结果，并在序列结束后比对完整状态。
// 日志（go test -v 可见，失败时自动打印）记录每步的输入、输出与判定依据。

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

// randOp 是一次随机操作的描述。
type randOp struct {
	kind  string // apply/undo/redo/protect/unprotect/cell/history
	user  string
	edits []Edit
	cell  string
	now   int64
}

func (op randOp) String() string {
	switch op.kind {
	case "apply":
		return fmt.Sprintf("Apply(%s, %v, now=%d)", op.user, op.edits, op.now)
	case "cell":
		return fmt.Sprintf("Cell(%q)", op.cell)
	case "history":
		return fmt.Sprintf("History(%s)", op.user)
	default:
		return fmt.Sprintf("%s(%s, %q, now=%d)", op.kind, op.user, op.cell, op.now)
	}
}

var randUsers = []string{"alice", "bob", "carol", "dave"}

const randKeySpace = 8 // 小键空间以制造冲突与覆盖

func randKey(r *rand.Rand) string {
	return fmt.Sprintf("c%d", r.Intn(randKeySpace))
}

// genOps 生成一条随机操作序列。now 大部分时候单调前进，
// 小概率随机取值以触发时钟回退；小概率注入非法参数。
func genOps(r *rand.Rand, n int) []randOp {
	ops := make([]randOp, 0, n)
	var cursor int64
	nextNow := func() int64 {
		if r.Intn(100) < 15 {
			// 随机时刻：可能回退
			return r.Int63n(cursor + 3)
		}
		cursor += int64(r.Intn(3))
		return cursor
	}
	for i := 0; i < n; i++ {
		user := randUsers[r.Intn(len(randUsers))]
		// 小概率使用未知用户或非法 now，覆盖参数非法分支
		if r.Intn(100) < 3 {
			user = "ghost"
		}
		now := nextNow()
		if r.Intn(100) < 2 {
			now = MaxNow + 1
		}
		switch x := r.Intn(100); {
		case x < 42: // Apply
			m := 1 + r.Intn(4)
			edits := make([]Edit, 0, m)
			used := map[string]bool{}
			for j := 0; j < m; j++ {
				k := randKey(r)
				if used[k] {
					continue
				}
				used[k] = true
				if r.Intn(100) < 25 {
					edits = append(edits, Edit{Key: k, Clear: true})
				} else {
					edits = append(edits, Edit{Key: k, Value: int64(r.Intn(6))})
				}
			}
			if len(edits) == 0 {
				edits = append(edits, Edit{Key: randKey(r), Value: 1})
			}
			// 小概率注入重复键 / 空键 / 越界值
			switch r.Intn(100) {
			case 0:
				edits = append(edits, edits[0])
			case 1:
				edits = append(edits, Edit{Key: "", Value: 1})
			case 2:
				edits = append(edits, Edit{Key: randKey(r), Value: MaxValue + 1})
			}
			ops = append(ops, randOp{kind: "apply", user: user, edits: edits, now: now})
		case x < 58:
			ops = append(ops, randOp{kind: "undo", user: user, now: now})
		case x < 72:
			ops = append(ops, randOp{kind: "redo", user: user, now: now})
		case x < 82:
			ops = append(ops, randOp{kind: "protect", user: user, cell: randKey(r), now: now})
		case x < 90:
			ops = append(ops, randOp{kind: "unprotect", user: user, cell: randKey(r), now: now})
		case x < 96:
			ops = append(ops, randOp{kind: "cell", cell: randKey(r)})
		default:
			ops = append(ops, randOp{kind: "history", user: user})
		}
	}
	return ops
}

// runOne 在正式实现上执行一个操作并返回可比对的结果。
func runOne(s *Service, op randOp) (Result, CellInfo, HistoryInfo, bool) {
	switch op.kind {
	case "apply":
		r := s.Apply(op.user, op.edits, op.now)
		return r, CellInfo{}, HistoryInfo{}, true
	case "undo":
		r := s.Undo(op.user, op.now)
		return r, CellInfo{}, HistoryInfo{}, true
	case "redo":
		r := s.Redo(op.user, op.now)
		return r, CellInfo{}, HistoryInfo{}, true
	case "protect":
		r := s.Protect(op.user, op.cell, op.now)
		return r, CellInfo{}, HistoryInfo{}, true
	case "unprotect":
		r := s.Unprotect(op.user, op.cell, op.now)
		return r, CellInfo{}, HistoryInfo{}, true
	case "cell":
		info, ok := s.Cell(op.cell)
		return Result{}, info, HistoryInfo{}, ok
	default:
		h, ok := s.History(op.user)
		return Result{}, CellInfo{}, h, ok
	}
}

// runOneNaive 在朴素模型上执行同一操作。
func runOneNaive(n *naiveService, op randOp) (Result, CellInfo, HistoryInfo, bool) {
	switch op.kind {
	case "apply":
		r := n.Apply(op.user, op.edits, op.now)
		return r, CellInfo{}, HistoryInfo{}, true
	case "undo":
		r := n.Undo(op.user, op.now)
		return r, CellInfo{}, HistoryInfo{}, true
	case "redo":
		r := n.Redo(op.user, op.now)
		return r, CellInfo{}, HistoryInfo{}, true
	case "protect":
		r := n.Protect(op.user, op.cell, op.now)
		return r, CellInfo{}, HistoryInfo{}, true
	case "unprotect":
		r := n.Unprotect(op.user, op.cell, op.now)
		return r, CellInfo{}, HistoryInfo{}, true
	case "cell":
		info, ok := n.Cell(op.cell)
		return Result{}, info, HistoryInfo{}, ok
	default:
		h, ok := n.History(op.user)
		return Result{}, CellInfo{}, h, ok
	}
}

// dumpState 导出正式实现的完整状态用于终态比对。
func dumpState(s *Service, users []string) string {
	var cells []string
	for i := 0; i < randKeySpace; i++ {
		k := fmt.Sprintf("c%d", i)
		info, _ := s.Cell(k)
		cells = append(cells, fmt.Sprintf("%s=%+v", k, info))
	}
	sort.Strings(cells)
	var hist []string
	for _, u := range users {
		h, _ := s.History(u)
		hist = append(hist, fmt.Sprintf("%s:%+v", u, h))
	}
	return fmt.Sprintf("rev=%d cells=%v hist=%v", s.Revision(), cells, hist)
}

func dumpStateNaive(n *naiveService, users []string) string {
	var cells []string
	for i := 0; i < randKeySpace; i++ {
		k := fmt.Sprintf("c%d", i)
		info, _ := n.Cell(k)
		cells = append(cells, fmt.Sprintf("%s=%+v", k, info))
	}
	sort.Strings(cells)
	var hist []string
	for _, u := range users {
		h, _ := n.History(u)
		hist = append(hist, fmt.Sprintf("%s:%+v", u, h))
	}
	return fmt.Sprintf("rev=%d cells=%v hist=%v", n.rev, cells, hist)
}

func TestRandomDifferential(t *testing.T) {
	const sequences = 1500
	const baseSeed = 20261007
	depths := map[string]int{"alice": 2, "bob": 3, "carol": 4, "dave": 5}

	for seq := 0; seq < sequences; seq++ {
		seed := int64(baseSeed + seq)
		r := rand.New(rand.NewSource(seed))
		ops := genOps(r, 20+r.Intn(40))

		svc, err := NewService(depths)
		if err != nil {
			t.Fatalf("NewService: %v", err)
		}
		naive := newNaiveService(depths)

		mismatch := false
		for i, op := range ops {
			gotR, gotC, gotH, gotOK := runOne(svc, op)
			wantR, wantC, wantH, wantOK := runOneNaive(naive, op)
			same := gotR == wantR && gotC == wantC && gotOK == wantOK &&
				reflect.DeepEqual(gotH, wantH)
			// 日志：输入、双方输出、判定依据
			t.Logf("seq=%d seed=%d op#%d in=%s | impl=(%v %v %v ok=%v) model=(%v %v %v ok=%v) | 判定: 逐项比对 Code/Cell/Popped/Revision 与查询结果 => match=%v",
				seq, seed, i, op, gotR, gotC, gotH, gotOK, wantR, wantC, wantH, wantOK, same)
			if !same {
				t.Errorf("seq=%d seed=%d op#%d 不一致\n  输入: %s\n  实现: %+v %+v %+v ok=%v\n  模型: %+v %+v %+v ok=%v",
					seq, seed, i, op, gotR, gotC, gotH, gotOK, wantR, wantC, wantH, wantOK)
				mismatch = true
				break
			}
		}
		if mismatch {
			continue
		}
		// 终态比对：全部单元格、修订号、所有用户历史
		ds, dn := dumpState(svc, randUsers), dumpStateNaive(naive, randUsers)
		t.Logf("seq=%d seed=%d 终态判定: 全单元格+修订号+历史 比对 => match=%v", seq, seed, ds == dn)
		if ds != dn {
			t.Errorf("seq=%d seed=%d 终态不一致\n  实现: %s\n  模型: %s", seq, seed, ds, dn)
		}
	}
}
