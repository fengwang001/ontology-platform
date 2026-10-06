package pvbinding

import (
	"fmt"
	"sync"
	"testing"
)

// TestConcurrency_NoDoubleBinding 并发压力：大量 goroutine 同时为立即声明与
// 延迟联合绑定竞争同一批卷，最终每个卷至多被一个声明绑定，且绑定互相一致。
func TestConcurrency_NoDoubleBinding(t *testing.T) {
	c := New()
	log := newLog(t)
	for i := 0; i < 30; i++ {
		assertNoErr(t, c, c.AddVolume(fmt.Sprintf("v%02d", i),
			volSpec(10, "x", am(ReadWriteOnce))), "addV")
	}

	var wg sync.WaitGroup
	// 30 个立即声明，由 30 个 goroutine 并发提交，恰好对应 30 个卷。
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = c.AddClaim(fmt.Sprintf("imm%02d", i),
				claimSpec(1, "x", am(ReadWriteOnce)))
		}(i)
	}
	// 同时并发删除并重建部分声明，制造释放与重评估交错。
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := fmt.Sprintf("imm%02d", i)
			_ = c.DeleteClaim(name)
			_ = c.AddClaim(name+"b", claimSpec(1, "x", am(ReadWriteOnce)))
		}(i)
	}
	// 并发延迟联合绑定（不同存储类 y，有独立卷池）。
	for i := 0; i < 20; i++ {
		assertNoErr(t, c, c.AddVolume(fmt.Sprintf("y%02d", i),
			volSpec(10, "y", am(ReadWriteOnce))), "addY")
		assertNoErr(t, c, c.AddClaim(fmt.Sprintf("d%02d", i), delayed(1, "y")), "addD")
	}
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			a, b := fmt.Sprintf("d%02d", 2*i), fmt.Sprintf("d%02d", 2*i+1)
			_, _ = c.JointBind(JointBindRequest{Node: "n", ClaimNames: []string{a, b}})
		}(i)
	}
	wg.Wait()

	if err := c.CheckInvariants(); err != nil {
		t.Fatalf("invariants violated after concurrency: %v\n%s", err,
			dumpSnapshot(c.Snapshot()))
	}
	// 直接检查：同一卷绝不出现在两个声明上。
	owners := map[string]string{}
	for name, cl := range c.Snapshot().Claims {
		if !cl.Bound {
			continue
		}
		if prev, dup := owners[cl.VolumeName]; dup {
			t.Fatalf("volume %s bound to both %s and %s", cl.VolumeName, prev, name)
		}
		owners[cl.VolumeName] = name
	}
	log.step("并发混合操作结束", len(owners),
		"自检通过；每个被绑定卷仅有唯一属主，等价于某串行顺序")
}

// TestConcurrency_JointBindSerializability 多个联合绑定竞争共享卷池：
// 成功的次数必须与被消耗的卷数一致。
func TestConcurrency_JointBindSerializability(t *testing.T) {
	c := New()
	for i := 0; i < 50; i++ {
		assertNoErr(t, c, c.AddVolume(fmt.Sprintf("p%02d", i),
			volSpec(5, "z", am(ReadWriteOnce))), "addP")
	}
	for g := 0; g < 25; g++ {
		for j := 0; j < 2; j++ {
			assertNoErr(t, c, c.AddClaim(fmt.Sprintf("q%02d%02d", g, j),
				delayed(1, "z")), "addQ")
		}
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	successes := 0
	for g := 0; g < 25; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			res, err := c.JointBind(JointBindRequest{
				Node:       "n",
				ClaimNames: []string{fmt.Sprintf("q%02d00", g), fmt.Sprintf("q%02d01", g)},
			})
			if err == nil {
				mu.Lock()
				successes++
				mu.Unlock()
				if res[0].VolumeName == res[1].VolumeName {
					t.Errorf("two claims got same volume %s", res[0].VolumeName)
				}
			}
		}(g)
	}
	wg.Wait()
	assertInvariants(t, c)
	boundVolumes := 0
	for _, v := range c.Snapshot().Volumes {
		if v.Phase == VolumeBound {
			boundVolumes++
		}
	}
	if boundVolumes != 2*successes {
		t.Fatalf("bound volumes=%d, want %d (2 per successful joint bind)",
			boundVolumes, 2*successes)
	}
}
