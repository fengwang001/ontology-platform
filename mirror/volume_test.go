package mirror_test

import (
	"reflect"
	"testing"

	"ontology/mirror"
)

func mustNew(t *testing.T, members, blocks, limit int) *mirror.Volume {
	t.Helper()
	v, err := mirror.NewVolume(members, blocks, limit)
	if err != nil {
		t.Fatalf("NewVolume(%d,%d,%d) 失败: %v", members, blocks, limit, err)
	}
	return v
}

func mustWrite(t *testing.T, v *mirror.Volume, block int, value string, failed ...int) {
	t.Helper()
	if err := v.Write(block, value, failed); err != nil {
		t.Fatalf("Write(%d,%q,%v) 意外失败: %v", block, value, failed, err)
	}
}

func mustReportFault(t *testing.T, v *mirror.Volume, id int) {
	t.Helper()
	if err := v.ReportFault(id); err != nil {
		t.Fatalf("ReportFault(%d) 意外失败: %v", id, err)
	}
}

func mustRejoin(t *testing.T, v *mirror.Volume, id int, diskGen uint64) {
	t.Helper()
	if err := v.Rejoin(id, diskGen); err != nil {
		t.Fatalf("Rejoin(%d,%d) 意外失败: %v", id, diskGen, err)
	}
}

func mustAdvance(t *testing.T, v *mirror.Volume, id, maxBlocks int, want []int) {
	t.Helper()
	got, err := v.ResyncAdvance(id, maxBlocks)
	if err != nil {
		t.Fatalf("ResyncAdvance(%d,%d) 意外失败: %v", id, maxBlocks, err)
	}
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ResyncAdvance(%d,%d) 复制块=%v，期望 %v", id, maxBlocks, got, want)
	}
}

func wantErrKind(t *testing.T, err error, kind mirror.ErrKind) {
	t.Helper()
	if err == nil {
		t.Fatalf("期望错误 %s，实际为 nil", kind)
	}
	got, ok := mirror.KindOf(err)
	if !ok || got != kind {
		t.Fatalf("期望错误 %s，实际为 %v", kind, err)
	}
}

func memberSnap(t *testing.T, v *mirror.Volume, id int) mirror.MemberSnapshot {
	t.Helper()
	return v.Snapshot().Members[id]
}

// 脏区块数恰等于上限时记录有效（部分重同步），多一时记录被丢弃（全量重同步）。
func TestDirtyLimitExactAndOverflow(t *testing.T) {
	v := mustNew(t, 3, 8, 2)
	mustReportFault(t, v, 1)
	mustWrite(t, v, 3, "a")
	mustWrite(t, v, 5, "b")
	ms := memberSnap(t, v, 1)
	if ms.FullResync || !reflect.DeepEqual(ms.Pending, []int{3, 5}) {
		t.Fatalf("恰等于上限时脏区应有效，实际 full=%v pending=%v", ms.FullResync, ms.Pending)
	}
	mustRejoin(t, v, 1, 1)
	mustAdvance(t, v, 1, 10, []int{3, 5})
	if got := memberSnap(t, v, 1); got.State != mirror.Online {
		t.Fatalf("部分重同步完成后应在线，实际 %s", got.State)
	}

	mustReportFault(t, v, 1)
	mustWrite(t, v, 0, "c")
	mustWrite(t, v, 1, "d")
	mustWrite(t, v, 2, "e") // 第三个不同块，超过上限 2
	ms = memberSnap(t, v, 1)
	if !ms.FullResync || len(ms.Pending) != 0 {
		t.Fatalf("超过上限时脏区应被丢弃并标记全量，实际 full=%v pending=%v", ms.FullResync, ms.Pending)
	}
	mustRejoin(t, v, 1, 2) // 标签等于故障世代，但记录已丢弃，仍全量
	mustAdvance(t, v, 1, 100, []int{0, 1, 2, 3, 4, 5, 6, 7})
	if got := memberSnap(t, v, 1); got.State != mirror.Online || got.FullResync {
		t.Fatalf("全量重同步完成后应在线且标记清除，实际 %s full=%v", got.State, got.FullResync)
	}
	if got := memberSnap(t, v, 1).Data; !reflect.DeepEqual(got, []string{"c", "d", "e", "a", "", "b", "", ""}) {
		t.Fatalf("全量重同步后数据不符: %q", got)
	}
}

