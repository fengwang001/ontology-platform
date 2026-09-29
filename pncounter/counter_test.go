package pncounter

import (
	"errors"
	"fmt"
	"math/big"
	"testing"
)

func bigInt(v int64) *big.Int { return big.NewInt(v) }

func dumpValues(t *testing.T, c *Cluster, why string) {
	t.Helper()
	values := c.Values()
	parts := make([]string, len(values))
	for i, v := range values {
		parts[i] = fmt.Sprintf("R%d=%s", i, v.String())
	}
	t.Logf("判定依据[%s]: 各副本值 [%s]", why, joinComma(parts))
}

func joinComma(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += ", "
		}
		out += p
	}
	return out
}

// TestBasicIncrementDecrement 验证本地增减只改本副本分量、值允许为负。
func TestBasicIncrementDecrement(t *testing.T) {
	c, err := NewCluster(3)
	if err != nil {
		t.Fatalf("NewCluster: %v", err)
	}
	r0, _ := c.Replica(0)
	r1, _ := c.Replica(1)

	t.Log("输入: R0 += 10")
	if err := r0.Increment(10); err != nil {
		t.Fatalf("R0.Increment: %v", err)
	}
	dumpValues(t, c, "只有 R0 的增分量[0]=10，其余为 0")
	if r0.Value().Cmp(bigInt(10)) != 0 {
		t.Fatalf("R0 应=10, 得 %s", r0.Value())
	}
	if r1.Value().Sign() != 0 {
		t.Fatalf("R1 未参与应=0, 得 %s", r1.Value())
	}

	t.Log("输入: R0 -= 25（允许结果为负，不截断）")
	if err := r0.Decrement(25); err != nil {
		t.Fatalf("R0.Decrement: %v", err)
	}
	dumpValues(t, c, "R0 值 = 增10 - 减25 = -15")
	if r0.Value().Cmp(bigInt(-15)) != 0 {
		t.Fatalf("R0 应=-15, 得 %s", r0.Value())
	}

	snap := r0.Snapshot()
	snap.Inc[0] = 999
	if r0.Snapshot().Inc[0] != 10 {
		t.Fatal("快照不是防御性拷贝：外部修改污染了副本状态")
	}
	if err := c.Check(); err != nil {
		t.Fatalf("集群自检失败: %v", err)
	}
	t.Log("自检通过；快照防御性拷贝验证通过")
}

// TestInvalidInputs 覆盖全部非法输入类别，并验证拒绝原因互不相同。
func TestInvalidInputs(t *testing.T) {
	sentinels := []error{
		ErrInvalidReplicaCount, ErrInvalidLimit, ErrNoSuchReplica,
		ErrNonPositiveDelta, ErrComponentOverflow, ErrInvariantViolation,
	}
	for i := 0; i < len(sentinels); i++ {
		for j := i + 1; j < len(sentinels); j++ {
			if errors.Is(sentinels[i], sentinels[j]) || sentinels[i].Error() == sentinels[j].Error() {
				t.Fatalf("错误原因必须互不相同可区分: %v vs %v", sentinels[i], sentinels[j])
			}
		}
	}
	t.Log("判定依据: 6 个哨兵错误两两不同（errors.Is 与文案双重检查）")

	if _, err := NewCluster(0); !errors.Is(err, ErrInvalidReplicaCount) {
		t.Fatalf("n=0 应拒绝 ErrInvalidReplicaCount, 得 %v", err)
	}
	if _, err := NewCluster(-2); !errors.Is(err, ErrInvalidReplicaCount) {
		t.Fatalf("n=-2 应拒绝 ErrInvalidReplicaCount, 得 %v", err)
	}
	t.Log("输入: NewCluster(0)/NewCluster(-2) -> ErrInvalidReplicaCount")

	if _, err := NewClusterWithLimit(2, 0); !errors.Is(err, ErrInvalidLimit) {
		t.Fatalf("limit=0 应拒绝 ErrInvalidLimit, 得 %v", err)
	}
	t.Log("输入: NewClusterWithLimit(2, 0) -> ErrInvalidLimit")

	c, err := NewClusterWithLimit(3, 10)
	if err != nil {
		t.Fatalf("NewClusterWithLimit: %v", err)
	}
	r0, _ := c.Replica(0)

	// 先执行一次合法操作到 8，此后所有调用都必须被拒绝且状态保持在 8。
	if err := r0.Increment(8); err != nil {
		t.Fatalf("Increment(8): %v", err)
	}
	before := r0.Snapshot()

	if _, err := c.Replica(3); !errors.Is(err, ErrNoSuchReplica) {
		t.Fatalf("编号 3 应 ErrNoSuchReplica, 得 %v", err)
	}
	if _, err := c.Replica(-1); !errors.Is(err, ErrNoSuchReplica) {
		t.Fatalf("编号 -1 应 ErrNoSuchReplica, 得 %v", err)
	}
	if err := r0.Merge(nil); !errors.Is(err, ErrNoSuchReplica) {
		t.Fatalf("Merge(nil) 应 ErrNoSuchReplica, 得 %v", err)
	}
	t.Log("输入: Replica(3)/Replica(-1)/Merge(nil) -> ErrNoSuchReplica")

	if err := r0.Increment(0); !errors.Is(err, ErrNonPositiveDelta) {
		t.Fatalf("delta=0 应 ErrNonPositiveDelta, 得 %v", err)
	}
	if err := r0.Decrement(0); !errors.Is(err, ErrNonPositiveDelta) {
		t.Fatalf("delta=0 应 ErrNonPositiveDelta, 得 %v", err)
	}
	t.Log("输入: Increment(0)/Decrement(0) -> ErrNonPositiveDelta（非正增量）")

	// 再加 5 会超过上限 10。
	if err := r0.Increment(5); !errors.Is(err, ErrComponentOverflow) {
		t.Fatalf("8+5>10 应 ErrComponentOverflow, 得 %v", err)
	}
	t.Log("输入: limit=10, R0 已增至 8 后再 +=5 -> ErrComponentOverflow")
	if err := r0.Decrement(11); !errors.Is(err, ErrComponentOverflow) {
		t.Fatalf("11>limit 应 ErrComponentOverflow, 得 %v", err)
	}
	t.Log("输入: R0 -= 11（11>limit=10）-> ErrComponentOverflow")

	after := r0.Snapshot()
	if after.ID != before.ID || !equalVec(after.Inc, before.Inc) || !equalVec(after.Dec, before.Dec) {
		t.Fatalf("拒绝后状态发生变化（失败留痕）:\nbefore=%+v\nafter =%+v", before, after)
	}
	dumpValues(t, c, "所有拒绝均不留痕：R0 增分量仍为 8、减分量仍为 0")
	if r0.Value().Cmp(bigInt(8)) != 0 {
		t.Fatalf("R0 拒绝后应仍为 8, 得 %s", r0.Value())
	}
	if err := c.Check(); err != nil {
		t.Fatalf("自检失败: %v", err)
	}
}

