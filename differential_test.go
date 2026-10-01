package ontology

import (
	"flag"
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

var diffLog = flag.Bool("difflog", false, "打印全部随机对拍日志（输入、输出与判定依据）")

var allModes = []Mode{IS, IX, S, SIX, X}

func registeredIDs(n *naiveModel) []string {
	ids := make([]string, 0, len(n.parent))
	for id := range n.parent {
		ids = append(ids, id)
	}
	sortStrings(ids)
	return ids
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

// lockBasis 依据朴素规则给出本次 Lock 的判定依据，用于日志复核。
func lockBasis(n *naiveModel, txn int64, node string, mode Mode) string {
	switch {
	case txn <= 0:
		return "依据: 事务号非正"
	case !validMode[mode]:
		return "依据: 模式非法"
	case !n.exists(node):
		return "依据: 节点未登记"
	}
	holders := n.locks[node]
	held, has := holders[txn]
	if has && atLeast(held, mode) {
		return fmt.Sprintf("依据: 已持 %s >= %s，无操作", held, mode)
	}
	target := mode
	if has {
		target = join(held, mode)
	}
	need := requiredIntent(target)
	for _, anc := range n.ancestors(node) {
		ah, ok := n.locks[anc][txn]
		if !ok || !atLeast(ah, need) {
			return fmt.Sprintf("依据: join 后目标 %s 需祖先 %s，祖先 %q 上为 %q",
				target, need, anc, ah)
		}
	}
	for other, om := range holders {
		if other != txn && !canCoexist(target, om) {
			return fmt.Sprintf("依据: 目标 %s 与事务 %d 的 %s 不相容", target, other, om)
		}
	}
	return fmt.Sprintf("依据: 全部检查通过，置为 %s", target)
}

func formatMut(op string, r mutResult) string {
	if r.errKey != "" {
		if r.detail != "" {
			return "ERR:" + r.errKey + "(" + r.detail + ")"
		}
		return "ERR:" + r.errKey
	}
	if r.mode != "" {
		return "OK->" + string(r.mode)
	}
	return "OK"
}

// TestRandomDifferential 用 2000 组随机操作序列对拍生产实现与朴素模型，
// 并在每组结束后比对所有节点的完整持锁快照与不变量。
func TestRandomDifferential(t *testing.T) {
	const sequences = 2000
	const opsPerSeq = 120

	for seq := 0; seq < sequences; seq++ {
		rng := rand.New(rand.NewSource(int64(seq + 1)))
		prod := NewLockManager()
		naive := newNaiveModel()
		var logb strings.Builder
		fmt.Fprintf(&logb, "==== 序列 %d (seed=%d) ====\n", seq+1, seq+1)

		// 登记 1~6 个节点的随机森林：随机顺序、随机父节点。
		nodeCount := 1 + rng.Intn(6)
		pending := make([]string, nodeCount)
		for i := range pending {
			pending[i] = fmt.Sprintf("n%d", i)
		}
		rng.Shuffle(nodeCount, func(i, j int) { pending[i], pending[j] = pending[j], pending[i] })
		for _, id := range pending {
			var parent string
			done := registeredIDs(naive)
			if len(done) > 0 && rng.Intn(10) >= 3 {
				parent = done[rng.Intn(len(done))]
			}
			perr := prod.Register(id, parent)
			nr := naive.register(id, parent)
			pr := prodRegisterResult(perr)
			fmt.Fprintf(&logb, "Register(%q,%q) -> %s\n", id, parent, formatMut("", nr))
			if !sameMut(pr, nr) {
				t.Fatalf("序列 %d Register(%q,%q) 不一致: prod=%v naive=%v\n%s",
					seq+1, id, parent, pr, nr, logb.String())
			}
		}
		nodes := registeredIDs(naive)

		// 注入少量非法 Register，覆盖三类登记错误。
		for i := 0; i < 3; i++ {
			id := []string{"", nodes[0], "ghost"}[i]
			parent := []string{"x", "", "missing"}[i]
			perr := prod.Register(id, parent)
			nr := naive.register(id, parent)
			if !sameMut(prodRegisterResult(perr), nr) {
				t.Fatalf("序列 %d 非法 Register(%q,%q) 不一致", seq+1, id, parent)
			}
		}

		maxTxn := 1 + rng.Intn(5)
		for step := 0; step < opsPerSeq; step++ {
			txn := int64(1 + rng.Intn(maxTxn))
			node := nodes[rng.Intn(len(nodes))]
			if rng.Intn(20) == 0 {
				txn = 0 // 注入非法事务号
			}

			switch rng.Intn(100) {
			case 0, 1, 2: // ReleaseAll
				perr := prod.ReleaseAll(txn)
				nr := naive.releaseAll(txn)
				pr := mutResult{errKey: errKey(perr)}
				fmt.Fprintf(&logb, "ReleaseAll(%d) -> %s | 依据: 一次清空全部持锁\n",
					txn, formatMut("", pr))
				if !sameMut(pr, nr) {
					t.Fatalf("序列 %d 步 %d ReleaseAll 不一致\n%s", seq+1, step, logb.String())
				}
			case 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14: // Unlock
				perr := prod.Unlock(txn, node)
				nr := naive.unlock(txn, node)
				pr := prodUnlockResult(perr)
				fmt.Fprintf(&logb, "Unlock(%d,%q) -> %s | 依据: 事务号/节点/未持有/后代锁\n",
					txn, node, formatMut("", pr))
				if !sameMut(pr, nr) {
					t.Fatalf("序列 %d 步 %d Unlock 不一致: prod=%v naive=%v\n%s",
						seq+1, step, pr, nr, logb.String())
				}
			case 15, 16, 17: // Held 查询
				pm, phas, perr := prod.Held(txn, node)
				if perr == nil {
					nm, nhas := naive.locks[node][txn]
					if phas != nhas || (phas && pm != nm) {
						t.Fatalf("序列 %d Held(%d,%q) 不一致: prod=%q,%v naive=%q,%v",
							seq+1, txn, node, pm, phas, nm, nhas)
					}
					fmt.Fprintf(&logb, "Held(%d,%q) -> %q,v=%v\n", txn, node, pm, phas)
				} else {
					if nodeOK := naive.exists(node); nodeOK && txn > 0 {
						t.Fatalf("序列 %d Held 意外错误: %v", seq+1, perr)
					}
					fmt.Fprintf(&logb, "Held(%d,%q) -> ERR:%s\n", txn, node, errKey(perr))
				}
			case 18, 19, 20: // Holders 查询
				ph, perr := prod.Holders(node)
				nr := naive.holders(node)
				if (errKey(perr) != "") != (nr.errKey != "") {
					t.Fatalf("序列 %d Holders(%q) 错误不一致: %v vs %s",
						seq+1, node, perr, nr.errKey)
				}
				if perr == nil && !holdersEqual(ph, nr.holders) {
					t.Fatalf("序列 %d Holders(%q) 不一致: %v vs %v",
						seq+1, node, ph, nr.holders)
				}
				fmt.Fprintf(&logb, "Holders(%q) -> %v\n", node, ph)
			default: // Lock
				mode := allModes[rng.Intn(len(allModes))]
				if rng.Intn(25) == 0 {
					mode = Mode("BAD")
				}
				basis := lockBasis(naive, txn, node, mode)
				pm, perr := prod.Lock(txn, node, mode)
				nr := naive.lock(txn, node, mode)
				pr := prodLockResult(pm, perr)
				fmt.Fprintf(&logb, "Lock(%d,%q,%s) -> %s | %s\n",
					txn, node, mode, formatMut("", pr), basis)
				if !sameMut(pr, nr) {
					t.Fatalf("序列 %d 步 %d Lock 不一致:\nprod=%s\nnaive=%s\n%s",
						seq+1, step, formatMut("", pr), formatMut("", nr), logb.String())
				}
			}
		}

		// 全量快照比对：每个节点的持有者集合必须完全一致。
		for _, node := range nodes {
			ph, perr := prod.Holders(node)
			if perr != nil {
				t.Fatalf("快照阶段 Holders(%q): %v", node, perr)
			}
			nh := naive.holders(node).holders
			if !holdersEqual(ph, nh) {
				t.Fatalf("序列 %d 最终快照节点 %q 不一致:\nprod=%v\nnaive=%v\n%s",
					seq+1, node, ph, nh, logb.String())
			}
		}

		// 不变量检查：相容 + 祖先意向齐全。
		checkInvariants(t, prod, seq+1)

		if *diffLog {
			t.Logf("\n%s", logb.String())
		} else if testing.Verbose() {
			t.Logf("序列 %d 完成：%d 步，最终持锁节点 %d 个",
				seq+1, opsPerSeq, countNonEmpty(prod))
		}
	}
}

func holdersEqual(a, b []Holder) bool {
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

func countNonEmpty(m *LockManager) int {
	count := 0
	for _, holders := range m.locks {
		if len(holders) > 0 {
			count++
		}
	}
	return count
}

// checkInvariants 直接检查生产状态满足：
// 同节点任意两个不同事务的模式相容；每把锁的真祖先意向齐全。
func checkInvariants(t *testing.T, m *LockManager, seq int) {
	t.Helper()
	for node, holders := range m.locks {
		var txns []int64
		for txn := range holders {
			txns = append(txns, txn)
		}
		for i := 0; i < len(txns); i++ {
			for j := i + 1; j < len(txns); j++ {
				mi, mj := holders[txns[i]], holders[txns[j]]
				if !canCoexist(mi, mj) {
					t.Fatalf("序列 %d 节点 %q 上 %d:%s 与 %d:%s 不相容",
						seq, node, txns[i], mi, txns[j], mj)
				}
			}
		}
		for txn, mode := range holders {
			need := requiredIntent(mode)
			for p := m.parent[node]; p != ""; p = m.parent[p] {
				ph, ok := m.locks[p][txn]
				if !ok || !atLeast(ph, need) {
					t.Fatalf("序列 %d 事务 %d 在 %q 持 %s，但祖先 %q 上为 %q（需 %s）",
						seq, txn, node, mode, p, ph, need)
				}
			}
		}
	}
}
