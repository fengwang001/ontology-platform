package mirror

import (
	"reflect"
	"testing"
)

func mustNewVolume(t *testing.T, members, blocks, limit int) *Volume {
	t.Helper()
	v, err := NewVolume(members, blocks, limit)
	if err != nil {
		t.Fatalf("NewVolume(%d,%d,%d) 失败: %v", members, blocks, limit, err)
	}
	return v
}

func mustWrite(t *testing.T, v *Volume, block int, value uint64, failed map[int]bool) {
	t.Helper()
	if err := v.Write(block, value, failed); err != nil {
		t.Fatalf("Write(%d) 意外失败: %v", block, err)
	}
}

func mustFault(t *testing.T, v *Volume, id int) {
	t.Helper()
	if err := v.ReportFault(id); err != nil {
		t.Fatalf("ReportFault(%d) 意外失败: %v", id, err)
	}
}

func mustRejoin(t *testing.T, v *Volume, id int, diskGen uint64) {
	t.Helper()
	if err := v.Rejoin(id, diskGen); err != nil {
		t.Fatalf("Rejoin(%d,%d) 意外失败: %v", id, diskGen, err)
	}
}

// drain 推进重同步直到成员在线，返回累计复制的块。
func drain(t *testing.T, v *Volume, id int, limit int) []int {
	t.Helper()
	var all []int
	for v.Snapshot().Members[id].State == MemberResyncing {
		copied, err := v.AdvanceResync(id, limit)
		if err != nil {
			t.Fatalf("AdvanceResync(%d) 意外失败: %v", id, err)
		}
		all = append(all, copied...)
	}
	return all
}

func expectErrKind(t *testing.T, err error, kind ErrKind) {
	t.Helper()
	if err == nil {
		t.Fatalf("期望错误 %s，实际为 nil", kind)
	}
	k, ok := KindOf(err)
	if !ok || k != kind {
		t.Fatalf("期望错误 %s，实际为 %v", kind, err)
	}
}

func TestNewVolumeInvalidConfig(t *testing.T) {
	for _, cfg := range [][3]int{{1, 8, 2}, {5, 8, 2}, {2, 0, 2}, {2, -1, 2}, {2, 8, -1}} {
		_, err := NewVolume(cfg[0], cfg[1], cfg[2])
		expectErrKind(t, err, ErrInvalidArg)
	}
}

func TestReadEmptyValueNotError(t *testing.T) {
	v := mustNewVolume(t, 2, 8, 4)
	got, err := v.Read(5)
	if err != nil || got != 0 {
		t.Fatalf("读未写过的块应得空值 0，实际 (%d, %v)", got, err)
	}
}

// 读取只由编号最小的在线成员服务：白盒篡改成员 1 的数据，
// 读到的仍应是成员 0 的值；成员 0 故障后才轮到成员 1。
func TestReadServedBySmallestOnline(t *testing.T) {
	v := mustNewVolume(t, 3, 8, 4)
	mustWrite(t, v, 2, 42, nil)
	v.members[1].data[2] = 999 // 白盒篡改，在线成员正常情况下数据一致
	got, err := v.Read(2)
	if err != nil || got != 42 {
		t.Fatalf("应由成员 0 服务读到 42，实际 (%d, %v)", got, err)
	}
	mustFault(t, v, 0)
	got, err = v.Read(2)
	if err != nil || got != 999 {
		t.Fatalf("成员 0 故障后应由成员 1 服务读到 999，实际 (%d, %v)", got, err)
	}
}

func TestReadNoOnlineMember(t *testing.T) {
	v := mustNewVolume(t, 2, 8, 4)
	mustFault(t, v, 0)
	mustFault(t, v, 1)
	_, err := v.Read(0)
	expectErrKind(t, err, ErrVolumeUnavailable)
}