// TestMergeRules 验证合并逐分量取大、只改目标、自身合并非空操作语义且幂等。
func TestMergeRules(t *testing.T) {
	c, _ := NewCluster(3)
	r0, _ := c.Replica(0)
	r1, _ := c.Replica(1)

	_ = r0.Increment(5)
	_ = r1.Decrement(2)
	_ = r1.Increment(7)
	t.Log("输入: R0+=5; R1-=2; R1+=7")

	if err := r0.Merge(r0); err != nil {
		t.Fatalf("自身合并必须是合法空操作, 得 %v", err)
	}
	if r0.Value().Cmp(bigInt(5)) != 0 {
		t.Fatalf("自身合并不应改变值, 得 %s", r0.Value())
	}
	t.Log("输入: Merge(R0,R0) -> 合法空操作，R0 仍为 5")

	if err := r0.Merge(r1); err != nil {
		t.Fatalf("Merge: %v", err)
	}
	dumpValues(t, c, "R0 取两侧分量较大者后 = 增(5+7) - 减2 = 10；R1 不变 = 5")
	if r0.Value().Cmp(bigInt(10)) != 0 {
		t.Fatalf("合并后 R0 应=10, 得 %s", r0.Value())
	}
	if r1.Value().Cmp(bigInt(5)) != 0 {
		t.Fatalf("来源 R1 不应被修改, 应=5, 得 %s", r1.Value())
	}

	// 乱序/重复的旧状态合并不回退分量。
	_ = r1.Increment(3) // R1 增分量[1] = 10
	if err := r1.Merge(r0); err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if err := r0.Merge(r1); err != nil {
		t.Fatalf("重复 Merge: %v", err)
	}
	// 此刻双方状态一致；再次以任一方向重复合并必须是幂等空操作。
	s0 := r0.Snapshot()
	s1 := r1.Snapshot()
	if err := r0.Merge(r1); err != nil {
		t.Fatalf("幂等 Merge: %v", err)
	}
	if err := r1.Merge(r0); err != nil {
		t.Fatalf("逆方向幂等 Merge: %v", err)
	}
	got := r0.Snapshot()
	if !equalVec(got.Inc, s0.Inc) {
		t.Fatalf("重复合并改变了 R0: inc %v -> %v", s0.Inc, got.Inc)
	}
	if got1 := r1.Snapshot(); !equalVec(got1.Inc, s1.Inc) || !equalVec(got1.Dec, s1.Dec) {
		t.Fatalf("重复合并改变了 R1: %+v -> %+v", s1, got1)
	}
	if r0.Value().Cmp(bigInt(13)) != 0 {
		t.Fatalf("R0 应收敛到 13, 得 %s", r0.Value())
	}
	dumpValues(t, c, "重复/乱序合并后取 max，分量不回退，R0=R1=13")
	if err := c.Check(); err != nil {
		t.Fatalf("自检失败: %v", err)
	}
}

func equalVec(a, b []uint64) bool {
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
