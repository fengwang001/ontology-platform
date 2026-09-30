package lineage

import (
	"errors"
	"testing"
)

type testLogger struct{ t *testing.T }

func (l testLogger) Printf(format string, args ...any) {
	l.t.Helper()
	l.t.Logf(format, args...)
}

func mustOK(t *testing.T, err error, action string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s 意外失败: %v", action, err)
	}
}

func wantErr(t *testing.T, got error, want error, action string) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("%s 期望错误 %v, 实际 %v", action, want, got)
	}
}

func grantErrParents(t *testing.T, err error, want []ShardID) {
	t.Helper()
	var ge *GrantError
	if !errors.As(err, &ge) || !errors.Is(ge.Err, ErrParentsUndrained) {
		t.Fatalf("期望 ErrParentsUndrained 的 GrantError, 实际 %v", err)
	}
	if len(ge.UndrainedParents) != len(want) {
		t.Fatalf("未排空父分片期望 %v, 实际 %v", want, ge.UndrainedParents)
	}
	for i := range want {
		if ge.UndrainedParents[i] != want[i] {
			t.Fatalf("未排空父分片期望 %v, 实际 %v", want, ge.UndrainedParents)
		}
	}
}

// TestAppendSplitRoutingPositions 覆盖追加路由、位置计数、分裂后子分片位置从 0 起。
func TestAppendSplitRoutingPositions(t *testing.T) {
	c := New(10, 5, 2, testLogger{t})

	r, err := c.Append(3)
	mustOK(t, err, "Append(3)")
	if r != (AppendResult{Shard: 0, Position: 0}) {
		t.Fatalf("首条追加结果错误: %+v", r)
	}
	r, err = c.Append(9)
	mustOK(t, err, "Append(9)")
	if r != (AppendResult{Shard: 0, Position: 1}) {
		t.Fatalf("第二条追加结果错误: %+v", r)
	}

	left, right, err := c.Split(0, 5)
	mustOK(t, err, "Split(0,5)")
	if left != 1 || right != 2 {
		t.Fatalf("分裂子 ID 期望 1,2 实际 %d,%d", left, right)
	}

	info, _ := c.Get(0)
	if !info.Closed || info.EndPosition != 2 {
		t.Fatalf("父分片应关闭且结束位置为 2: %+v", info)
	}

	r, err = c.Append(4)
	mustOK(t, err, "Append(4)")
	if r != (AppendResult{Shard: 1, Position: 0}) {
		t.Fatalf("左子首条应位于 (1,0): %+v", r)
	}
	r, err = c.Append(5)
	mustOK(t, err, "Append(5)")
	if r != (AppendResult{Shard: 2, Position: 0}) {
		t.Fatalf("右子首条应位于 (2,0): %+v", r)
	}
	r, err = c.Append(9)
	mustOK(t, err, "Append(9)")
	if r != (AppendResult{Shard: 2, Position: 1}) {
		t.Fatalf("右子第二条应位于 (2,1): %+v", r)
	}

	// 键越界。
	_, err = c.Append(-1)
	wantErr(t, err, ErrKeyOutOfRange, "Append(-1)")
	_, err = c.Append(10)
	wantErr(t, err, ErrKeyOutOfRange, "Append(10)")

	// 已关闭分片不能再分裂。
	_, _, err = c.Split(0, 2)
	wantErr(t, err, ErrShardClosed, "Split 已关闭分片")
}

// TestSplitChildWaitsForParentDrain 分裂后子分片必须等父分片排空才可领取。
func TestSplitChildWaitsForParentDrain(t *testing.T) {
	c := New(10, 100, 2, testLogger{t})
	_, _ = c.Append(1)
	mustOK(t, c.Grant(0, "w1"), "Grant(0,w1)")
	left, right, _ := c.Split(0, 5)

	grantErrParents(t, c.Grant(left, "w2"), []ShardID{0})
	grantErrParents(t, c.Grant(right, "w2"), []ShardID{0})

	mustOK(t, c.Commit(0, "w1", 1), "Commit(0,w1,1)")
	if info, _ := c.Get(0); !info.Drained {
		t.Fatalf("父分片提交至结束位置后应已排空")
	}

	mustOK(t, c.Grant(left, "w2"), "父排空后 Grant(left)")
	mustOK(t, c.Grant(right, "w3"), "父排空后 Grant(right)")
	wantErr(t, c.Grant(0, "w9"), ErrShardDrained, "Grant(已排空父)")
}

// TestWorkerLeaseLimit 同一工作者有效租约数不得超过上限，过期后释放额度。
func TestWorkerLeaseLimit(t *testing.T) {
	c := New(100, 10, 1, testLogger{t})
	a, b, _ := c.Split(0, 50)
	mustOK(t, c.Grant(a, "w1"), "Grant(a,w1)")
	wantErr(t, c.Grant(b, "w1"), ErrWorkerLeaseLimit, "同一工作者第二张租约")
	mustOK(t, c.Grant(b, "w2"), "他人仍可领取")

	mustOK(t, c.AdvanceClock(10), "租约到期")
	mustOK(t, c.Grant(a, "w1"), "过期后额度释放，可重新领取")
}

