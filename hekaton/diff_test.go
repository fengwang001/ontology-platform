package hekaton

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

// generateOps 生成随机序列；ops[0] 是携带 K 的伪 op（kind='i'）。
func generateOps(rng *rand.Rand, n int) []op {
	k := 1 + rng.Intn(4)
	ops := []op{{kind: 'i', t: k}}
	nextID := 0
	for i := 0; i < n; i++ {
		r := rng.Float64()
		switch {
		case r < 0.18 || nextID == 0:
			nextID++
			ops = append(ops, op{kind: 'b'})
		case r < 0.40:
			id := 1 + rng.Intn(nextID)
			if rng.Intn(10) == 0 {
				id = nextID + 1 + rng.Intn(5) // 不存在的事务号
			}
			ops = append(ops, op{kind: 'r', t: id, key: rng.Intn(k + 2)})
		case r < 0.60:
			id := 1 + rng.Intn(nextID)
			if rng.Intn(10) == 0 {
				id = nextID + 1 + rng.Intn(5)
			}
			ops = append(ops, op{
				kind: 'w', t: id,
				key: rng.Intn(k + 2), x: rng.Intn(100),
			})
		case r < 0.74:
			ops = append(ops, op{kind: 'p', t: 1 + rng.Intn(nextID)})
		case r < 0.88:
			ops = append(ops, op{kind: 'f', t: 1 + rng.Intn(nextID)})
		default:
			ops = append(ops, op{kind: 'a', t: 1 + rng.Intn(nextID)})
		}
	}
	return ops
}

func formatResult(r refResult) string {
	if r.rejected {
		return "<被拒绝>"
	}
	return fmt.Sprintf("{val=%d list=%v}", r.val, r.list)
}

func TestDifferentialAgainstNaive(t *testing.T) {
	const sequences = 2000
	const opCount = 60
	for seq := 0; seq < sequences; seq++ {
		seed := int64(seq*7 + 101)
		rng := rand.New(rand.NewSource(seed))
		ops := generateOps(rng, opCount)
		k := ops[0].t
		body := ops[1:]

		e, err := New(k)
		if err != nil {
			t.Fatal(err)
		}
		m := newNaive(k)

		logs := []string{
			fmt.Sprintf("===== 序列 %d seed=%d K=%d ops=%d =====", seq, seed, k, opCount),
		}
		for idx, o := range body {
			er := runEngine(e, o)
			nr := runNaive(m, o)
			logs = append(logs, fmt.Sprintf("#%02d %-26s 输入如上; 引擎输出=%s; 朴素输出=%s; 判定依据: %s",
				idx, o.String(), formatResult(er), formatResult(nr), m.reason))
			if !resultsEqual(er, nr) {
				for _, l := range logs {
					t.Log(l)
				}
				t.Fatalf("序列 %d 第 %d 步结果分歧: %s (引擎=%v 朴素=%v)",
					seq, idx, o, er, nr)
			}
			es, ms := engineSnap(e), naiveSnap(m)
			if !reflect.DeepEqual(es, ms) {
				for _, l := range logs {
					t.Log(l)
				}
				t.Fatalf("序列 %d 第 %d 步后状态分歧:\n引擎=%+v\n朴素=%+v",
					seq, idx, es, ms)
			}
			checkOpenVersionInvariant(t, e, fmt.Sprintf("seq%d/step%d", seq, idx))
		}

		// 终结后依赖集合清空（已提交/已中止事务均无残留边）。
		for id, tr := range e.txns {
			if tr.state == Committed || tr.state == Aborted {
				if len(tr.deps) != 0 || len(tr.dependents) != 0 {
					t.Fatalf("序列 %d: 已终结事务 t%d 依赖未清空", seq, id)
				}
			}
		}

		checkCommittedProperties(t, e, body, fmt.Sprintf("seq%d", seq))

		// 相同调用序列重放结果完全一致：两个全新引擎逐步对照。
		e1, _ := New(k)
		e2, _ := New(k)
		m2 := newNaive(k)
		for _, o := range body {
			r1 := runEngine(e1, o)
			r2 := runEngine(e2, o)
			n1 := runNaive(m2, o)
			if !resultsEqual(r1, r2) || !resultsEqual(r2, n1) {
				t.Fatalf("序列 %d 重放结果不确定: %s", seq, o)
			}
		}
		if !reflect.DeepEqual(engineSnap(e1), engineSnap(e2)) ||
			!reflect.DeepEqual(engineSnap(e2), naiveSnap(m2)) {
			t.Fatalf("序列 %d 重放状态不一致", seq)
		}

		// 每个序列都打印日志（输入/输出/判定依据），-v 时可见；
		// 非 -v 时仅保留少量样例以免输出过大。
		if testing.Verbose() || seq < 5 {
			for _, l := range logs {
				t.Log(l)
			}
		}
	}
}