// 一次写入中多个成员同时新故障，世代只增加一。
func TestSimultaneousFaultsSingleGeneration(t *testing.T) {
	v := mustNew(t, 4, 4, 8)
	mustWrite(t, v, 0, "x", 2, 3)
	snap := v.Snapshot()
	if snap.Generation != 2 {
		t.Fatalf("两个成员同时新故障世代应只加一（1->2），实际 %d", snap.Generation)
	}
	for _, id := range []int{2, 3} {
		ms := snap.Members[id]
		if ms.State != mirror.Faulted || ms.FaultGen != 1 {
			t.Fatalf("成员 %d 应故障且故障世代为 1，实际 %s fg=%d", id, ms.State, ms.FaultGen)
		}
		if !reflect.DeepEqual(ms.Pending, []int{0}) {
			t.Fatalf("成员 %d 的脏区应登记本次写缺失的块 0，实际 %v", id, ms.Pending)
		}
	}
}

// 重同步中成员再次故障：脏区保留、故障世代重记，重新加入后按新标签部分重同步。
func TestResyncingMemberFaultAndRejoin(t *testing.T) {
	v := mustNew(t, 3, 6, 10)
	mustReportFault(t, v, 1) // fg=1, gen=2
	mustWrite(t, v, 0, "a")
	mustWrite(t, v, 2, "b")
	mustRejoin(t, v, 1, 1)     // 部分重同步，待同步 {0,2}
	mustWrite(t, v, 5, "c", 1) // 重同步中写失败，再次故障
	snap := v.Snapshot()
	if snap.Generation != 3 {
		t.Fatalf("再次故障世代应为 3，实际 %d", snap.Generation)
	}
	ms := snap.Members[1]
	if ms.State != mirror.Faulted || ms.FaultGen != 2 {
		t.Fatalf("成员 1 应故障且故障世代重记为 2，实际 %s fg=%d", ms.State, ms.FaultGen)
	}
	if !reflect.DeepEqual(ms.Pending, []int{0, 2, 5}) {
		t.Fatalf("已有脏区应保留并登记新块，实际 %v", ms.Pending)
	}
	mustRejoin(t, v, 1, 2)
	mustAdvance(t, v, 1, 10, []int{0, 2, 5})
	if got := memberSnap(t, v, 1); got.State != mirror.Online {
		t.Fatalf("重同步完成后应在线，实际 %s", got.State)
	}
	if got := memberSnap(t, v, 1).Data; !reflect.DeepEqual(got, []string{"a", "", "b", "", "", "c"}) {
		t.Fatalf("重同步后数据不符: %q", got)
	}
}

// 盘面标签小于、等于、大于故障世代三种情形，以及标签超过卷世代的拒绝。
func TestDiskLabelCases(t *testing.T) {
	v := mustNew(t, 2, 4, 4)
	mustReportFault(t, v, 1) // fg=1, gen=2
	mustWrite(t, v, 0, "a")
	mustRejoin(t, v, 1, 0) // 标签小于故障世代：全量
	mustAdvance(t, v, 1, 10, []int{0, 1, 2, 3})

	mustReportFault(t, v, 1) // fg=2, gen=3
	mustWrite(t, v, 1, "b")
	mustRejoin(t, v, 1, 2) // 标签等于故障世代且记录有效：部分
	mustAdvance(t, v, 1, 10, []int{1})

	mustReportFault(t, v, 1) // fg=3, gen=4
	mustWrite(t, v, 2, "c")
	mustRejoin(t, v, 1, 4) // 标签大于故障世代但不超过卷世代：全量
	mustAdvance(t, v, 1, 10, []int{0, 1, 2, 3})

	mustReportFault(t, v, 1) // fg=4, gen=5
	mustWrite(t, v, 2, "c2")
	wantErrKind(t, v.Rejoin(1, 6), mirror.ErrGenerationAhead) // 标签超过卷世代
	if got := memberSnap(t, v, 1); got.State != mirror.Faulted {
		t.Fatalf("世代超前不应改变状态，实际 %s", got.State)
	}
	mustRejoin(t, v, 1, 4) // 状态未被破坏，可正常部分重同步
	mustAdvance(t, v, 1, 10, []int{2})
}

