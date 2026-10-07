package sheet_test

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"

	"ontology/sheet"
	"ontology/sheet/naive"
)

// op 是一条随机生成的操作, 用于日志与重放。
type op struct {
	kind  string // "apply" / "undo" / "redo" / "protect" / "unprotect"
	user  string
	edits []sheet.Edit
	cell  string
	now   int64
}

func (o op) String() string {
	switch o.kind {
	case "apply":
		return fmt.Sprintf("Apply(%s, %v, now=%d)", o.user, o.edits, o.now)
	case "undo":
		return fmt.Sprintf("Undo(%s, now=%d)", o.user, o.now)
	case "redo":
		return fmt.Sprintf("Redo(%s, now=%d)", o.user, o.now)
	case "protect":
		return fmt.Sprintf("Protect(%s, %s, now=%d)", o.user, o.cell, o.now)
	default:
		return fmt.Sprintf("Unprotect(%s, %s, now=%d)", o.user, o.cell, o.now)
	}
}

func runOp(s *sheet.Service, m *naive.Model, o op) (sheet.Result, sheet.Result) {
	switch o.kind {
	case "apply":
		return s.Apply(o.user, o.edits, o.now), m.Apply(o.user, o.edits, o.now)
	case "undo":
		return s.Undo(o.user, o.now), m.Undo(o.user, o.now)
	case "redo":
		return s.Redo(o.user, o.now), m.Redo(o.user, o.now)
	case "protect":
		return s.Protect(o.user, o.cell, o.now), m.Protect(o.user, o.cell, o.now)
	default:
		return s.Unprotect(o.user, o.cell, o.now), m.Unprotect(o.user, o.cell, o.now)
	}
}

// genSequence 生成一条随机操作序列。小值域与小键池用于制造
// 同值无效编辑、互相覆盖与保护冲突。
func genSequence(rng *rand.Rand, users []string, keys []string, nOps int) []op {
	ops := make([]op, 0, nOps)
	lastNow := int64(0)
	for i := 0; i < nOps; i++ {
		// 80% 时钟前进, 20% 时钟回退（触发拒绝路径）。
		var now int64
		if rng.Intn(100) < 80 {
			now = lastNow + int64(rng.Intn(3))
		} else {
			now = lastNow - int64(rng.Intn(3)) - 1
		}
		if now >= 0 {
			lastNow = now
		}
		user := users[rng.Intn(len(users))]
		switch r := rng.Intn(100); {
		case r < 45:
			n := 1 + rng.Intn(4)
			perm := rng.Perm(len(keys))
			edits := make([]sheet.Edit, 0, n)
			for j := 0; j < n && j < len(keys); j++ {
				e := sheet.Edit{Key: keys[perm[j]]}
				if rng.Intn(100) < 70 {
					e.Value = int64(rng.Intn(5)) - 2
				} else {
					e.Clear = true
				}
				edits = append(edits, e)
			}
			ops = append(ops, op{kind: "apply", user: user, edits: edits, now: now})
		case r < 63:
			ops = append(ops, op{kind: "undo", user: user, now: now})
		case r < 78:
			ops = append(ops, op{kind: "redo", user: user, now: now})
		case r < 88:
			ops = append(ops, op{kind: "protect", user: user, cell: keys[rng.Intn(len(keys))], now: now})
		default:
			ops = append(ops, op{kind: "unprotect", user: user, cell: keys[rng.Intn(len(keys))], now: now})
		}
	}
	return ops
}

// compareState 比较两个实现的全部可观察状态。
func compareState(t *testing.T, s *sheet.Service, m *naive.Model, users, keys []string) {
	t.Helper()
	if s.Revision() != m.Revision() {
		t.Fatalf("修订号不一致: service=%d naive=%d", s.Revision(), m.Revision())
	}
	for _, k := range keys {
		if got, want := s.Cell(k), m.Cell(k); got != want {
			t.Fatalf("Cell(%q) 不一致: service=%+v naive=%+v", k, got, want)
		}
		gotOwner, gotOK := s.Protector(k)
		wantOwner, wantOK := m.Protector(k)
		if gotOwner != wantOwner || gotOK != wantOK {
			t.Fatalf("Protector(%q) 不一致: service=(%q,%v) naive=(%q,%v)",
				k, gotOwner, gotOK, wantOwner, wantOK)
		}
	}
	for _, u := range users {
		got, gotOK := s.History(u)
		want, wantOK := m.History(u)
		if gotOK != wantOK || !reflect.DeepEqual(got, want) {
			t.Fatalf("History(%q) 不一致: service=%+v naive=%+v", u, got, want)
		}
	}
}

// 与独立朴素模型对照 1500 组随机操作序列。
// 日志打印每步的输入、输出与判定依据（拒绝类别/冲突单元格/丢弃标记）。
func TestRandomSequencesAgainstNaive(t *testing.T) {
	const sequences = 1500
	users := []string{"u0", "u1", "u2"}
	keys := []string{"k0", "k1", "k2", "k3", "k4"}

	for seq := 0; seq < sequences; seq++ {
		rng := rand.New(rand.NewSource(int64(seq)*7919 + 13))
		depths := map[string]int{}
		for _, u := range users {
			depths[u] = 1 + rng.Intn(3)
		}
		s, err := sheet.New(depths)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		m := naive.New(depths)
		ops := genSequence(rng, users, keys, 5+rng.Intn(36))

		for i, o := range ops {
			got, want := runOp(s, m, o)
			// 判定依据：OK 即通过全部检查; 否则为拒绝类别
			// （含冲突单元格与被覆盖例外的丢弃标记）。
			basis := fmt.Sprintf("判定=%s cell=%q dropped=%v rev=%d",
				got.Code, got.Cell, got.Dropped, got.Revision)
			t.Logf("seq=%d op=%d 输入=%s 输出=%s", seq, i, o, basis)
			if got != want {
				t.Fatalf("seq=%d op=%d 结果不一致\n输入: %s\nservice: %+v\nnaive:   %+v",
					seq, i, o, got, want)
			}
			compareState(t, s, m, users, keys)
		}
	}
}

// 相同操作序列重放得到完全相同的结果。
func TestReplayDeterminism(t *testing.T) {
	users := []string{"u0", "u1", "u2"}
	keys := []string{"k0", "k1", "k2", "k3"}
	depths := map[string]int{"u0": 2, "u1": 3, "u2": 1}
	rng := rand.New(rand.NewSource(42))
	ops := genSequence(rng, users, keys, 300)

	var first []sheet.Result
	for run := 0; run < 2; run++ {
		s, err := sheet.New(depths)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		m := naive.New(depths)
		results := make([]sheet.Result, 0, len(ops))
		for _, o := range ops {
			got, _ := runOp(s, m, o)
			results = append(results, got)
		}
		if run == 0 {
			first = results
			continue
		}
		if !reflect.DeepEqual(first, results) {
			for i := range first {
				if first[i] != results[i] {
					t.Fatalf("重放不一致: op=%d %s 第一次=%+v 第二次=%+v",
						i, ops[i], first[i], results[i])
				}
			}
		}
	}
}
