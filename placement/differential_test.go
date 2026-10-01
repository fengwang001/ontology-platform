package placement

import (
	"bufio"
	"fmt"
	"math/rand"
	"os"
	"sync"
	"testing"
)

// TestRandomDifferential 用 2000 个独立随机种子各跑一条随机操作序列，
// 与朴素参考模型逐步比对返回值、可行节点集合与拒绝原因，
// 并把输入、输出与判定依据打印到日志文件。
func TestRandomDifferential(t *testing.T) {
	if testing.Short() {
		t.Skip("differential test skipped in -short mode")
	}
	logFile, err := os.CreateTemp("", "placement-diff-*.log")
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()
	w := bufio.NewWriter(logFile)
	defer w.Flush()
	t.Logf("differential log written to %s", logFile.Name())

	const sequences = 2000
	for seq := 0; seq < sequences; seq++ {
		r := rand.New(rand.NewSource(int64(seq) + 1))
		Q := 1 + r.Intn(4)
		f := NewFilter(Q)
		ns := newNaive(Q)
		fmt.Fprintf(w, "=== seq=%d seed=%d Q=%d ===\n", seq, seq+1, Q)

		var nodeNames []string
		for i := 0; i < 1+r.Intn(5); i++ {
			name := fmt.Sprintf("n%d", i)
			zone := fmt.Sprintf("z%d", r.Intn(3))
			nodeNames = append(nodeNames, name)
			pr := prodToNaive(f.AddNode(Node{Name: name, Zone: zone}))
			nr := ns.addNode(name, zone)
			if !diffSameResult(pr, nr) {
				t.Fatalf("seq %d addnode mismatch prod=%s naive=%s", seq, diffResultString(pr), diffResultString(nr))
			}
		}

		var liveIDs []string
		podSeq := 0
		for op := 0; op < 40+r.Intn(50); op++ {
			d := nextDiffOp(r, &podSeq, nodeNames, liveIDs)
			// 小概率探测不存在的 Pod / 节点。
			if r.Intn(8) == 0 {
				switch d.kind {
				case "Commit", "Cancel", "Remove", "Relabel":
					d.id = "ghost"
				case "Place", "Reserve":
					d.node = "ghost-node"
				}
			}

			pr, nr := runDiffOp(f, ns, &d)
			if d.kind == "AddNode" && pr.reason == "" {
				nodeNames = uniqueSorted(append(nodeNames, d.node))
			}
			if (d.kind == "Place" || d.kind == "Reserve") && pr.reason == "" {
				liveIDs = append(liveIDs, d.pod.ID)
			}
			if (d.kind == "Cancel" || d.kind == "Remove") && pr.reason == "" {
				liveIDs = removeString(liveIDs, d.id)
			}
			if !diffSameResult(pr, nr) {
				t.Fatalf("seq %d op %d %s mismatch: prod=%s naive=%s\npod=%s node=%s id=%s labels=%v",
					seq, op, d.kind, diffResultString(pr), diffResultString(nr),
					diffPodString(d.pod), d.node, d.id, d.labels)
			}
			fmt.Fprintf(w, "op=%-3d %-10s id=%-4s node=%-6s labels=%v pod=%s -> %s\n",
				op, d.kind, d.id, d.node, d.labels, diffPodString(d.pod), diffResultString(pr))

			// Feasible 差分：全新合法 / 非法注入 / 与已存在者同名。
			var probe Pod
			switch r.Intn(3) {
			case 0:
				probe = diffRandPod(r, "probe", false)
			case 1:
				probe = diffRandPod(r, "probe", true)
			default:
				if len(liveIDs) > 0 {
					probe = diffRandPod(r, liveIDs[r.Intn(len(liveIDs))], false)
				} else {
					probe = diffRandPod(r, "probe", false)
				}
			}
			pfeas, ferr := f.Feasible(probe)
			nres := ns.feasible(probe)
			perr := prodToNaive(ferr)
			if ferr != nil {
				if !diffSameResult(perr, nres) {
					t.Fatalf("seq %d Feasible error mismatch x=%s prod=%s naive=%s",
						seq, diffPodString(probe), diffResultString(perr), diffResultString(nres))
				}
			} else if nres.reason != "" || !sameStringSlice(pfeas, nres.feasible) {
				t.Fatalf("seq %d Feasible list mismatch x=%s prod=%v naive=(%s,%v)",
					seq, diffPodString(probe), pfeas, diffResultString(nres), nres.feasible)
			}
			fmt.Fprintf(w, "      Feasible probe=%s -> err=%s nodes=%v\n",
				diffPodString(probe), diffResultString(perr), pfeas)
		}

		// 最终不变量：任意一对已放置 Pod 之间不得互相违反 (2)(3)；预留数 <= Q。
		checkFinalInvariant(t, f, seq)
	}
}

