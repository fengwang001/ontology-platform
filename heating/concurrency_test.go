package heating

import (
	"fmt"
	"reflect"
	"sync"
	"testing"
)

// buildGrid 构造并发测试用网：一个环加若干支路与用户。
func buildGrid(t *testing.T) *Network {
	t.Helper()
	n := NewNetwork()
	mustNode(t, n, "src", NodeSource)
	const rings = 4
	for i := 0; i < rings; i++ {
		mustNode(t, n, fmt.Sprintf("r%d", i), NodeBranch)
		mustNode(t, n, fmt.Sprintf("u%d", i), NodeUser)
	}
	for i := 0; i < rings; i++ {
		mustSegment(t, n, fmt.Sprintf("ring%d", i), fmt.Sprintf("r%d", i), fmt.Sprintf("r%d", (i+1)%rings))
		mustSegment(t, n, fmt.Sprintf("feed%d", i), fmt.Sprintf("r%d", i), fmt.Sprintf("u%d", i))
		mustValve(t, n, fmt.Sprintf("feed%d", i), EndA, fmt.Sprintf("vf%d", i))
	}
	mustSegment(t, n, "main", "src", "r0")
	mustSegment(t, n, "main2", "src", "r2")
	mustValve(t, n, "main", EndB, "vm0")
	mustValve(t, n, "main2", EndB, "vm2")
	for i := 0; i < rings; i++ {
		mustValve(t, n, fmt.Sprintf("ring%d", i), EndA, fmt.Sprintf("vr%d", i))
		mustValve(t, n, fmt.Sprintf("ring%d", i), EndB, fmt.Sprintf("wr%d", i))
	}
	return n
}

// TestConcurrentMixed 混合并发调用：结果等价于某个串行顺序，
// 且不变量（泄漏管段当且仅当有活动隔离记录）始终成立。
func TestConcurrentMixed(t *testing.T) {
	n := buildGrid(t)
	segs := []string{"ring0", "ring1", "ring2", "ring3", "feed0", "feed1"}
	valves := []string{"vr0", "vr1", "vr2", "vr3", "vf0", "vf1", "vf2", "vf3"}

	var wg sync.WaitGroup
	const workers = 8
	const rounds = 200
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				seg := segs[(w+i)%len(segs)]
				v := valves[(w+i)%len(valves)]
				switch (w + i) % 5 {
				case 0:
					if _, err := n.SimulateIsolation(seg); err != nil &&
						err != ErrNotIsolatable && err != ErrSegmentNotFound {
						t.Errorf("推演返回非法错误: %v", err)
					}
				case 1:
					_, _ = n.ExecuteIsolation(seg, nil)
				case 2:
					_ = n.CompleteRepair(seg)
				case 3:
					_ = n.CloseValve(v)
					_ = n.OpenValve(v)
				case 4:
					_ = n.ReportStuck(v, ValveStuckOpen)
					_ = n.ConfirmValveRepaired(v)
				}
			}
		}(w)
	}
	wg.Wait()

	// 不变量：泄漏管段集合 == 活动隔离集合。
	active := make(map[string]bool)
	for _, id := range n.ActiveIsolations() {
		active[id] = true
	}
	for id := range n.segments {
		lk, err := n.SegmentLeaking(id)
		must(t, err)
		if lk != active[id] {
			t.Errorf("管段 %s 泄漏=%v 与活动隔离记录不一致", id, lk)
		}
	}
}

// TestConcurrentExecuteSameSegment 并发执行同一管段的隔离：
// 至多一个成功；并发修复也至多一个成功，最终状态干净。
func TestConcurrentExecuteSameSegment(t *testing.T) {
	n := buildGrid(t)
	const workers = 16

	var wg sync.WaitGroup
	var mu sync.Mutex
	execOK := 0
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := n.ExecuteIsolation("ring1", nil); err == nil {
				mu.Lock()
				execOK++
				mu.Unlock()
			} else if err != ErrSegmentIsolated && err != ErrNotIsolatable {
				t.Errorf("意外错误: %v", err)
			}
		}()
	}
	wg.Wait()
	if execOK > 1 {
		t.Fatalf("同一管段至多一个隔离执行成功, got %d", execOK)
	}

	repairOK := 0
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := n.CompleteRepair("ring1"); err == nil {
				mu.Lock()
				repairOK++
				mu.Unlock()
			} else if err != ErrSegmentNotIsolated {
				t.Errorf("意外错误: %v", err)
			}
		}()
	}
	wg.Wait()
	if repairOK != execOK {
		t.Fatalf("修复成功数 %d 应等于隔离成功数 %d", repairOK, execOK)
	}
	if got := n.ActiveIsolations(); len(got) != 0 {
		t.Fatalf("最终不应有活动隔离, got %v", got)
	}
	if lk, _ := n.SegmentLeaking("ring1"); lk {
		t.Fatal("最终 ring1 不应处于泄漏中")
	}
}

// TestConcurrentSimulateConsistent 并发推演读到一致快照，结果完全相同。
func TestConcurrentSimulateConsistent(t *testing.T) {
	n := buildGrid(t)
	base, err := n.SimulateIsolation("ring1")
	must(t, err)

	var wg sync.WaitGroup
	for w := 0; w < 16; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				p, err := n.SimulateIsolation("ring1")
				if err != nil {
					t.Errorf("推演失败: %v", err)
					return
				}
				if !reflect.DeepEqual(p, base) {
					t.Errorf("并发推演结果不一致:\n%+v\n%+v", p, base)
					return
				}
			}
		}()
	}
	wg.Wait()
}