// 全量重同步标记在下一次重同步完成后清除，之后故障可再做部分重同步。
func TestFullResyncFlagClearedAfterCompletion(t *testing.T) {
	v := mustNew(t, 2, 5, 1)
	mustReportFault(t, v, 1) // fg=1, gen=2
	mustWrite(t, v, 0, "a")
	mustWrite(t, v, 1, "b") // 超过上限 1，记录丢弃并标记全量
	if got := memberSnap(t, v, 1); !got.FullResync {
		t.Fatalf("超过上限应标记全量重同步")
	}
	mustRejoin(t, v, 1, 1)
	mustAdvance(t, v, 1, 10, []int{0, 1, 2, 3, 4})
	if got := memberSnap(t, v, 1); got.State != mirror.Online || got.FullResync {
		t.Fatalf("完成后应在线且全量标记清除，实际 %s full=%v", got.State, got.FullResync)
	}
	mustReportFault(t, v, 1) // fg=2, gen=3
	mustWrite(t, v, 3, "z")
	mustRejoin(t, v, 1, 2) // 标记已清除，标签相等且记录有效：部分
	mustAdvance(t, v, 1, 10, []int{3})
}

// 全部成员故障后，只有故障世代最大的成员能作为首个重新加入者并立即在线，
// 其余成员此后一律全量重同步。
func TestAllFaultedAuthoritativeSelection(t *testing.T) {
	v := mustNew(t, 3, 3, 3)
	mustWrite(t, v, 0, "a")
	mustReportFault(t, v, 0) // fg=1, gen=2
	mustWrite(t, v, 1, "b")
	mustReportFault(t, v, 1) // fg=2, gen=3
	mustWrite(t, v, 2, "c")
	mustReportFault(t, v, 2) // fg=3, gen=4，全部故障

	if _, _, err := v.Read(0); err == nil {
		t.Fatalf("全部故障时读应报卷不可用")
	} else {
		wantErrKind(t, err, mirror.ErrVolumeUnavailable)
	}
	wantErrKind(t, v.Write(0, "x", nil), mirror.ErrVolumeUnavailable)
	wantErrKind(t, v.Rejoin(0, 1), mirror.ErrNotAuthoritative)
	wantErrKind(t, v.Rejoin(1, 2), mirror.ErrNotAuthoritative)

	mustRejoin(t, v, 2, 3) // 权威成员，立即在线、无需同步
	if got := memberSnap(t, v, 2); got.State != mirror.Online || got.Pending != nil {
		t.Fatalf("权威成员应立即在线且无待同步块，实际 %s pending=%v", got.State, got.Pending)
	}
	got, from, err := v.Read(2)
	if err != nil || got != "c" || from != 2 {
		t.Fatalf("权威成员应立即服务读，实际 %q from=%d err=%v", got, from, err)
	}
	mustRejoin(t, v, 0, 1) // 标签等于故障世代，但仍一律全量
	mustAdvance(t, v, 0, 10, []int{0, 1, 2})
	mustRejoin(t, v, 1, 2)
	mustAdvance(t, v, 1, 10, []int{0, 1, 2})
}

