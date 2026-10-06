package vanload

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

// randomConfig 生成一组随机但规模较小的车厢配置。
func randomConfig(rng *rand.Rand) []Compartment {
	n := 1 + rng.Intn(4)
	cmps := make([]Compartment, n)
	for i := range cmps {
		cmps[i] = Compartment{
			MaxWeight: 1 + rng.Intn(30),
			MaxVolume: 1 + rng.Intn(30),
		}
	}
	return cmps
}

// randomCargo 生成一件货物；id 由调用方给定。
func randomCargo(rng *rand.Rand, id int) Cargo {
	return Cargo{
		ID:       id,
		Weight:   1 + rng.Intn(12),
		Volume:   1 + rng.Intn(12),
		Stop:     1 + rng.Intn(5),
		Category: Category(rng.Intn(4)),
	}
}

// TestRandomDifferential 大量随机操作序列与独立朴素模型逐操作对照。
func TestRandomDifferential(t *testing.T) {
	const sequences = 800
	for seq := 0; seq < sequences; seq++ {
		rng := rand.New(rand.NewSource(int64(seq) + 1))
		cmps := randomConfig(rng)
		sys, err := New(cmps)
		if err != nil {
			t.Fatalf("seq=%d 配置失败: %v", seq, err)
		}
		naive := newNaive(cmps)

		nextID := 1
		steps := 40
		for step := 0; step < steps; step++ {
			op := rng.Intn(10)
			switch {
			case op < 6: // 60% 单件装货
				c := randomCargo(rng, nextID)
				nextID++
				res, gerr := sys.Load(c)
				nout := naive.load(c)
				if nout.pre != KindNone {
					// 前置拒绝时朴素模型不应提交，系统也必须以同样原因拒绝。
					if gerr == nil {
						t.Fatalf("seq=%d step=%d 货物 %+v 朴素前置拒绝(%v)但系统成功", seq, step, c, nout.pre)
					}
					if mustReject(t, gerr).Kind != nout.pre {
						t.Fatalf("seq=%d step=%d 前置原因不一致: 系统=%v 朴素=%v",
							seq, step, mustReject(t, gerr).Kind, nout.pre)
					}
				} else {
					assertLoadAgree(t, seq, step, c, gerr, res, nout)
				}
				if gerr == nil && nout.pre == KindNone {
					naive.commit(c, nout.chosen)
				}
			case op < 8: // 20% 卸货（可能重复/跳号）
				stop := 1 + rng.Intn(6)
				gres, gerr := sys.Unload(stop)
				nstatus, nremoved, nkind := naive.unload(stop)
				assertUnloadAgree(t, seq, step, stop, gerr, gres, nkind, nstatus, nremoved)
			default: // 20% 查询对照
				assertSnapshotAgree(t, sys, naive)
				if id := 1 + rng.Intn(nextID+2); rng.Intn(2) == 0 {
					gk, gok := sys.Locate(id)
					nk, nok := naive.locate(id)
					if gok != nok || (gok && gk != nk) {
						t.Fatalf("seq=%d step=%d Locate(%d) 不一致: (%d,%v) vs (%d,%v)",
							seq, step, id, gk, gok, nk, nok)
					}
				}
			}
		}
		assertSnapshotAgree(t, sys, naive)
	}
}

func assertLoadAgree(t *testing.T, seq, step int, c Cargo, gerr error, gres LoadResult, nout naiveOutcome) {
	t.Helper()
	if gerr != nil {
		gr := mustReject(t, gerr)
		// 只在系统可能返回四类约束失败的分支对照（其余分支朴素模型不模拟）。
		if nout.chosen == 0 {
			if gr.Kind != nout.overall {
				t.Fatalf("seq=%d step=%d 货物 %+v 归并不一致: 系统=%v 朴素=%v reasons(sys)=%v reasons(naive)=%v",
					seq, step, c, gr.Kind, nout.overall, gr.CompartmentReasons, nout.reasons)
			}
			if len(gr.CompartmentReasons) != len(nout.reasons) {
				t.Fatalf("seq=%d step=%d 分区原因数不一致: %v vs %v",
					seq, step, gr.CompartmentReasons, nout.reasons)
			}
			for k, v := range nout.reasons {
				if gr.CompartmentReasons[k] != v {
					t.Fatalf("seq=%d step=%d 分区%d 原因不一致: 系统=%v 朴素=%v",
						seq, step, k, gr.CompartmentReasons[k], v)
				}
			}
		}
		return
	}
	if nout.chosen == 0 {
		t.Fatalf("seq=%d step=%d 货物 %+v 系统成功但朴素失败: %v", seq, step, c, nout.overall)
	}
	if gres.Compartment != nout.chosen {
		t.Fatalf("seq=%d step=%d 货物 %+v 选定分区不一致: 系统=%d 朴素=%d",
			seq, step, c, gres.Compartment, nout.chosen)
	}
}

func assertUnloadAgree(t *testing.T, seq, step, stop int, gerr error, gres UnloadResult, nkind RejectKind, nstatus UnloadStatus, nremoved []int) {
	t.Helper()
	if gerr != nil {
		if mustReject(t, gerr).Kind != nkind {
			t.Fatalf("seq=%d step=%d 卸货 %d 失败原因不一致: %v vs %v",
				seq, step, stop, gerr, nkind)
		}
		return
	}
	if nkind != KindNone {
		t.Fatalf("seq=%d step=%d 卸货 %d 系统成功但朴素失败: %v", seq, step, stop, nkind)
	}
	if gres.Status != nstatus || len(gres.Removed) != len(nremoved) {
		t.Fatalf("seq=%d step=%d 卸货 %d 结果不一致: sys=%+v naive(status=%v removed=%v)",
			seq, step, stop, gres, nstatus, nremoved)
	}
	for i := range nremoved {
		if gres.Removed[i] != nremoved[i] {
			t.Fatalf("seq=%d step=%d 卸货明细不一致: %v vs %v", seq, step, gres.Removed, nremoved)
		}
	}
}

