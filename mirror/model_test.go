package mirror

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

// 本文件是一个独立编写的朴素逐块模型：脏区用按块布尔数组表示、
// 计数用全表扫描、选块用全表升序扫描，实现风格与被测实现完全不同，
// 用于对随机操作序列做逐条对照，并打印每条操作的输入、输出与判定依据。

type naiveMember struct {
	state    MemberState
	data     []uint64
	faultGen uint64
	dirty    []bool // 非在线成员的缺失块标记（重同步中即待同步标记）
	dropped  bool   // 脏区记录已超限丢弃
}

type naiveVolume struct {
	gen     uint64
	limit   int
	members []naiveMember
}

func newNaiveVolume(members, blocks, limit int) *naiveVolume {
	nv := &naiveVolume{gen: 1, limit: limit}
	for i := 0; i < members; i++ {
		nv.members = append(nv.members, naiveMember{
			state: MemberOnline,
			data:  make([]uint64, blocks),
			dirty: make([]bool, blocks),
		})
	}
	return nv
}

func (nv *naiveVolume) dirtyCount(m *naiveMember) int {
	n := 0
	for _, d := range m.dirty {
		if d {
			n++
		}
	}
	return n
}

func (nv *naiveVolume) naiveAdd(m *naiveMember, block int) {
	if m.dropped || m.dirty[block] {
		return
	}
	if nv.dirtyCount(m)+1 > nv.limit {
		for i := range m.dirty {
			m.dirty[i] = false
		}
		m.dropped = true
		return
	}
	m.dirty[block] = true
}

func (nv *naiveVolume) pendingOf(m *naiveMember) []int {
	var out []int
	for b, d := range m.dirty {
		if d {
			out = append(out, b)
		}
	}
	return out
}

func (nv *naiveVolume) onlineIDs() []int {
	var out []int
	for i, m := range nv.members {
		if m.state == MemberOnline {
			out = append(out, i)
		}
	}
	return out
}

func (nv *naiveVolume) fault(m *naiveMember, faultGen uint64) {
	m.state = MemberFaulted
	m.faultGen = faultGen
}

func (nv *naiveVolume) write(block int, value uint64, failed map[int]bool) (error, string) {
	if block < 0 || block >= len(nv.members[0].data) {
		return newErr(ErrInvalidArg, "块号越界"), ""
	}
	for id := range failed {
		if id < 0 || id >= len(nv.members) {
			return newErr(ErrNoSuchMember, "成员不存在"), ""
		}
	}
	anyOK := false
	for id, m := range nv.members {
		if m.state == MemberOnline && !failed[id] {
			anyOK = true
		}
	}
	if !anyOK {
		return newErr(ErrVolumeUnavailable, "全部在线成员写失败"), ""
	}
	var faulted []int
	for id := range nv.members {
		m := &nv.members[id]
		if m.state != MemberOnline && m.state != MemberResyncing {
			continue
		}
		if failed[id] {
			nv.fault(m, nv.gen)
			faulted = append(faulted, id)
			continue
		}
		m.data[block] = value
		m.dirty[block] = false // 重同步中成员：清除待同步标记
	}
	if len(faulted) > 0 {
		nv.gen++
	}
	for id := range nv.members {
		if nv.members[id].state == MemberFaulted {
			nv.naiveAdd(&nv.members[id], block)
		}
	}
	if len(faulted) > 0 {
		return nil, fmt.Sprintf("至少一个在线成员写成功→接受；成员%v新故障→世代+1", faulted)
	}
	return nil, "全部目标成员写成功→接受"
}

func (nv *naiveVolume) read(block int) (uint64, error, string) {
	if block < 0 || block >= len(nv.members[0].data) {
		return 0, newErr(ErrInvalidArg, "块号越界"), ""
	}
	for id := range nv.members {
		if nv.members[id].state == MemberOnline {
			return nv.members[id].data[block], nil,
				fmt.Sprintf("编号最小在线成员 %d 服务", id)
		}
	}
	return 0, newErr(ErrVolumeUnavailable, "没有在线成员"), ""
}

