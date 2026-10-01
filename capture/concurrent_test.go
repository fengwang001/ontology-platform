package capture

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

// transcript 记录一次完整重放的每步输出（返回值或拒绝原因）与最终状态。
type transcript struct {
	outputs []string
	final   snapshot
}

func replayTranscript(ops []op) transcript {
	m := New()
	tr := transcript{outputs: make([]string, 0, len(ops))}
	for _, o := range ops {
		r := applyManager(m, o)
		if r.ok {
			tr.outputs = append(tr.outputs, fmt.Sprintf("ok:%d", r.val))
		} else {
			tr.outputs = append(tr.outputs, "err:"+r.reason.String())
		}
	}
	tr.final = snapshotManager(m)
	return tr
}

// TestDeterministicReplay 相同操作序列在全新管理器上重放两次，
// 必须得到完全相同的句柄编号、取值、拒绝原因与最终状态。
func TestDeterministicReplay(t *testing.T) {
	for seed := int64(100); seed < 120; seed++ {
		rng := rand.New(rand.NewSource(seed))
		ops := genOps(rng, 200)
		first := replayTranscript(ops)
		second := replayTranscript(ops)
		if eq, diff := statesEqual(first.final, second.final); !eq {
			t.Fatalf("seed=%d final state differs: %s", seed, diff)
		}
		for i := range first.outputs {
			if first.outputs[i] != second.outputs[i] {
				t.Fatalf("seed=%d step %d output differs: %s != %s",
					seed, i+1, first.outputs[i], second.outputs[i])
			}
		}
		t.Logf("seed=%d 重放一致：%d 步，句柄数 %d，最终 top=%d stack=%v",
			seed, len(ops), len(first.final.vars), first.final.top, first.final.stack)
	}
}

// TestConcurrentStress 多 goroutine 并发调用全部方法。
// 不比较具体交错结果（每种交错都等价于某个串行顺序，无法预知），
// 但要求：无 panic、无死锁、所有拒绝都带可区分原因，且全程满足
// “同一槽至多一个共享开放变量”“句柄编号单调不复用”等内部不变式。
// 使用 -race 运行时同时验证不存在数据竞争。
func TestConcurrentStress(t *testing.T) {
	const goroutines = 8
	const rounds = 300
	var wg sync.WaitGroup
	m := New()

	// 预置几个槽与句柄，制造开放/关闭/释放混合的争用面。
	var seedHandles []int
	for i := 0; i < 4; i++ {
		m.Push(i * 10)
		h, err := m.Capture(i)
		if err != nil {
			t.Fatalf("seed capture: %v", err)
		}
		seedHandles = append(seedHandles, h)
	}

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(1000 + g)))
			for r := 0; r < rounds; r++ {
				switch rng.Intn(9) {
				case 0:
					m.Push(rng.Intn(100))
				case 1:
					top := m.Top()
					if top > 0 {
						_, _ = m.Capture(rng.Intn(top))
					}
				case 2:
					top := m.Top()
					_, _ = m.Capture(top + rng.Intn(2)) // 偶发越界
				case 3:
					top := m.Top()
					if top > 0 {
						_, _ = m.SlotGet(rng.Intn(top))
					}
				case 4:
					top := m.Top()
					if top > 0 {
						_ = m.SlotSet(rng.Intn(top), rng.Intn(100))
					}
				case 5:
					h := seedHandles[rng.Intn(len(seedHandles))]
					_, _ = m.HandleGet(h)
				case 6:
					h := 1 + rng.Intn(50) // 含大量不存在句柄
					_ = m.HandleSet(h, rng.Intn(100))
				case 7:
					_ = m.CloseFrom(rng.Intn(m.Top() + 2)) // 偶发越界
				default:
					h := 1 + rng.Intn(50)
					_ = m.Release(h)
				}
			}
		}(g)
	}
	wg.Wait()

	// 并发结束后检查内部不变式。
	m.mu.Lock()
	defer m.mu.Unlock()
	for slot, v := range m.open {
		if v.closed {
			t.Fatalf("invariant: open table slot %d holds a closed variable", slot)
		}
		if v.holders <= 0 {
			t.Fatalf("invariant: open table slot %d holds a released variable", slot)
		}
		if v.slot != slot {
			t.Fatalf("invariant: open table slot %d maps to variable of slot %d", slot, v.slot)
		}
		if m.vars[v.id] != v {
			t.Fatalf("invariant: open variable %d missing in vars", v.id)
		}
	}
	seenID := map[int]bool{}
	for id, v := range m.vars {
		if id != v.id {
			t.Fatalf("invariant: vars key %d != variable id %d", id, v.id)
		}
		if seenID[id] {
			t.Fatalf("invariant: duplicate handle id %d", id)
		}
		seenID[id] = true
		if !v.closed && v.holders > 0 && m.open[v.slot] != v {
			t.Fatalf("invariant: live open variable %d not shared from its slot", id)
		}
	}
	t.Logf("并发压测结束：top=%d，开放槽 %d 个，累计句柄 %d 个",
		len(m.stack), len(m.open), len(m.vars))
}

// TestConcurrentSameSlotSharing 并发在同一槽上捕获：无论如何交错，
// 第一个捕获建立句柄后，其余捕获都必须拿到同一个句柄（持有数恰好累加）。
func TestConcurrentSameSlotSharing(t *testing.T) {
	const n = 16
	m := New()
	m.Push(1)

	handles := make([]int, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			handles[i], errs[i] = m.Capture(0)
		}(i)
	}
	close(start)
	wg.Wait()

	for _, err := range errs {
		if err != nil {
			t.Fatalf("capture rejected: %v", err)
		}
	}
	for i := 1; i < n; i++ {
		if handles[i] != handles[0] {
			t.Fatalf("expected shared handle %d, got %d at %d", handles[0], handles[i], i)
		}
	}
	m.mu.Lock()
	got := m.vars[handles[0]].holders
	m.mu.Unlock()
	if got != n {
		t.Fatalf("expected holders=%d, got %d", n, got)
	}
	t.Logf("同槽并发捕获：16 次全部共享句柄 h=%d，持有数=%d", handles[0], got)
}