// 脏区块数恰等于上限时记录保留；多一个块时记录被丢弃并永久标记全量。
func TestDirtyLimitExactAndOverflow(t *testing.T) {
	v := mustNewVolume(t, 2, 16, 2)
	mustFault(t, v, 1) // 世代 1->2，成员 1 故障世代为 1

	mustWrite(t, v, 3, 10, nil)
	mustWrite(t, v, 7, 20, nil)
	mustWrite(t, v, 3, 30, nil) // 重复块不增加计数
	snap := v.Snapshot()
	if snap.Members[1].DirtyDropped {
		t.Fatalf("恰等于上限时不应丢弃脏区记录")
	}
	if !reflect.DeepEqual(snap.Members[1].Pending, []int{3, 7}) {
		t.Fatalf("脏区应为 [3 7]，实际 %v", snap.Members[1].Pending)
	}

	mustWrite(t, v, 9, 40, nil) // 第 3 个不同块，超过上限 2
	snap = v.Snapshot()
	if !snap.Members[1].DirtyDropped {
		t.Fatalf("超过上限后脏区记录应被丢弃并标记全量")
	}
	if len(snap.Members[1].Pending) != 0 {
		t.Fatalf("记录丢弃后脏区应为空，实际 %v", snap.Members[1].Pending)
	}

	// 标记是永久的：即使标签等于故障世代，也必须全量重同步。
	mustRejoin(t, v, 1, 1)
	if got := len(v.Snapshot().Members[1].Pending); got != 16 {
		t.Fatalf("超限后应全量重同步 16 块，实际待同步 %d 块", got)
	}
}

// 恰等于上限时记录有效，重新加入只做部分重同步。
func TestDirtyLimitExactAllowsPartialResync(t *testing.T) {
	v := mustNewVolume(t, 2, 16, 2)
	mustFault(t, v, 1)
	mustWrite(t, v, 3, 10, nil)
	mustWrite(t, v, 7, 20, nil)
	mustRejoin(t, v, 1, 1) // 标签等于故障世代
	if got := v.Snapshot().Members[1].Pending; !reflect.DeepEqual(got, []int{3, 7}) {
		t.Fatalf("应只重同步脏区 [3 7]，实际 %v", got)
	}
}

// 一次写入中多个成员同时新故障，世代只加一；故障世代等于转换前卷的世代。
func TestMultiFaultSingleGenerationBump(t *testing.T) {
	v := mustNewVolume(t, 4, 8, 4)
	mustWrite(t, v, 0, 1, map[int]bool{1: true})
	snap := v.Snapshot()
	if snap.Generation != 2 {
		t.Fatalf("成员故障世代应加一到 2，实际 %d", snap.Generation)
	}
	if snap.Members[1].State != MemberFaulted || snap.Members[1].FaultGen != 1 {
		t.Fatalf("成员 1 应故障且故障世代为 1，实际 %+v", snap.Members[1])
	}
	// 一次写入中两个成员同时新故障，世代仍只加一。
	mustWrite(t, v, 1, 2, map[int]bool{0: true, 2: true})
	snap = v.Snapshot()
	if snap.Generation != 3 {
		t.Fatalf("两个成员同时故障世代应只加一到 3，实际 %d", snap.Generation)
	}
	for _, id := range []int{0, 2} {
		if snap.Members[id].State != MemberFaulted || snap.Members[id].FaultGen != 2 {
			t.Fatalf("成员 %d 应故障且故障世代为 2，实际 %+v", id, snap.Members[id])
		}
	}
}

// 所有在线成员写失败：写入被拒绝，报卷不可用，且不留下任何痕迹。
func TestWriteAllOnlineFailRejectedNoTrace(t *testing.T) {
	v := mustNewVolume(t, 3, 8, 4)
	mustWrite(t, v, 0, 7, nil)
	mustFault(t, v, 2) // 世代 1->2
	mustWrite(t, v, 1, 8, nil)
	before := v.Snapshot()

	err := v.Write(2, 9, map[int]bool{0: true, 1: true})
	expectErrKind(t, err, ErrVolumeUnavailable)

	after := v.Snapshot()
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("被拒绝的写入不得改变任何状态\n前: %+v\n后: %+v", before, after)
	}
}