func (nv *naiveVolume) reportFault(id int) (error, string) {
	if id < 0 || id >= len(nv.members) {
		return newErr(ErrNoSuchMember, "成员不存在"), ""
	}
	m := &nv.members[id]
	if m.state == MemberFaulted {
		return newErr(ErrBadState, "已是故障状态"), ""
	}
	wasLastOnline := m.state == MemberOnline && len(nv.onlineIDs()) == 1
	faultGen := nv.gen
	nv.gen++
	nv.fault(m, faultGen)
	if wasLastOnline {
		for i := range nv.members {
			if nv.members[i].state == MemberResyncing {
				nv.fault(&nv.members[i], faultGen)
			}
		}
		return nil, fmt.Sprintf("最后一个在线成员故障→重同步中成员一并故障，同记故障世代 %d，世代只+1", faultGen)
	}
	return nil, fmt.Sprintf("成员转故障，记录故障世代 %d，世代+1", faultGen)
}

func (nv *naiveVolume) rejoin(id int, diskGen uint64) (error, string) {
	if id < 0 || id >= len(nv.members) {
		return newErr(ErrNoSuchMember, "成员不存在"), ""
	}
	m := &nv.members[id]
	if m.state != MemberFaulted {
		return newErr(ErrBadState, "不是故障状态"), ""
	}
	if diskGen > nv.gen {
		return newErr(ErrGenerationAhead, "标签超过卷世代"), ""
	}
	allFaulted := true
	for i := range nv.members {
		if nv.members[i].state != MemberFaulted {
			allFaulted = false
		}
	}
	if allFaulted {
		auth := 0
		for i := range nv.members {
			if nv.members[i].faultGen > nv.members[auth].faultGen {
				auth = i
			}
		}
		if id != auth {
			return newErr(ErrNotAuthoritative, "首个重新加入者须为权威成员"), ""
		}
		m.state = MemberOnline
		for i := range m.dirty {
			m.dirty[i] = false
		}
		m.dropped = false
		for i := range nv.members {
			if nv.members[i].state == MemberFaulted {
				nv.members[i].dropped = true
			}
		}
		return nil, "全部故障下的权威成员（故障世代最大、并列取最小编号）→立即在线，其余成员标记全量"
	}
	if diskGen == m.faultGen && !m.dropped {
		m.state = MemberResyncing
		return nil, fmt.Sprintf("标签==故障世代 %d 且脏区有效→部分重同步 %v", m.faultGen, nv.pendingOf(m))
	}
	for i := range m.dirty {
		m.dirty[i] = true
	}
	m.state = MemberResyncing
	return nil, "标签不等或脏区已丢弃→全量重同步"
}

func (nv *naiveVolume) advance(id int, limit int) ([]int, error, string) {
	if limit <= 0 {
		return nil, newErr(ErrInvalidArg, "批量上限非正"), ""
	}
	if id < 0 || id >= len(nv.members) {
		return nil, newErr(ErrNoSuchMember, "成员不存在"), ""
	}
	m := &nv.members[id]
	if m.state != MemberResyncing {
		return nil, newErr(ErrBadState, "不在重同步中"), ""
	}
	src := -1
	for i := range nv.members {
		if nv.members[i].state == MemberOnline {
			src = i
			break
		}
	}
	var copied []int
	for b := range m.dirty {
		if len(copied) >= limit {
			break
		}
		if m.dirty[b] {
			m.data[b] = nv.members[src].data[b]
			m.dirty[b] = false
			copied = append(copied, b)
		}
	}
	if nv.dirtyCount(m) == 0 {
		m.state = MemberOnline
		m.dropped = false
		return copied, nil, fmt.Sprintf("复制%v后待同步清空→转在线", copied)
	}
	return copied, nil, fmt.Sprintf("复制%v，剩余待同步%v", copied, nv.pendingOf(m))
}

// 以下是对照框架：把实现与模型压成同一可比较形态，逐操作比对。

type cmpMember struct {
	state    MemberState
	faultGen uint64
	dropped  bool
	pending  []int
	data     []uint64
}

type cmpVolume struct {
	gen     uint64
	members []cmpMember
}

