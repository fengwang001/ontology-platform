package federation

import (
	"fmt"
	"math/big"
	"sync"
	"testing"
)

// 9. 错误类别优先级：参数非法 > 配置冲突 > 最小之和超总数 > 容量不足。
func TestErrorPriority(t *testing.T) {
	l := newTestLog(t, "priority")
	defer l.close()
	r := NewRegistry()

	// 同时构造多个问题的请求：total 为负（参数非法），且集群配置存在冲突，
	// 且 min 之和 > 一个正的参照总数，且容量也不足 —— 必须只报参数非法。
	if err := r.Register(mkCluster("a", -1, 5, 100, 4, true, 0)); err == nil ||
		codeOf(err) != ErrInvalidParam {
		t.Fatalf("负权重必须为参数非法，err=%v", err)
	}

	mustReg := func(c *Cluster) {
		t.Helper()
		if err := r.Register(c); err != nil {
			t.Fatalf("register %s: %v", c.Name, err)
		}
	}
	// b: min=5 cap=4 -> 配置冲突；其 min 还会让 min 之和巨大；容量也不够。
	mustReg(mkCluster("b", 1, 5, 100, 4, true, 0))

	_, err := r.Allocate(big.NewInt(-3))
	l.printf("[prio] total=-3 且 b 配置冲突 => 实际 err=%v", err)
	if err == nil || codeOf(err) != ErrInvalidParam {
		t.Fatalf("判定依据: 负 total 是参数非法且优先级最高，err=%v", err)
	}

	// total 合法（但小于 min 之和，且配置冲突仍在）=> 配置冲突优先。
	_, err = r.Allocate(big.NewInt(2))
	l.printf("[prio] total=2 < min(5) 且 b 配置冲突 => 实际 err=%v", err)
	if err == nil || codeOf(err) != ErrConfigConflict {
		t.Fatalf("判定依据: 配置冲突优先于 min 之和超总数，err=%v", err)
	}

	// 修复配置冲突：b 改为 min=5 cap=10；再登记一个大 min 集群，
	// 使 min 之和 > total，同时有效上限仍容纳不下 total —— 必须报 min 之和超限。
	if err := r.Update(mkCluster("b", 1, 5, 100, 10, true, 0)); err != nil {
		t.Fatalf("update: %v", err)
	}
	mustReg(mkCluster("c", 1, 5, 100, 10, true, 0))
	// total=7: min 之和 10 > 7；而总有效上限 20 足够 7。
	_, err = r.Allocate(big.NewInt(7))
	l.printf("[prio] total=7 < sumMin=10 且容量充足 => 实际 err=%v", err)
	if err == nil || codeOf(err) != ErrMinExceedsTotal {
		t.Fatalf("判定依据: min 之和超总数优先于容量不足，err=%v", err)
	}

	// total=15: min 之和 10 满足；总有效上限 20 也满足。
	// 再把两集群上限压到 min（容量可容纳 total 之外再无空间），total=30 => 容量不足。
	if err := r.Update(mkCluster("b", 1, 5, 5, 5, true, 0)); err != nil {
		t.Fatalf("update: %v", err)
	}
	if err := r.Update(mkCluster("c", 1, 5, 5, 5, true, 0)); err != nil {
		t.Fatalf("update: %v", err)
	}
	_, err = r.Allocate(big.NewInt(30))
	l.printf("[prio] total=30, min 满足但总上限 10 => 实际 err=%v", err)
	if err == nil || codeOf(err) != ErrInsufficientCapacity {
		t.Fatalf("判定依据: 仅剩容量问题时报容量不足，err=%v", err)
	}
	l.printf("判定: PASS，四类错误按规定优先级精确区分")

	// 被拒绝不改变状态：冲突集群仍按登记配置存在。
	if got := r.live["b"]; got == nil || got.Min.Int64() != 5 || got.Capacity.Int64() != 5 {
		t.Fatalf("拒绝后状态被修改")
	}
}