// 重同步中成员写失败转为故障并重新记录故障世代，已有脏区保留；
// 重新加入时按新故障世代做部分重同步。
func TestResyncingMemberFaultsAgainAndRejoins(t *testing.T) {
	v := mustNewVolume(t, 2, 8, 8)
	mustFault(t, v, 1) // 世代 1->2，成员 1 故障世代 1
	mustWrite(t, v, 2, 10, nil)
	mustWrite(t, v, 5, 20, nil)
	mustRejoin(t, v, 1, 1) // 部分重同步 {2,5}
	if got := v.Snapshot().Members[1].Pending; !reflect.DeepEqual(got, []int{2, 5}) {
		t.Fatalf("待同步应为 [2 5]，实际 %v", got)
	}

	// 重同步中写失败：转为故障，故障世代为当前世代 2，脏区保留。
	mustWrite(t, v, 6, 30, map[int]bool{1: true})
	snap := v.Snapshot()
	if snap.Generation != 3 {
		t.Fatalf("世代应加到 3，实际 %d", snap.Generation)
	}
	m1 := snap.Members[1]
	if m1.State != MemberFaulted || m1.FaultGen != 2 {
		t.Fatalf("成员 1 应故障且故障世代为 2，实际 %+v", m1)
	}
	// 原有脏区 {2,5} 保留，并登记本次缺失的块 6。
	if !reflect.DeepEqual(m1.Pending, []int{2, 5, 6}) {
		t.Fatalf("脏区应保留并追加为 [2 5 6]，实际 %v", m1.Pending)
	}

	// 用新故障世代标签重新加入：仍只做部分重同步。
	mustRejoin(t, v, 1, 2)
	copied := drain(t, v, 1, 100)
	if !reflect.DeepEqual(copied, []int{2, 5, 6}) {
		t.Fatalf("应只复制 [2 5 6]，实际 %v", copied)
	}
	if got, _ := v.Read(6); got != 30 {
		t.Fatalf("重同步后读块 6 应为 30，实际 %d", got)
	}
}

// 盘面世代标签小于、等于、大于故障世代三种情形：
// 等于且脏区有效 → 部分；小于或大于（但不超过卷世代）→ 全量；超过卷世代 → 世代超前。
func TestRejoinLabelLessEqualGreater(t *testing.T) {
	setup := func(t *testing.T) *Volume {
		v := mustNewVolume(t, 4, 8, 8)
		mustFault(t, v, 1) // 世代 1->2，成员 1 故障世代 1
		mustFault(t, v, 2) // 世代 2->3，成员 2 故障世代 2
		mustFault(t, v, 3) // 世代 3->4，成员 3 故障世代 3
		mustWrite(t, v, 4, 44, nil)
		return v // 卷世代 4，仅成员 0 在线
	}

	// 等于故障世代且脏区有效：部分重同步。
	v := setup(t)
	mustRejoin(t, v, 1, 1)
	if got := v.Snapshot().Members[1].Pending; !reflect.DeepEqual(got, []int{4}) {
		t.Fatalf("标签等于故障世代应部分重同步 [4]，实际 %v", got)
	}

	// 小于故障世代：全量重同步。
	v = setup(t)
	mustRejoin(t, v, 2, 1) // 故障世代为 2
	if got := len(v.Snapshot().Members[2].Pending); got != 8 {
		t.Fatalf("标签小于故障世代应全量重同步 8 块，实际 %d", got)
	}

	// 大于故障世代但不超过卷世代：全量重同步。
	v = setup(t)
	mustRejoin(t, v, 2, 4) // 故障世代为 2，卷世代为 4
	if got := len(v.Snapshot().Members[2].Pending); got != 8 {
		t.Fatalf("标签大于故障世代应全量重同步 8 块，实际 %d", got)
	}

	// 大于卷世代：世代超前，且不改变任何状态。
	v = setup(t)
	before := v.Snapshot()
	err := v.Rejoin(3, 5)
	expectErrKind(t, err, ErrGenerationAhead)
	if after := v.Snapshot(); !reflect.DeepEqual(before, after) {
		t.Fatalf("世代超前不得改变状态\n前: %+v\n后: %+v", before, after)
	}
}

