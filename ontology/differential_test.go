package ontology

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

// diffAction 是对拍脚本中的一步操作。
type diffAction struct {
	name string
	mode Mode
	ops  []Op
}

// TestDifferentialAgainstNaive 用 2000 组随机操作序列对拍正式实现与朴素模拟器。
// 每组日志打印：构造参数、逐步输入、双方输出、视图与判定依据。
func TestDifferentialAgainstNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(20261001))

	rowPool := []string{"", "a", "b", "c", "r1", "r2"}
	keyPool := []Key{
		NullKey(),
		NullKey(),
		StringKey(""),
		StringKey("k1"),
		StringKey("k2"),
		StringKey("k3"),
	}

	const groups = 2000
	for g := 0; g < groups; g++ {
		deferrable := rng.Intn(2) == 0
		initiallyDeferred := deferrable && rng.Intn(2) == 0

		var log strings.Builder
		fmt.Fprintf(&log, "group %d: NewTable(deferrable=%v, initiallyDeferred=%v)\n",
			g, deferrable, initiallyDeferred)

		tbl, realCtorErr := NewTable(deferrable, initiallyDeferred)
		nv := newNaive(deferrable, initiallyDeferred)
		realCtorReason, _, _ := errorInfo(realCtorErr)
		naiveCtorReason := ReasonOK
		if initiallyDeferred && !deferrable {
			naiveCtorReason = ReasonInitiallyDeferredRequiresDeferrable
		}
		if realCtorReason != naiveCtorReason {
			t.Fatalf("group %d constructor mismatch: real=%s naive=%s\n%s",
				g, reasonString(realCtorReason), reasonString(naiveCtorReason), log.String())
		}
		if realCtorErr != nil {
			fmt.Fprintf(&log, "  constructor rejected: %s -> 双方一致，本组结束\n", reasonString(realCtorReason))
			t.Log(log.String())
			continue
		}

		const steps = 14
		for s := 0; s < steps; s++ {
			act := randomDiffAction(rng, nv, rowPool, keyPool)
			fmt.Fprintf(&log, "  step %d: %s\n", s, formatAction(act))

			var rr Reason
			var ri int
			var rk string
			var nr Reason
			var ni int
			var nk string

			switch act.name {
			case "Begin":
				rr, _, _ = errorInfo(tbl.Begin())
				nr = nv.begin()
			case "Apply":
				rr, ri, rk = errorInfo(tbl.Apply(act.ops))
				nr, ni, nk = nv.apply(act.ops)
			case "SetMode":
				rr, _, rk = errorInfo(tbl.SetMode(act.mode))
				nr, nk = nv.setMode(act.mode)
			case "Commit":
				rr, _, rk = errorInfo(tbl.Commit())
				nr, nk = nv.commit()
			case "Rollback":
				rr, _, _ = errorInfo(tbl.Rollback())
				nr = nv.rollback()
			case "Keys":
				rr, nr = ReasonOK, ReasonOK
			}

			var rkView, nkView []RowKey
			if act.name == "Keys" || true {
				rkView = tbl.Keys()
				nkView = nv.keys()
			}

			basis := "输出原因一致"
			if rr == ReasonUniqueViolation {
				basis = "原因+OpIndex+违例键均一致"
			}
			ok := rr == nr
			if act.name == "Apply" {
				ok = ok && ri == ni
			}
			if rr == ReasonUniqueViolation {
				ok = ok && rk == nk
			}
			ok = ok && sameView(rkView, nkView)

			fmt.Fprintf(&log, "    real : reason=%s opIndex=%d violatedKey=%q\n",
				reasonString(rr), ri, rk)
			fmt.Fprintf(&log, "    naive: reason=%s opIndex=%d violatedKey=%q\n",
				reasonString(nr), ni, nk)
			fmt.Fprintf(&log, "    view(real)=%s\n", formatView(rkView))
			fmt.Fprintf(&log, "    view(naive)=%s\n", formatView(nkView))
			fmt.Fprintf(&log, "    判定依据: %s => %v\n", basis, ok)

			if !ok {
				t.Fatalf("group %d step %d diverged:\n%s", g, s, log.String())
			}
		}
		fmt.Fprintf(&log, "  结论: %d 步全部一致\n", steps)
		t.Log(log.String())
	}
}

func randomDiffAction(rng *rand.Rand, nv *naiveTable, rows []string, keys []Key) diffAction {
	// 无事务时偏向 Begin / Rollback / Commit 类
	if nv.txn == nil {
		switch rng.Intn(4) {
		case 0, 1:
			return diffAction{name: "Begin"}
		case 2:
			return diffAction{name: "Keys"}
		default:
			return diffAction{name: "Apply"}
		}
	}
	switch rng.Intn(7) {
	case 0:
		mode := []Mode{IMMEDIATE, DEFERRED, Mode(99)}[rng.Intn(3)]
		return diffAction{name: "SetMode", mode: mode}
	case 1:
		return diffAction{name: "Commit"}
	case 2:
		return diffAction{name: "Rollback"}
	case 3:
		return diffAction{name: "Keys"}
	default:
		n := 1 + rng.Intn(4)
		ops := make([]Op, 0, n)
		for i := 0; i < n; i++ {
			ops = append(ops, randomOp(rng, rows, keys))
		}
		return diffAction{name: "Apply", ops: ops}
	}
}

func randomOp(rng *rand.Rand, rows []string, keys []Key) Op {
	row := rows[rng.Intn(len(rows))]
	key := keys[rng.Intn(len(keys))]
	switch rng.Intn(3) {
	case 0:
		return Insert(row, key)
	case 1:
		return Update(row, key)
	default:
		return Delete(row)
	}
}

func sameView(a, b []RowKey) bool {
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

func formatAction(a diffAction) string {
	switch a.name {
	case "Apply":
		parts := make([]string, len(a.ops))
		for i, op := range a.ops {
			parts[i] = formatOp(op)
		}
		return "Apply([" + strings.Join(parts, ", ") + "])"
	case "SetMode":
		return "SetMode(" + formatMode(a.mode) + ")"
	default:
		return a.name + "()"
	}
}

func formatOp(op Op) string {
	switch o := op.(type) {
	case InsertOp:
		return fmt.Sprintf("Insert(%q,%s)", o.Row, formatKey(o.Key))
	case UpdateOp:
		return fmt.Sprintf("Update(%q,%s)", o.Row, formatKey(o.Key))
	case DeleteOp:
		return fmt.Sprintf("Delete(%q)", o.Row)
	}
	return "?"
}

func formatKey(k Key) string {
	if k.IsNull {
		return "NULL"
	}
	return fmt.Sprintf("%q", k.Value)
}

func formatMode(m Mode) string {
	switch m {
	case IMMEDIATE:
		return "IMMEDIATE"
	case DEFERRED:
		return "DEFERRED"
	default:
		return fmt.Sprintf("INVALID(%d)", int(m))
	}
}

func formatView(v []RowKey) string {
	parts := make([]string, len(v))
	for i, rk := range v {
		parts[i] = fmt.Sprintf("%s=%s", rk.Row, formatKey(rk.Key))
	}
	return "[" + strings.Join(parts, ", ") + "]"
}