func nextDiffOp(r *rand.Rand, podSeq *int, nodeNames, liveIDs []string) diffOp {
	switch r.Intn(9) {
	case 0:
		return diffOp{kind: "AddNode", node: fmt.Sprintf("x%d", r.Intn(7)),
			zone: fmt.Sprintf("z%d", r.Intn(3))}
	case 1:
		return diffOp{kind: "RemoveNode", node: nodeNames[r.Intn(len(nodeNames))]}
	case 2, 3:
		*podSeq++
		return diffOp{kind: "Place", pod: diffRandPod(r, fmt.Sprintf("p%d", *podSeq), true),
			node: nodeNames[r.Intn(len(nodeNames))]}
	case 4:
		*podSeq++
		return diffOp{kind: "Reserve", pod: diffRandPod(r, fmt.Sprintf("p%d", *podSeq), true),
			node: nodeNames[r.Intn(len(nodeNames))]}
	}
	if len(liveIDs) == 0 {
		// 无生命周期目标时生成一次 Place。
		*podSeq++
		return diffOp{kind: "Place", pod: diffRandPod(r, fmt.Sprintf("p%d", *podSeq), true),
			node: nodeNames[r.Intn(len(nodeNames))]}
	}
	d := diffOp{id: liveIDs[r.Intn(len(liveIDs))]}
	switch r.Intn(4) {
	case 0:
		d.kind = "Commit"
	case 1:
		d.kind = "Cancel"
	case 2:
		d.kind = "Remove"
	default:
		d.kind = "Relabel"
		d.labels = diffRandLabels(r, 0.75)
	}
	return d
}

// checkFinalInvariant 读取内部状态验证两两反/对称关系无冲突、预留额度合规。
func checkFinalInvariant(t *testing.T, f *Filter, seq int) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.mu.reserved) > f.mu.Q {
		t.Fatalf("seq %d reservation quota exceeded: %d > %d", seq, len(f.mu.reserved), f.mu.Q)
	}
	for _, a := range f.mu.pods {
		for _, term := range a.pod.AntiAffinity {
			for _, b := range f.mu.pods {
				if a == b {
					continue
				}
				if matches(b.pod.Labels, term.Selector) && f.sameDomain(a.node, b.node, term.Topology) {
					t.Fatalf("seq %d invariant violated: %s anti-affinity blocks %s", seq, a.pod.ID, b.pod.ID)
				}
			}
		}
	}
}

// TestConcurrentSameIDWinsOnce 同一 ID 并发 Place/Reserve 恰有一次成功。
func TestConcurrentSameIDWinsOnce(t *testing.T) {
	for iter := 0; iter < 50; iter++ {
		f := NewFilter(1000)
		if err := f.AddNode(Node{Name: "n1", Zone: "z1"}); err != nil {
			t.Fatal(err)
		}
		x := Pod{ID: "same", Labels: map[string]string{"k": "v"}}
		var wg sync.WaitGroup
		var wins int64
		var winMu sync.Mutex
		for i := 0; i < 16; i++ {
			wg.Add(1)
			go func(useReserve bool) {
				defer wg.Done()
				var err error
				if useReserve {
					err = f.Reserve(x, "n1")
				} else {
					err = f.Place(x, "n1")
				}
				if err == nil {
					winMu.Lock()
					wins++
					winMu.Unlock()
				} else if reasonOf(err) != ReasonPodExists && reasonOf(err) != ReasonReservationFull {
					t.Errorf("unexpected concurrent error: %v", err)
				}
			}(i%2 == 0)
		}
		wg.Wait()
		if wins != 1 {
			t.Fatalf("iter %d: want exactly 1 winner, got %d", iter, wins)
		}
	}
}