// TestCommitValidation 提交进度：持有者校验、只增不减、不超过追加条数。
func TestCommitValidation(t *testing.T) {
	c := New(10, 100, 2, testLogger{t})
	_, _ = c.Append(0)
	_, _ = c.Append(0)
	mustOK(t, c.Grant(0, "w1"), "Grant")

	wantErr(t, c.Commit(0, "wX", 0), ErrNotValidHolder, "非持有者提交")
	wantErr(t, c.Commit(99, "w1", 0), ErrNotValidHolder, "不存在分片提交")
	mustOK(t, c.Commit(0, "w1", 1), "Commit=1")
	wantErr(t, c.Commit(0, "w1", 0), ErrProgressBackward, "进度回退")
	wantErr(t, c.Commit(0, "w1", 3), ErrProgressBeyondAppend, "超过追加条数")
	mustOK(t, c.Commit(0, "w1", 1), "相同进度幂等允许")
	mustOK(t, c.Commit(0, "w1", 2), "Commit=2")
}

// TestRejectionOrder 验证各类非法输入按规定次序只报第一个原因。
func TestRejectionOrder(t *testing.T) {
	c := New(10, 100, 1, testLogger{t})

	mustOK(t, c.AdvanceClock(5), "clock=5")
	wantErr(t, c.AdvanceClock(4), ErrClockBackward, "时钟回退")

	// Split：不存在 -> 已关闭 -> 越界
	_, _, err := c.Split(99, 3)
	wantErr(t, err, ErrShardNotFound, "Split 不存在")
	l, r, _ := c.Split(0, 5)
	_, _, err = c.Split(0, 5)
	wantErr(t, err, ErrShardClosed, "Split 已关闭")
	_, _, err = c.Split(l, 0)
	wantErr(t, err, ErrSplitPointOutOfRange, "Split mid==lo")
	_, _, err = c.Split(l, 5)
	wantErr(t, err, ErrSplitPointOutOfRange, "Split mid==hi")

	// Merge：不存在 -> 已关闭 -> 不相邻（含相同）
	_, err = c.Merge(l, 99)
	wantErr(t, err, ErrShardNotFound, "Merge 不存在")
	_, err = c.Merge(l, 0)
	wantErr(t, err, ErrShardClosed, "Merge 含已关闭")
	_, err = c.Merge(l, l)
	wantErr(t, err, ErrShardsNotAdjacent, "Merge 相同分片")
	// [0,5) 与 [7,10) 不相邻。
	_, r2, splitErr := c.Split(r, 7)
	mustOK(t, splitErr, "Split(r,7)")
	_, err = c.Merge(l, r2)
	wantErr(t, err, ErrShardsNotAdjacent, "Merge [0,5) 与 [7,10) 不相邻")

	// Grant：不存在 -> 已排空 -> 父未排空 -> 已有持有者 -> 工作者超限
	wantErr(t, c.Grant(99, "w"), ErrShardNotFound, "Grant 不存在")
	wantErr(t, c.Grant(0, "w"), ErrShardDrained, "Grant 已排空空父")
	mustOK(t, c.Grant(l, "w"), "空父已排空，Grant(l) 成功")
	wantErr(t, c.Grant(l, "w2"), ErrAlreadyHeld, "Grant 已被持有的分片")
	// “已有持有者”先于“工作者超限”：w 持 1 张（上限 1），再次领取自己持有的分片。
	wantErr(t, c.Grant(l, "w"), ErrAlreadyHeld, "已有持有者优先于超限")
	// 工作者超限：r2=[7,10) 的父 r 为空、关闭即排空，可领，但 w 已达上限。
	wantErr(t, c.Grant(r2, "w"), ErrWorkerLeaseLimit, "工作者超限")
	// 父未排空：给 l 追加并分裂，新子分片父未排空。
	// l 当前被 w 持有；先分裂空 l（关闭且无记录立即排空），再给 ll 追加一条后分裂。
	ll, lr, splitErr := c.Split(l, 2)
	mustOK(t, splitErr, "Split(空 l,2)")
	mustOK(t, c.Grant(ll, "w2"), "Grant(ll)")
	_, _ = c.Append(0) // 路由到 ll=[0,2)
	lll, llr, splitErr := c.Split(ll, 1)
	mustOK(t, splitErr, "Split(ll,1)")
	grantErrParents(t, c.Grant(lll, "w9"), []ShardID{ll})
	grantErrParents(t, c.Grant(llr, "w9"), []ShardID{ll})
	_ = lr
}