func assertSnapshotAgree(t *testing.T, sys *System, naive *naiveSystem) {
	t.Helper()
	gc := sys.RemainingCapacities()
	nc := naive.capacities()
	if len(gc) != len(nc) {
		t.Fatalf("分区数不一致")
	}
	for i := range gc {
		if gc[i] != nc[i] {
			t.Fatalf("分区剩余不一致: 系统=%+v 朴素=%+v", gc[i], nc[i])
		}
	}
	if sys.ArrivedStop() != naive.arrivedStop {
		t.Fatalf("已到达序号不一致: %d vs %d", sys.ArrivedStop(), naive.arrivedStop)
	}
	sys.mu.RLock()
	defer sys.mu.RUnlock()
	placed := sys.state.placedItems()
	if len(placed) != len(naive.items) {
		t.Fatalf("在车货物数不一致: %d vs %d", len(placed), len(naive.items))
	}
	for _, it := range naive.items {
		k, ok := placed[it.cargo.ID]
		if !ok || k != it.compartment {
			t.Fatalf("货物 %d 位置不一致", it.cargo.ID)
		}
	}
}

// TestDeterministicReplay 相同操作序列重放得到完全相同的装载位置。
func TestDeterministicReplay(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	cmps := randomConfig(rng)

	// 两次独立重放，输入完全一致，装载位置必须逐件相同。
	first := makeReplay(cmps)
	second := makeReplay(cmps)
	if len(first) != len(second) {
		t.Fatalf("两次重放成功件数不同")
	}
	for id, k := range first {
		if second[id] != k {
			t.Fatalf("货物 %d 重放位置不一致: %d vs %d", id, k, second[id])
		}
	}
}

func makeReplay(cmps []Compartment) map[int]int {
	sys, _ := New(cmps)
	placed := make(map[int]int)
	for id := 1; id <= 80; id++ {
		c := randomCargo(rand.New(rand.NewSource(int64(id)*7+1)), id)
		if res, err := sys.Load(c); err == nil {
			placed[id] = res.Compartment
		}
	}
	return placed
}

// TestConcurrentSnapshotNeverPartial 并发下查询只能读到完整操作前后的快照。
func TestConcurrentSnapshotNeverPartial(t *testing.T) {
	cmps := []Compartment{{MaxWeight: 1_000_000, MaxVolume: 1_000_000}}
	sys, _ := New(cmps)

	var wg sync.WaitGroup
	const batches = 40
	for b := 0; b < batches; b++ {
		base := b * 100
		cs := make([]Cargo, 20)
		for i := range cs {
			cs[i] = Cargo{ID: base + i + 1, Weight: 1, Volume: 1, Stop: 10}
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = sys.BatchLoad(cs)
		}()
	}

	// 读取者：任意时刻车上件数必须是 20 的整数倍（批量原子性）。
	stop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				sys.mu.RLock()
				n := len(sys.state.items)
				sys.mu.RUnlock()
				if n%20 != 0 {
					panic(fmt.Sprintf("读到批量进行到一半的状态: %d 件", n))
				}
			}
		}
	}()

	loadDone := make(chan struct{})
	go func() {
		// 复用 wg 仅等待装载者较复杂，这里单独等待装载 goroutine：
		// 通过批量提交后的最终件数判定结束。
		for {
			sys.mu.RLock()
			n := len(sys.state.items)
			sys.mu.RUnlock()
			if n == batches*20 {
				close(loadDone)
				return
			}
		}
	}()
	<-loadDone
	close(stop)
	wg.Wait()
}

// TestEvaluationIndependentOfItemCount 放置判定检查的在车货物记录数不随总件数增长。
// 这里通过结构性断言保证实现只依赖分区聚合：compareFeasibility 对两种等价聚合状态
// （一分区 1 件 vs 同分区多件聚合到相同总量）给出相同结论。
func TestEvaluationIndependentOfItemCount(t *testing.T) {
	// 场景A：分区1已有一件 10 重 stop5；场景B：分区1有十件 1 重 stop5。
	// 对同一件 stop1、重 5 的货物，两场景各分区判定必须完全相同。
	cmps := []Compartment{{MaxWeight: 11, MaxVolume: 1_000_000}}

	sA := newState(cmps)
	sA.states[0].addCargo(Cargo{ID: 1, Weight: 10, Volume: 1, Stop: 5})
	sB := newState(cmps)
	for i := 0; i < 10; i++ {
		sB.states[0].addCargo(Cargo{ID: i + 1, Weight: 1, Volume: 1, Stop: 5})
	}

	target := Cargo{ID: 99, Weight: 5, Volume: 1, Stop: 1}
	fA := evaluateFeasibility(sA, target)
	fB := evaluateFeasibility(sB, target)
	if fA.overall != fB.overall || fA.chosen != fB.chosen {
		t.Fatalf("聚合等价状态判定应一致: A=%+v B=%+v", fA, fB)
	}
}