// 10. 登记非法输入与动态增删改立即生效。
func TestRegistryValidationAndMutation(t *testing.T) {
	l := newTestLog(t, "registry")
	defer l.close()
	r := NewRegistry()

	// 各类非法输入。
	badCases := []*Cluster{
		nil,
		{Name: "", Weight: bigI(1), Min: bigI(0), Max: nil, Capacity: bigI(1), Current: bigI(0)},
		mkCluster("dup", -1, 0, -1, 1, true, 0),
		mkCluster("dup2", 1, -2, -1, 1, true, 0),
		mkCluster("dup3", 1, 0, -1, -3, true, 0),
		mkCluster("dup4", 1, 0, -1, 1, true, -1),
	}
	for i, bc := range badCases {
		if err := r.Register(bc); err == nil || codeOf(err) != ErrInvalidParam {
			t.Fatalf("用例 %d: 必须拒绝为参数非法，err=%v", i, err)
		}
	}
	if err := r.Register(mkCluster("dup5", 1, 3, 2, 9, true, 0)); err == nil {
		t.Fatalf("min>max 必须在登记时拒绝")
	}

	if err := r.Register(mkCluster("a", 1, 0, -1, 10, true, 0)); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(mkCluster("a", 2, 0, -1, 10, true, 0)); err == nil ||
		codeOf(err) != ErrInvalidParam {
		t.Fatalf("重复登记必须拒绝，err=%v", err)
	}
	if err := r.Update(mkCluster("ghost", 1, 0, -1, 10, true, 0)); err == nil {
		t.Fatalf("更新不存在的集群必须拒绝")
	}

	// 修改立即生效：权重 1 -> 9，目标随之变化。
	if err := r.Update(mkCluster("a", 9, 0, -1, 100, true, 0)); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(mkCluster("b", 1, 0, -1, 100, true, 0)); err != nil {
		t.Fatal(err)
	}
	res := mustAlloc(t, r, 10)
	l.printf("[mut] 更新 a.weight=9 后 total=10 => a=%s b=%s", res.Targets["a"], res.Targets["b"])
	if res.Targets["a"].Int64() != 9 || res.Targets["b"].Int64() != 1 {
		t.Fatalf("判定依据: 权重更新后应按 9:1 分配，得到 %s:%s",
			res.Targets["a"], res.Targets["b"])
	}

	// 删除立即生效：b 删除后其当前承载（设为 3）必须迁出；重新登记清除墓碑。
	if err := r.Update(mkCluster("b", 1, 0, -1, 100, true, 3)); err != nil {
		t.Fatal(err)
	}
	if err := r.Remove("b"); err != nil {
		t.Fatal(err)
	}
	res2 := mustAlloc(t, r, 5)
	l.printf("[mut] 删除 b(current=3) 后 total=5 => targets=%v migration=%s",
		res2.Targets, res2.Migration)
	if res2.Targets["b"].Int64() != 0 || res2.Migration.Int64() != 3 {
		t.Fatalf("判定依据: 删除后 b 目标 0、迁出 3")
	}
	if err := r.Register(mkCluster("b", 1, 0, -1, 100, true, 0)); err != nil {
		t.Fatalf("重新登记已删除集群应被允许: %v", err)
	}
	res3 := mustAlloc(t, r, 5)
	if res3.Migration.Sign() != 0 {
		t.Fatalf("重新登记应清除墓碑，b 以 current=0 重新参与，migration 应为 0，得到 %s",
			res3.Migration)
	}
	if err := r.Remove("ghost"); err == nil {
		t.Fatalf("删除不存在集群必须拒绝")
	}
	l.printf("判定: PASS，增删改立即生效、重复/非法输入被拒、墓碑可被重新登记清除")

	// total=0 的边界：全部目标为 0，只有 min 之和为 0 时允许。
	res0, err0 := r.Allocate(big.NewInt(0))
	l.printf("[mut] total=0 => err=%v targets=%v", err0, func() string {
		if res0 == nil {
			return "nil"
		}
		return fmt.Sprint(res0.Targets)
	}())
	if err0 != nil {
		t.Fatalf("total=0 且 min 全为 0 时应成功")
	}
	for _, v := range res0.Targets {
		if v.Sign() != 0 {
			t.Fatalf("total=0 时所有目标必须为 0")
		}
	}
}

// 11. 并发：所有结果必须对应某个串行顺序（不变量自检），且快照一致。
func TestConcurrency(t *testing.T) {
	l := newTestLog(t, "concurrent")
	defer l.close()
	r := NewRegistry()
	for i := 0; i < 8; i++ {
		if err := r.Register(mkCluster(fmt.Sprintf("c%d", i),
			int64(1+i%4), int64(i%2), int64(50+i*3), 1000, i != 7, int64(i))); err != nil {
			t.Fatal(err)
		}
	}
	l.printf("[conc] 8 个集群，16 个 worker 并发混合 Allocate/Update/Register/Remove")

	var wg sync.WaitGroup
	var mu sync.Mutex
	var failures []string
	addFail := func(s string) {
		mu.Lock()
		failures = append(failures, s)
		mu.Unlock()
	}

	for w := 0; w < 16; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for k := 0; k < 300; k++ {
				total := int64((id*7 + k*13) % 400)
				res, err := r.Allocate(bigI(total))
				if err == nil {
					sum := big.NewInt(0)
					for _, v := range res.Targets {
						if v.Sign() < 0 {
							addFail(fmt.Sprintf("worker%d: 负目标", id))
						}
						sum.Add(sum, v)
					}
					if sum.Cmp(bigI(total)) != 0 {
						addFail(fmt.Sprintf("worker%d: 目标之和 %s != total %d", id, sum, total))
					}
					mig := big.NewInt(0)
					for _, ch := range res.Changes {
						if ch.Delta.Sign() < 0 {
							mig.Add(mig, new(big.Int).Neg(ch.Delta))
						}
						if ch.To.Cmp(ch.From) == 0 {
							addFail(fmt.Sprintf("worker%d: 无变化集群进入计划", id))
						}
					}
					if mig.Cmp(res.Migration) != 0 {
						addFail(fmt.Sprintf("worker%d: 迁移量不一致", id))
					}
					for i := 1; i < len(res.Changes); i++ {
						if res.Changes[i-1].Name >= res.Changes[i].Name {
							addFail(fmt.Sprintf("worker%d: 变更计划未排序", id))
						}
					}
				}
				// 穿插修改，制造快照竞争。
				if k%5 == 0 {
					name := fmt.Sprintf("c%d", id%8)
					_ = r.Update(mkCluster(name, int64(1+(k%5)), 0, int64(50+k%10),
						1000, true, int64(k%7)))
				}
				if k%37 == 0 && id == 0 {
					name := fmt.Sprintf("tmp%d", k)
					_ = r.Register(mkCluster(name, 2, 0, -1, 100, true, 0))
					_ = r.Remove(name)
				}
			}
		}(w)
	}
	wg.Wait()
	for _, f := range failures {
		t.Error(f)
	}
	l.printf("判定: PASS，%d 条失败记录，所有成功分配满足不变量（和==total、无负目标、迁移量一致、计划有序）",
		len(failures))
}