// TestMergeChildWaitsForBothParents 合并子分片需要两个父分片都排空。
func TestMergeChildWaitsForBothParents(t *testing.T) {
	c := New(10, 100, 4, testLogger{t})
	l, r, err := c.Split(0, 5)
	mustOK(t, err, "Split(0,5)") // 根分片无记录，分裂后立即排空

	_, _ = c.Append(1) // -> l
	_, _ = c.Append(8) // -> r
	mustOK(t, c.Grant(l, "wl"), "Grant(l)")
	mustOK(t, c.Grant(r, "wr"), "Grant(r)")

	child, err := c.Merge(r, l) // 参数次序无关
	mustOK(t, err, "Merge(r,l)")
	if child != 3 {
		t.Fatalf("合并子 ID 期望 3, 实际 %d", child)
	}
	info, _ := c.Get(child)
	if info.Lo != 0 || info.Hi != 10 {
		t.Fatalf("并集区间应为 [0,10): %+v", info)
	}
	if len(info.Parents) != 2 || info.Parents[0] != l || info.Parents[1] != r {
		t.Fatalf("父分片列表应升序为 [1 2]: %+v", info.Parents)
	}

	grantErrParents(t, c.Grant(child, "wc"), []ShardID{l, r})

	mustOK(t, c.Commit(l, "wl", 1), "Commit(l)")
	grantErrParents(t, c.Grant(child, "wc"), []ShardID{r})

	mustOK(t, c.Commit(r, "wr", 1), "Commit(r)")
	mustOK(t, c.Grant(child, "wc"), "两父均排空后 Grant(child)")

	r2, err := c.Append(9)
	mustOK(t, err, "合并后 Append")
	if r2 != (AppendResult{Shard: child, Position: 0}) {
		t.Fatalf("合并后追加应路由到并集子分片位置 0: %+v", r2)
	}
}

// TestEmptyParentDrainsImmediately 关闭时无记录的父分片立即视为已排空并放行。
func TestEmptyParentDrainsImmediately(t *testing.T) {
	c := New(10, 100, 4, testLogger{t})
	left, right, _ := c.Split(0, 4)
	if info, _ := c.Get(0); !info.Drained {
		t.Fatalf("无记录父分片关闭后应立即排空")
	}
	mustOK(t, c.Grant(right, "w4"), "无记录父排空后 Grant(right)")

	ll, lr, err := c.Split(left, 2)
	mustOK(t, err, "Split(空左子,2)")
	if info, _ := c.Get(left); !info.Drained {
		t.Fatalf("无记录分片关闭后应立即排空")
	}
	mustOK(t, c.Grant(ll, "w2"), "空链 Grant(ll)")
	mustOK(t, c.Grant(lr, "w3"), "空链 Grant(lr)")
	wantErr(t, c.Grant(0, "w9"), ErrShardDrained, "Grant(空根)")
}

// TestLeaseExpiresExactlyAtDeadline 租约恰在到期时刻失效，他人接管后旧持有者提交被拒。
func TestLeaseExpiresExactlyAtDeadline(t *testing.T) {
	c := New(10, 5, 2, testLogger{t})
	_, _ = c.Append(0)
	_, _ = c.Append(1)
	mustOK(t, c.Grant(0, "old"), "Grant(0,old) 时钟=0，到期=5")

	mustOK(t, c.AdvanceClock(4), "AdvanceClock(4)")
	wantErr(t, c.Grant(0, "new"), ErrAlreadyHeld, "未到期 Grant(0,new)")
	mustOK(t, c.Commit(0, "old", 1), "未到期 old 可提交")

	mustOK(t, c.AdvanceClock(5), "AdvanceClock(5) 恰为到期时刻")
	wantErr(t, c.Commit(0, "old", 2), ErrNotValidHolder, "到期后旧持有者提交")
	wantErr(t, c.Renew(0, "old"), ErrNotValidHolder, "到期后续租")
	mustOK(t, c.Grant(0, "new"), "到期后 new 接管")

	wantErr(t, c.Commit(0, "old", 2), ErrNotValidHolder, "接管后旧持有者提交")
	mustOK(t, c.Commit(0, "new", 2), "new 提交到结束位置")
	if info, _ := c.Get(0); info.Committed != 2 || info.Drained {
		t.Fatalf("开放分片提交全部记录后不应排空（仍可追加）: %+v", info)
	}
	l, r, err := c.Split(0, 5)
	mustOK(t, err, "全量提交后分裂")
	if info, _ := c.Get(0); !info.Drained || info.EndPosition != 2 {
		t.Fatalf("已全量提交的父分片关闭时应立即排空: %+v", info)
	}
	// 子分片父已排空，可立即领取。
	mustOK(t, c.Grant(l, "child"), "子分片立即可领")
	mustOK(t, c.Grant(r, "child2"), "子分片立即可领")

	c2 := New(10, 5, 2, testLogger{t})
	mustOK(t, c2.Grant(0, "a"), "c2 Grant 到期=5")
	mustOK(t, c2.AdvanceClock(4), "c2 clock=4")
	mustOK(t, c2.Renew(0, "a"), "c2 Renew 新到期=9")
	mustOK(t, c2.AdvanceClock(8), "c2 clock=8 < 9 仍有效")
	mustOK(t, c2.Commit(0, "a", 0), "c2 续租后仍可提交")
	mustOK(t, c2.AdvanceClock(9), "c2 clock=9 恰好到期")
	wantErr(t, c2.Commit(0, "a", 0), ErrNotValidHolder, "c2 到期时刻提交被拒")
}