// 全部故障且最大故障世代并列时，取编号最小者为权威成员。
func TestAllFaultedAuthoritativeTie(t *testing.T) {
	v := mustNew(t, 4, 2, 2)
	mustWrite(t, v, 0, "x", 2, 3) // m2、m3 故障，fg=1，gen=2
	mustRejoin(t, v, 2, 1)
	mustRejoin(t, v, 3, 1)   // m2、m3 重同步中
	mustReportFault(t, v, 0) // fg=2, gen=3
	mustReportFault(t, v, 1) // 最后一个在线成员：m1 fg=3，m2、m3 一并故障 fg=3，gen=4
	snap := v.Snapshot()
	if snap.Generation != 4 {
		t.Fatalf("世代应为 4，实际 %d", snap.Generation)
	}
	for _, id := range []int{1, 2, 3} {
		if fg := snap.Members[id].FaultGen; fg != 3 {
			t.Fatalf("成员 %d 故障世代应为 3，实际 %d", id, fg)
		}
	}
	wantErrKind(t, v.Rejoin(2, 3), mirror.ErrNotAuthoritative)
	wantErrKind(t, v.Rejoin(3, 3), mirror.ErrNotAuthoritative)
	mustRejoin(t, v, 1, 3) // 并列中取编号最小者
	if got := memberSnap(t, v, 1); got.State != mirror.Online {
		t.Fatalf("权威成员应立即在线，实际 %s", got.State)
	}
	mustRejoin(t, v, 2, 3) // 其余成员一律全量
	mustAdvance(t, v, 2, 10, []int{0, 1})
}

// 全部在线成员写失败时写入被拒绝，且不改变任何状态、世代与脏区。
func TestWriteAllOnlineFailRejectedNoTrace(t *testing.T) {
	v := mustNew(t, 3, 4, 2)
	mustWrite(t, v, 0, "a")
	mustWrite(t, v, 1, "b")
	mustReportFault(t, v, 2) // fg=1, gen=2
	mustWrite(t, v, 2, "c")
	before := v.Digest()
	wantErrKind(t, v.Write(3, "x", []int{0, 1}), mirror.ErrVolumeUnavailable)
	if after := v.Digest(); after != before {
		t.Fatalf("被拒绝的写入不得留痕\n之前: %s\n之后: %s", before, after)
	}
	got, from, err := v.Read(3)
	if err != nil || got != "" || from != 0 {
		t.Fatalf("未写过的块应得空值，实际 %q from=%d err=%v", got, from, err)
	}
}

// 重同步期间的写入直接落到重同步中成员并清除待同步标记；读取只由在线成员服务。
func TestWriteAndReadDuringResync(t *testing.T) {
	v := mustNew(t, 3, 6, 4)
	mustReportFault(t, v, 2) // fg=1, gen=2
	mustWrite(t, v, 1, "a")
	mustWrite(t, v, 4, "b")
	mustRejoin(t, v, 2, 1) // 部分重同步，待同步 {1,4}

	got, from, err := v.Read(1)
	if err != nil || got != "a" || from != 0 {
		t.Fatalf("读应由编号最小的在线成员 0 服务，实际 %q from=%d err=%v", got, from, err)
	}
	mustWrite(t, v, 1, "a2") // 直接写到重同步中成员并清除块 1 的标记
	if ms := memberSnap(t, v, 2); !reflect.DeepEqual(ms.Pending, []int{4}) {
		t.Fatalf("块 1 的待同步标记应被清除，实际 %v", ms.Pending)
	}
	mustAdvance(t, v, 2, 1, []int{4}) // 复制最后一块，成员转为在线
	if ms := memberSnap(t, v, 2); ms.State != mirror.Online {
		t.Fatalf("复制完最后一块应在线，实际 %s", ms.State)
	}
	if data := memberSnap(t, v, 2).Data; data[1] != "a2" || data[4] != "b" {
		t.Fatalf("重同步中成员数据不符: %q", data)
	}
	got, from, err = v.Read(1)
	if err != nil || got != "a2" || from != 0 {
		t.Fatalf("读到的应为最新值，实际 %q from=%d err=%v", got, from, err)
	}
}