func snapshotOfImpl(v *Volume) cmpVolume {
	snap := v.Snapshot()
	out := cmpVolume{gen: snap.Generation}
	for _, ms := range snap.Members {
		pending := ms.Pending
		if len(pending) == 0 {
			pending = nil
		}
		out.members = append(out.members, cmpMember{
			state:    ms.State,
			faultGen: ms.FaultGen,
			dropped:  ms.DirtyDropped,
			pending:  pending,
			data:     ms.Data,
		})
	}
	return out
}

func snapshotOfNaive(nv *naiveVolume) cmpVolume {
	out := cmpVolume{gen: nv.gen}
	for i := range nv.members {
		m := &nv.members[i]
		cm := cmpMember{state: m.state, faultGen: m.faultGen, dropped: m.dropped}
		if m.state != MemberOnline {
			cm.pending = nv.pendingOf(m)
		}
		cm.data = append([]uint64(nil), m.data...)
		out.members = append(out.members, cm)
	}
	return out
}

func errKindOf(err error) string {
	if err == nil {
		return "OK"
	}
	k, _ := KindOf(err)
	return k.String()
}

func statesOf(nv *naiveVolume) string {
	s := "["
	for i := range nv.members {
		switch nv.members[i].state {
		case MemberOnline:
			s += "在"
		case MemberFaulted:
			s += "障"
		case MemberResyncing:
			s += "同"
		}
	}
	return s + "]"
}