// 全量重同步标记在下一次完成重同步后清除：之后再次故障，
// 脏区记录重新从空开始累积，可按部分重同步加入。
func TestFullResyncFlagClearedAfterCompletion(t *testing.T) {
	v := mustNewVolume(t, 2, 4, 1)
	mustFault(t, v, 1)         // 成员 1 故障世代 1
	mustWrite(t, v, 0, 1, nil) // 脏区 {0}，恰等于上限
	mustWrite(t, v, 1, 2, nil) // 超限，记录丢弃并标记全量
	if !v.Snapshot().Members[1].DirtyDropped {
		t.Fatalf("超限后应标记全量重同步")
	}
	mustRejoin(t, v, 1, 1) // 标记在，即使标签相等也全量
	if got := len(v.Snapshot().Members[1].Pending); got != 4 {
		t.Fatalf("应全量重同步 4 块，实际 %d", got)
	}
	drain(t, v, 1, 100) // 完成重同步，标记清除
	if v.Snapshot().Members[1].State != MemberOnline {
		t.Fatalf("重同步完成后应在线")
	}

	// 再次故障后脏区重新累积，未超限则可部分重同步。
	mustFault(t, v, 1)
	mustWrite(t, v, 2, 3, nil)
	mustRejoin(t, v, 1, v.Snapshot().Members[1].FaultGen)
	if got := v.Snapshot().Members[1].Pending; !reflect.DeepEqual(got, []int{2}) {
		t.Fatalf("标记清除后应部分重同步 [2]，实际 %v", got)
	}
}

// 全部成员故障后：只有故障世代最大的成员能首先重新加入，
// 加入后立即在线无需同步；其余成员此后一律全量重同步。
func TestAllFaultedAuthoritativeSelection(t *testing.T) {
	v := mustNewVolume(t, 3, 4, 8)
	mustFault(t, v, 0) // 故障世代 1
	mustFault(t, v, 1) // 故障世代 2
	mustFault(t, v, 2) // 故障世代 3，最后一个在线，卷不可用
	if _, err := v.Read(0); err == nil {
		t.Fatalf("全部故障后读应报卷不可用")
	}

	// 非权威成员（故障世代较小）重新加入被拒绝。
	expectErrKind(t, v.Rejoin(0, 1), ErrNotAuthoritative)
	expectErrKind(t, v.Rejoin(1, 2), ErrNotAuthoritative)

	// 权威成员加入：立即在线、无需同步，数据视为权威。
	mustRejoin(t, v, 2, 3)
	snap := v.Snapshot()
	if snap.Members[2].State != MemberOnline || len(snap.Members[2].Pending) != 0 {
		t.Fatalf("权威成员应立即在线且无待同步块，实际 %+v", snap.Members[2])
	}

	// 其余成员即使标签等于各自故障世代，也一律全量重同步。
	mustRejoin(t, v, 1, 2)
	if got := len(v.Snapshot().Members[1].Pending); got != 4 {
		t.Fatalf("权威恢复后其余成员应全量重同步 4 块，实际 %d", got)
	}
}

