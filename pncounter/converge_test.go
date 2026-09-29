package pncounter

import (
	"fmt"
	"math/big"
	"math/rand"
	"sort"
	"testing"
)

// naiveRef 是朴素参照：对每个副本在自己编号上的净增减做汇总。
// 对任意副本 j，最终增分量 = 历史上对 j 的增操作总和，减分量同理，
// 与合并顺序无关；收敛值 = Σ增 - Σ减。
type naiveRef struct {
	n        int
	totalInc []uint64
	totalDec []uint64
}

func newNaiveRef(n int) *naiveRef {
	return &naiveRef{n: n, totalInc: make([]uint64, n), totalDec: make([]uint64, n)}
}

func (m *naiveRef) add(id uint64, inc bool) {
	if inc {
		m.totalInc[id]++
	} else {
		m.totalDec[id]++
	}
}

func (m *naiveRef) value() *big.Int {
	v := new(big.Int)
	for _, x := range m.totalInc {
		v.Add(v, new(big.Int).SetUint64(x))
	}
	for _, x := range m.totalDec {
		v.Sub(v, new(big.Int).SetUint64(x))
	}
	return v
}

// 深拷贝一副本的向量，便于在"克隆集群"上重放不同合并拓扑。
func cloneReplicas(c *Cluster) []Snapshot {
	out := make([]Snapshot, c.Size())
	for i := range out {
		r, _ := c.Replica(i)
		out[i] = r.Snapshot()
	}
	return out
}

// applyMerge 在给定快照集合上模拟把 src 并入 dst（逐分量取大，只改 dst）。
func applyMerge(snaps []Snapshot, dst, src int) {
	for i := range snaps[dst].Inc {
		if snaps[src].Inc[i] > snaps[dst].Inc[i] {
			snaps[dst].Inc[i] = snaps[src].Inc[i]
		}
		if snaps[src].Dec[i] > snaps[dst].Dec[i] {
			snaps[dst].Dec[i] = snaps[src].Dec[i]
		}
	}
}

func snapValue(s Snapshot) *big.Int { return valueOf(s.Inc, s.Dec) }

// TestAlgebraicProperties 验证合并的交换律、结合律、幂等性（与合并方向无关）。
func TestAlgebraicProperties(t *testing.T) {
	c, _ := NewCluster(3)
	ops := []struct {
		rep   int
		delta uint64
		inc   bool
	}{
		{0, 4, true}, {1, 9, true}, {2, 3, false},
		{0, 2, false}, {1, 1, false}, {2, 7, true},
	}
	ref := newNaiveRef(3)
	for _, op := range ops {
		var err error
		if op.inc {
			err = mustReplica(c, op.rep).Increment(op.delta)
		} else {
			err = mustReplica(c, op.rep).Decrement(op.delta)
		}
		if err != nil {
			t.Fatalf("准备操作失败: %v", err)
		}
		for k := uint64(0); k < op.delta; k++ {
			ref.add(uint64(op.rep), op.inc)
		}
	}
	t.Log("输入序列: R0+=4, R1+=9, R2-=3, R0-=2, R1-=1, R2+=7")
	t.Logf("朴素参照: 总增=%v 总减=%v, 收敛值=%s", ref.totalInc, ref.totalDec, ref.value())

	base := cloneReplicas(c)

	// 交换律：先 0<-1 再 0<-2 与先 0<-2 再 0<-1 等价。
	a := cloneFrom(base)
	applyMerge(a, 0, 1)
	applyMerge(a, 0, 2)
	b := cloneFrom(base)
	applyMerge(b, 0, 2)
	applyMerge(b, 0, 1)
	if !snapshotsEqual(a[0], b[0]) {
		t.Fatalf("交换律不成立:\n%+v\n%+v", a[0], b[0])
	}
	t.Log("判定依据[交换律]: merge(merge(0,1),2) 与 merge(merge(0,2),1) 的 R0 向量完全相同")

	// 结合律：(0<-1)<-2 与 0<-(1<-2后) 全量传播后等价。
	lhs := cloneFrom(base)
	applyMerge(lhs, 0, 1)
	applyMerge(lhs, 0, 2)
	rhs := cloneFrom(base)
	applyMerge(rhs, 1, 2)
	applyMerge(rhs, 0, 1)
	applyMerge(rhs, 2, 0) // 全量传播，让所有副本都拿到全部信息
	applyMerge(rhs, 1, 2)
	for _, s := range rhs {
		if snapValue(s).Cmp(ref.value()) != 0 {
			t.Fatalf("结合律路径下副本 R%d 值=%s，参照=%s", s.ID, snapValue(s), ref.value())
		}
	}
	if snapValue(lhs[0]).Cmp(ref.value()) != 0 {
		t.Fatalf("结合律 LHS R0=%s，参照=%s", snapValue(lhs[0]), ref.value())
	}
	t.Log("判定依据[结合律]: 两种括号化全量合并后各副本值都等于朴素参照值")

	// 幂等：把同一份快照反复并入，结果不变。
	idm := cloneFrom(base)
	applyMerge(idm, 0, 1)
	once := snapshotCopy(idm[0])
	for i := 0; i < 5; i++ {
		applyMerge(idm, 0, 1)
	}
	if !snapshotsEqual(idm[0], once) {
		t.Fatalf("幂等不成立: %+v != %+v", idm[0], once)
	}
	t.Log("判定依据[幂等]: 同一来源重复并入 5 次，目标向量与并入 1 次完全相同")
}