func runRandomOps(t *testing.T, seed int64, steps int) {
	const members, blocks, limit = 3, 12, 4
	rng := rand.New(rand.NewSource(seed))
	impl := mustNewVolume(t, members, blocks, limit)
	naive := newNaiveVolume(members, blocks, limit)

	check := func(opIdx int, desc string) {
		t.Helper()
		gotImpl := snapshotOfImpl(impl)
		gotNaive := snapshotOfNaive(naive)
		if !reflect.DeepEqual(gotImpl, gotNaive) {
			t.Fatalf("操作 #%d %s 后状态不一致\n实现: %+v\n模型: %+v", opIdx, desc, gotImpl, gotNaive)
		}
		// 不变式：所有在线成员数据必须一致。
		var ref []uint64
		for _, cm := range gotImpl.members {
			if cm.state == MemberOnline {
				if ref == nil {
					ref = cm.data
				} else if !reflect.DeepEqual(ref, cm.data) {
					t.Fatalf("操作 #%d %s 后在线成员数据不一致", opIdx, desc)
				}
			}
		}
	}

	for i := 0; i < steps; i++ {
		pre := fmt.Sprintf("世代=%d 状态=%s", naive.gen, statesOf(naive))
		switch rng.Intn(100) {
		case 0, 1, 2, 3, 4, 5, 6, 7, 8, 9,
			10, 11, 12, 13, 14, 15, 16, 17, 18, 19,
			20, 21, 22, 23, 24, 25, 26, 27, 28, 29,
			30, 31, 32, 33, 34, 35, 36, 37, 38, 39: // 写 40%
			block := rng.Intn(blocks)
			if rng.Intn(20) == 0 {
				block = blocks + rng.Intn(2) // 偶发非法块号
			}
			value := rng.Uint64() % 1000
			failed := map[int]bool{}
			for id := 0; id < members; id++ {
				if rng.Intn(5) == 0 {
					failed[id] = true
				}
			}
			if rng.Intn(30) == 0 {
				failed[members+1] = true // 偶发不存在的成员
			}
			errImpl := impl.Write(block, value, failed)
			errNaive, why := naive.write(block, value, failed)
			if errKindOf(errImpl) != errKindOf(errNaive) {
				t.Fatalf("操作 #%d 写 错误类别不一致: 实现=%v 模型=%v", i, errImpl, errNaive)
			}
			if errNaive != nil {
				why = "判定: " + errNaive.(*Error).Kind.String()
			}
			t.Logf("#%d 写 块=%d 值=%d 失败集=%v | %s => %s | 依据: %s",
				i, block, value, failed, pre, errKindOf(errNaive), why)
			check(i, "写")
		case 40, 41, 42, 43, 44, 45, 46, 47, 48, 49,
			50, 51, 52, 53, 54, 55, 56, 57, 58, 59: // 读 20%
			block := rng.Intn(blocks)
			gotImpl, errImpl := impl.Read(block)
			gotNaive, errNaive, why := naive.read(block)
			if errKindOf(errImpl) != errKindOf(errNaive) || gotImpl != gotNaive {
				t.Fatalf("操作 #%d 读 不一致: 实现=(%d,%v) 模型=(%d,%v)",
					i, gotImpl, errImpl, gotNaive, errNaive)
			}
			if errNaive != nil {
				why = "判定: " + errNaive.(*Error).Kind.String()
			}
			t.Logf("#%d 读 块=%d | %s => 值=%d %s | 依据: %s",
				i, block, pre, gotNaive, errKindOf(errNaive), why)
			check(i, "读")
		case 60, 61, 62, 63, 64, 65, 66, 67, 68, 69: // 报告故障 10%
			id := rng.Intn(members)
			if rng.Intn(25) == 0 {
				id = members + rng.Intn(2) // 偶发不存在的成员
			}
			errImpl := impl.ReportFault(id)
			errNaive, why := naive.reportFault(id)
			if errKindOf(errImpl) != errKindOf(errNaive) {
				t.Fatalf("操作 #%d 报告故障 错误类别不一致: 实现=%v 模型=%v", i, errImpl, errNaive)
			}
			if errNaive != nil {
				why = "判定: " + errNaive.(*Error).Kind.String()
			}
			t.Logf("#%d 报告故障 成员=%d | %s => %s | 依据: %s",
				i, id, pre, errKindOf(errNaive), why)
			check(i, "报告故障")
		case 70, 71, 72, 73, 74, 75, 76, 77, 78, 79,
			80, 81, 82, 83, 84: // 重新加入 15%
			id := rng.Intn(members)
			var label uint64
			if naive.members[id].state == MemberFaulted && rng.Intn(2) == 0 {
				label = naive.members[id].faultGen // 一半概率命中相等标签
			} else {
				label = uint64(rng.Intn(int(naive.gen) + 3)) // 覆盖小于/等于/大于
			}
			errImpl := impl.Rejoin(id, label)
			errNaive, why := naive.rejoin(id, label)
			if errKindOf(errImpl) != errKindOf(errNaive) {
				t.Fatalf("操作 #%d 重新加入 错误类别不一致: 实现=%v 模型=%v", i, errImpl, errNaive)
			}
			if errNaive != nil {
				why = "判定: " + errNaive.(*Error).Kind.String()
			}
			t.Logf("#%d 重新加入 成员=%d 标签=%d | %s => %s | 依据: %s",
				i, id, label, pre, errKindOf(errNaive), why)
			check(i, "重新加入")
		default: // 推进重同步 15%
			id := rng.Intn(members)
			lim := 1 + rng.Intn(5)
			if rng.Intn(30) == 0 {
				lim = 0 // 偶发非法上限
			}
			copiedImpl, errImpl := impl.AdvanceResync(id, lim)
			copiedNaive, errNaive, why := naive.advance(id, lim)
			if errKindOf(errImpl) != errKindOf(errNaive) ||
				!reflect.DeepEqual(copiedImpl, copiedNaive) {
				t.Fatalf("操作 #%d 推进 不一致: 实现=(%v,%v) 模型=(%v,%v)",
					i, copiedImpl, errImpl, copiedNaive, errNaive)
			}
			if errNaive != nil {
				why = "判定: " + errNaive.(*Error).Kind.String()
			}
			t.Logf("#%d 推进 成员=%d 上限=%d | %s => 复制%v %s | 依据: %s",
				i, id, lim, pre, copiedNaive, errKindOf(errNaive), why)
			check(i, "推进")
		}
	}
}

// 与独立编写的朴素逐块模型对照随机操作序列，
// 逐条打印输入、输出与判定依据（go test -v 可见）。
func TestModelRandomOps(t *testing.T) {
	for _, seed := range []int64{1, 7, 42, 2026} {
		seed := seed
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			runRandomOps(t, seed, 1500)
		})
	}
}