// 全部故障且最大故障世代并列时，取编号最小者为权威。
func TestAllFaultedAuthoritativeTie(t *testing.T) {
	v := mustNewVolume(t, 4, 4, 8)
	mustWrite(t, v, 0, 1, map[int]bool{0: true, 1: true}) // 0、1 故障世代 1，世代->2
	mustFault(t, v, 2)                                    // 2 故障世代 2，世代->3
	mustRejoin(t, v, 0, 1)                                // 0 部分重同步 {0}，转为重同步中
	mustFault(t, v, 3)                                    // 3 是最后在线成员：3 与重同步中的 0 同记故障世代 3，世代->4
	snap := v.Snapshot()
	if got := snap.Generation; got != 4 {
		t.Fatalf("世代应为 4，实际 %d", got)
	}
	for _, id := range []int{0, 3} {
		if snap.Members[id].FaultGen != 3 {
			t.Fatalf("成员 %d 故障世代应为 3，实际 %d", id, snap.Members[id].FaultGen)
		}
	}
	// 0、3 并列最大故障世代，编号最小者 0 为权威。
	expectErrKind(t, v.Rejoin(3, 3), ErrNotAuthoritative)
	mustRejoin(t, v, 0, 3)
	if v.Snapshot().Members[0].State != MemberOnline {
		t.Fatalf("并列时编号最小者应作为权威立即在线")
	}
	// 成员 3 此后全量重同步。
	mustRejoin(t, v, 3, 3)
	if got := len(v.Snapshot().Members[3].Pending); got != 4 {
		t.Fatalf("成员 3 应全量重同步 4 块，实际 %d", got)
	}
}

// 重同步期间：写入同时落到重同步中成员并清除对应待同步标记；
// 读取只由在线成员服务，永远读不到重同步中成员尚未同步的陈旧值。
func TestWriteReadDuringResync(t *testing.T) {
	v := mustNewVolume(t, 2, 8, 8)
	mustWrite(t, v, 0, 100, nil)
	mustFault(t, v, 1) // 成员 1 故障世代 1
	mustWrite(t, v, 1, 111, nil)
	mustWrite(t, v, 2, 222, nil)
	mustRejoin(t, v, 1, 1) // 部分重同步 {1,2}

	// 重同步期间写入块 1：直接落到成员 1 并清除其待同步标记。
	mustWrite(t, v, 1, 333, nil)
	if got := v.Snapshot().Members[1].Pending; !reflect.DeepEqual(got, []int{2}) {
		t.Fatalf("块 1 的待同步标记应被清除，实际 %v", got)
	}
	if got := v.members[1].data[1]; got != 333 {
		t.Fatalf("写入应直接落到重同步中成员，实际 %d", got)
	}
	// 重同步期间读取由在线成员服务，得到最新值。
	if got, err := v.Read(1); err != nil || got != 333 {
		t.Fatalf("重同步期间读块 1 应为 333，实际 (%d, %v)", got, err)
	}

	// 推进重同步：块 1 已被写入覆盖，不再复制；只复制块 2。
	copied := drain(t, v, 1, 100)
	if !reflect.DeepEqual(copied, []int{2}) {
		t.Fatalf("应只复制 [2]，实际 %v", copied)
	}
	// 成员 1 在线后数据与成员 0 完全一致，读不到陈旧值。
	if got := v.members[1].data[1]; got != 333 {
		t.Fatalf("成员 1 块 1 应为新值 333，实际 %d", got)
	}
	if got, _ := v.Read(1); got != 333 {
		t.Fatalf("重同步完成后读块 1 应为 333，实际 %d", got)
	}
}

// 批量推进：按块号升序、按上限分批，复制完成最后一块的那一次转为在线。
func TestAdvanceResyncBatchOrderAndLimit(t *testing.T) {
	v := mustNewVolume(t, 2, 5, 0) // 上限 0：任何登记都超限，必全量
	mustFault(t, v, 1)
	mustWrite(t, v, 0, 7, nil)
	mustRejoin(t, v, 1, 1) // 脏区已丢弃，全量重同步

	copied, err := v.AdvanceResync(1, 2)
	if err != nil || !reflect.DeepEqual(copied, []int{0, 1}) {
		t.Fatalf("第一批应为 [0 1]，实际 %v, %v", copied, err)
	}
	if v.Snapshot().Members[1].State != MemberResyncing {
		t.Fatalf("未完成前应仍为重同步中")
	}
	copied, _ = v.AdvanceResync(1, 2)
	if !reflect.DeepEqual(copied, []int{2, 3}) {
		t.Fatalf("第二批应为 [2 3]，实际 %v", copied)
	}
	copied, _ = v.AdvanceResync(1, 2)
	if !reflect.DeepEqual(copied, []int{4}) {
		t.Fatalf("最后一批应为 [4]，实际 %v", copied)
	}
	if v.Snapshot().Members[1].State != MemberOnline {
		t.Fatalf("复制完最后一块后应转为在线")
	}
	// 已在线的成员不能再推进。
	_, err = v.AdvanceResync(1, 2)
	expectErrKind(t, err, ErrBadState)
}