// 各类错误可区分，且同时满足多类时只报次序最靠前的一类。
func TestErrorKindsAndPrecedence(t *testing.T) {
	if _, err := mirror.NewVolume(1, 4, 1); err != nil {
		wantErrKind(t, err, mirror.ErrInvalidArgument)
	}
	if _, err := mirror.NewVolume(5, 4, 1); err != nil {
		wantErrKind(t, err, mirror.ErrInvalidArgument)
	}
	if _, err := mirror.NewVolume(2, 0, 1); err != nil {
		wantErrKind(t, err, mirror.ErrInvalidArgument)
	}
	if _, err := mirror.NewVolume(2, 4, -1); err != nil {
		wantErrKind(t, err, mirror.ErrInvalidArgument)
	}

	v := mustNew(t, 3, 4, 2)
	// 块号非法与成员不存在同时成立时，报参数非法。
	wantErrKind(t, v.Write(9, "x", []int{7}), mirror.ErrInvalidArgument)
	wantErrKind(t, v.Write(0, "x", []int{7}), mirror.ErrNoSuchMember)
	if _, _, err := v.Read(-1); err != nil {
		wantErrKind(t, err, mirror.ErrInvalidArgument)
	}
	// 成员不存在与世代超前同时成立时，报成员不存在。
	wantErrKind(t, v.Rejoin(7, 100), mirror.ErrNoSuchMember)
	// 状态不符与世代超前同时成立时，报状态不符。
	wantErrKind(t, v.Rejoin(0, 100), mirror.ErrInvalidState)
	wantErrKind(t, v.ReportFault(7), mirror.ErrNoSuchMember)
	if _, err := v.ResyncAdvance(0, 0); err != nil {
		wantErrKind(t, err, mirror.ErrInvalidArgument)
	}
	if _, err := v.ResyncAdvance(0, 1); err != nil {
		wantErrKind(t, err, mirror.ErrInvalidState)
	}
	mustReportFault(t, v, 0)
	wantErrKind(t, v.ReportFault(0), mirror.ErrInvalidState)
	// 全部故障后：世代超前优先于非权威成员。
	mustReportFault(t, v, 1)
	mustReportFault(t, v, 2)
	wantErrKind(t, v.Rejoin(0, 100), mirror.ErrGenerationAhead)
	wantErrKind(t, v.Rejoin(0, 1), mirror.ErrNotAuthoritative)
}

// 可验证的复杂度证明：同样的操作序列作用于块数 8 与块数 2^18 的卷，
// 写入的块级工作量与脏区元素级工作量完全一致，即开销不随总块数增长。
func TestWriteCostIndependentOfBlockCount(t *testing.T) {
	run := func(numBlocks int) mirror.Stats {
		v := mustNew(t, 3, numBlocks, 1000)
		mustReportFault(t, v, 2)
		v.ResetStats()
		for b := 0; b < 200; b++ {
			mustWrite(t, v, b, "v")
		}
		return v.Stats()
	}
	small := run(256)
	big := run(1 << 18)
	if small != big {
		t.Fatalf("写开销不应随总块数增长：256 块卷 %+v，2^18 块卷 %+v", small, big)
	}
	if small.BlockDataOps != 400 { // 每次写只触及 2 个在线成员
		t.Fatalf("块级写次数应只随成员数增长，实际 %d", small.BlockDataOps)
	}
	if small.DirtyElemOps == 0 {
		t.Fatalf("脏区登记应产生元素级工作量")
	}
}

// 部分重同步选块的开销只随脏区内块数增长：脏区同为 100 块时，
// 块数 8 与块数 2^18 的卷推进同样多的块，工作量完全一致。
func TestPartialResyncCostIndependentOfBlockCount(t *testing.T) {
	run := func(numBlocks int) mirror.Stats {
		v := mustNew(t, 2, numBlocks, 1000)
		mustReportFault(t, v, 1)
		for b := 0; b < 100; b++ {
			mustWrite(t, v, b, "v")
		}
		mustRejoin(t, v, 1, 1) // 部分重同步，脏区 100 块
		v.ResetStats()
		var want [50]int
		for i := range want {
			want[i] = i
		}
		mustAdvance(t, v, 1, 50, want[:])
		return v.Stats()
	}
	small := run(128)
	big := run(1 << 18)
	if small != big {
		t.Fatalf("部分重同步选块开销不应随总块数增长：128 块卷 %+v，2^18 块卷 %+v", small, big)
	}
	if small.BlockDataOps != 100 { // 复制 50 块，每块一读一写
		t.Fatalf("复制工作量应只随复制块数增长，实际 %d", small.BlockDataOps)
	}
}