// TestRandomMergeConvergence 随机生成操作与随机/重复/乱序合并，
// 最终全量同步后所有副本必须与朴素参照一致。
func TestRandomMergeConvergence(t *testing.T) {
	const n = 4
	rng := rand.New(rand.NewSource(20260929))
	for iter := 0; iter < 50; iter++ {
		c, _ := NewClusterWithLimit(n, 1<<20)
		ref := newNaiveRef(n)

		// 随机本地操作。
		for step := 0; step < 60; step++ {
			id := rng.Intn(n)
			delta := uint64(1 + rng.Intn(5))
			inc := rng.Intn(2) == 0
			r, _ := c.Replica(id)
			var err error
			if inc {
				err = r.Increment(delta)
			} else {
				err = r.Decrement(delta)
			}
			if err != nil {
				// 超过上限的拒绝是预期行为，朴素参照也不记录。
				continue
			}
			for k := uint64(0); k < delta; k++ {
				ref.add(uint64(id), inc)
			}
		}

		// 随机两两合并，包含重复合并、乱序合并。
		for step := 0; step < 200; step++ {
			dst := rng.Intn(n)
			src := rng.Intn(n)
			rd, _ := c.Replica(dst)
			rs, _ := c.Replica(src)
			if err := rd.Merge(rs); err != nil {
				t.Fatalf("iter=%d 随机合并 %d<-%d 失败: %v", iter, dst, src, err)
			}
		}

		// 确定性全量传播，保证每个副本最终都看到全部操作。
		for round := 0; round < n; round++ {
			for i := 0; i < n; i++ {
				for j := 0; j < n; j++ {
					ri, _ := c.Replica(i)
					rj, _ := c.Replica(j)
					if err := ri.Merge(rj); err != nil {
						t.Fatalf("全量合并失败: %v", err)
					}
				}
			}
		}

		values := c.Values()
		for i, v := range values {
			if v.Cmp(ref.value()) != 0 {
				t.Fatalf("iter=%d 副本 R%d=%s 与参照 %s 不一致", iter, i, v, ref.value())
			}
		}
		if err := c.Check(); err != nil {
			t.Fatalf("iter=%d 自检失败: %v", iter, err)
		}
		if iter == 0 || iter == 49 {
			parts := make([]string, n)
			for i, v := range values {
				parts[i] = fmt.Sprintf("R%d=%s", i, v)
			}
			t.Logf("判定依据[iter=%d]: 随机交错+重复乱序合并后，全部副本与参照一致 [%s]",
				iter, joinComma(parts))
		}
	}
}

// TestDeterministicReproducibility 同样的操作序列用不同合并顺序执行两次，
// 最终逐分量状态必须字节级一致（可复现）。
func TestDeterministicReproducibility(t *testing.T) {
	planA := []struct{ dst, src int }{{0, 1}, {2, 1}, {0, 2}, {1, 0}, {2, 0}, {1, 2}}
	planB := make([]struct{ dst, src int }, len(planA))
	copy(planB, planA)
	sort.SliceStable(planB, func(i, j int) bool { return planB[i].dst < planB[j].dst })

	run := func(plan []struct{ dst, src int }) []Snapshot {
		c, _ := NewCluster(3)
		_ = mustReplica(c, 0).Increment(5)
		_ = mustReplica(c, 1).Decrement(4)
		_ = mustReplica(c, 2).Increment(8)
		_ = mustReplica(c, 0).Decrement(1)
		for _, m := range plan {
			rd, _ := c.Replica(m.dst)
			rs, _ := c.Replica(m.src)
			if err := rd.Merge(rs); err != nil {
				t.Fatalf("合并失败: %v", err)
			}
		}
		return cloneReplicas(c)
	}

	a := run(planA)
	b := run(planB)
	for i := range a {
		if !snapshotsEqual(a[i], b[i]) {
			t.Fatalf("不同合并顺序不可复现: R%d\n%+v\n%+v", i, a[i], b[i])
		}
	}
	t.Log("判定依据[可复现]: 两种合并顺序执行后三个副本的 inc/dec 向量逐分量相同")
}

func mustReplica(c *Cluster, id int) *Replica {
	r, err := c.Replica(id)
	if err != nil {
		panic(err)
	}
	return r
}

func cloneFrom(base []Snapshot) []Snapshot {
	out := make([]Snapshot, len(base))
	for i := range base {
		out[i] = snapshotCopy(base[i])
	}
	return out
}

func snapshotCopy(s Snapshot) Snapshot {
	inc := append([]uint64(nil), s.Inc...)
	dec := append([]uint64(nil), s.Dec...)
	return Snapshot{ID: s.ID, Inc: inc, Dec: dec}
}

func snapshotsEqual(a, b Snapshot) bool {
	return a.ID == b.ID && equalVec(a.Inc, b.Inc) && equalVec(a.Dec, b.Dec)
}