// 错误只报次序最靠前的一类：参数非法 > 成员不存在 > 状态不符 >
// 世代超前 > 非权威成员 > 卷不可用。
func TestErrorPrecedence(t *testing.T) {
	v := mustNewVolume(t, 2, 8, 4)
	mustFault(t, v, 0)
	mustFault(t, v, 1) // 全部故障，卷不可用

	// 块号越界 + 无在线成员：报参数非法而非卷不可用。
	expectErrKind(t, v.Write(99, 1, nil), ErrInvalidArg)
	_, err := v.Read(-1)
	expectErrKind(t, err, ErrInvalidArg)
	// 写失败集合含不存在的成员：报成员不存在。
	expectErrKind(t, v.Write(0, 1, map[int]bool{7: true}), ErrNoSuchMember)
	// 不存在的成员 + 超大标签：报成员不存在而非世代超前。
	expectErrKind(t, v.Rejoin(9, 999), ErrNoSuchMember)
	// 非权威成员 + 超大标签：报世代超前而非非权威成员。
	expectErrKind(t, v.Rejoin(0, 999), ErrGenerationAhead)
	// 推进上限非正 + 成员不在重同步中：报参数非法。
	_, err = v.AdvanceResync(0, 0)
	expectErrKind(t, err, ErrInvalidArg)
}

// 状态不符：对故障成员再报故障、对已在线成员重新加入、对未故障成员推进。
func TestBadStateErrors(t *testing.T) {
	v := mustNewVolume(t, 2, 8, 4)
	expectErrKind(t, v.Rejoin(0, 1), ErrBadState) // 在线成员重新加入
	mustFault(t, v, 1)
	expectErrKind(t, v.ReportFault(1), ErrBadState) // 重复报故障
	_, err := v.AdvanceResync(0, 1)                 // 在线成员不在重同步中
	expectErrKind(t, err, ErrBadState)
	expectErrKind(t, v.ReportFault(9), ErrNoSuchMember)
}

// 报告最后一个在线成员故障时，重同步中成员一并转为故障，
// 各自记录同一故障世代，世代只加一。
func TestReportFaultLastOnlineCascades(t *testing.T) {
	v := mustNewVolume(t, 3, 8, 8)
	mustFault(t, v, 2) // 故障世代 1，世代->2
	mustWrite(t, v, 0, 5, nil)
	mustRejoin(t, v, 2, 1) // 部分重同步 {0}
	if v.Snapshot().Members[2].State != MemberResyncing {
		t.Fatalf("成员 2 应在重同步中")
	}
	mustFault(t, v, 0) // 世代->3
	mustFault(t, v, 1) // 最后一个在线：成员 2 一并故障，世代->4 只加一
	snap := v.Snapshot()
	if snap.Generation != 4 {
		t.Fatalf("世代应为 4，实际 %d", snap.Generation)
	}
	if snap.Members[1].FaultGen != 3 || snap.Members[2].FaultGen != 3 {
		t.Fatalf("成员 1、2 应同记故障世代 3，实际 %d、%d",
			snap.Members[1].FaultGen, snap.Members[2].FaultGen)
	}
	if snap.Members[2].State != MemberFaulted {
		t.Fatalf("重同步中成员应一并转为故障")
	}
	// 成员 2 的待同步集合保留为其脏区。
	if !reflect.DeepEqual(snap.Members[2].Pending, []int{0}) {
		t.Fatalf("成员 2 脏区应保留为 [0]，实际 %v", snap.Members[2].Pending)
	}
}
