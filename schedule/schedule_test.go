package schedule

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"testing"
)

// ---------- 朴素对照实现：逐分钟扫描 ----------

type naiveOverride struct {
	id, member string
	s, e       int64
	seq        int
}

type naive struct {
	members []string
	t0, L   int64
	ovs     []naiveOverride // 仅含未删除的覆盖
	nextSeq int
}

func newNaive(members []string, t0, L int64) *naive {
	return &naive{members: members, t0: t0, L: L}
}

func (n *naive) add(id, member string, s, e int64) {
	n.nextSeq++
	n.ovs = append(n.ovs, naiveOverride{id: id, member: member, s: s, e: e, seq: n.nextSeq})
}

func (n *naive) remove(id string) {
	for i, ov := range n.ovs {
		if ov.id == id {
			n.ovs = append(n.ovs[:i], n.ovs[i+1:]...)
			return
		}
	}
}

func (n *naive) who(t int64) (string, string) {
	var best *naiveOverride
	for i := range n.ovs {
		ov := &n.ovs[i]
		if ov.s <= t && t < ov.e && (best == nil || ov.seq > best.seq) {
			best = ov
		}
	}
	if best != nil {
		return best.member, best.id
	}
	idx := floorMod(floorDiv(t-n.t0, n.L), int64(len(n.members)))
	return n.members[idx], RotationSource
}

func (n *naive) timeline(a, b int64) []Segment {
	var out []Segment
	for t := a; t < b; t++ {
		m, src := n.who(t)
		if len(out) > 0 && out[len(out)-1].Member == m && out[len(out)-1].Source == src {
			out[len(out)-1].End = t + 1
			continue
		}
		out = append(out, Segment{Start: t, End: t + 1, Member: m, Source: src})
	}
	return out
}

// ---------- 测试辅助 ----------

func mustNew(t *testing.T, members []string, t0, period int64) *Scheduler {
	t.Helper()
	s, err := NewScheduler(members, t0, period)
	if err != nil {
		t.Fatalf("NewScheduler(%v, %d, %d) 意外失败: %v", members, t0, period, err)
	}
	return s
}

func mustAdd(t *testing.T, s *Scheduler, id, member string, start, end int64) {
	t.Helper()
	if err := s.AddOverride(id, member, start, end); err != nil {
		t.Fatalf("AddOverride(%q, %q, %d, %d) 意外失败: %v", id, member, start, end, err)
	}
}

func mustRemove(t *testing.T, s *Scheduler, id string) {
	t.Helper()
	if err := s.RemoveOverride(id); err != nil {
		t.Fatalf("RemoveOverride(%q) 意外失败: %v", id, err)
	}
}

func checkWho(t *testing.T, s *Scheduler, at int64, wantMember, wantSource string) {
	t.Helper()
	gotM, gotS := s.Who(at)
	t.Logf("输入 Who(%d) -> 输出 (%s, %s)；判定依据: 期望 (%s, %s)", at, gotM, gotS, wantMember, wantSource)
	if gotM != wantMember || gotS != wantSource {
		t.Errorf("Who(%d) = (%s, %s), 期望 (%s, %s)", at, gotM, gotS, wantMember, wantSource)
	}
}

func checkTimeline(t *testing.T, s *Scheduler, a, b int64, want []Segment) {
	t.Helper()
	got, err := s.Timeline(a, b)
	if err != nil {
		t.Fatalf("Timeline(%d, %d) 意外失败: %v", a, b, err)
	}
	t.Logf("输入 Timeline(%d, %d) -> 输出 %v；判定依据: 期望 %v", a, b, got, want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Timeline(%d, %d) = %v, 期望 %v", a, b, got, want)
	}
}

// checkTimelineInvariants 校验：段首尾相接恰好覆盖 [a,b)、相邻段不可再合并、
// 且与 Who 在段内抽样一致。
func checkTimelineInvariants(t *testing.T, s *Scheduler, a, b int64, segs []Segment) {
	t.Helper()
	if len(segs) == 0 {
		t.Fatalf("Timeline(%d, %d) 返回空段列表", a, b)
	}
	if segs[0].Start != a {
		t.Errorf("首段起点 %d != 查询起点 %d", segs[0].Start, a)
	}
	if segs[len(segs)-1].End != b {
		t.Errorf("末段终点 %d != 查询终点 %d", segs[len(segs)-1].End, b)
	}
	for i, seg := range segs {
		if seg.Start >= seg.End {
			t.Errorf("段 %d 为空或颠倒: %+v", i, seg)
		}
		if i+1 < len(segs) {
			next := segs[i+1]
			if seg.End != next.Start {
				t.Errorf("段 %d 与段 %d 不相接: %d != %d", i, i+1, seg.End, next.Start)
			}
			if seg.Member == next.Member && seg.Source == next.Source {
				t.Errorf("相邻段 %d 与 %d 可再合并: %+v / %+v", i, i+1, seg, next)
			}
		}
		m, src := s.Who(seg.Start)
		if m != seg.Member || src != seg.Source {
			t.Errorf("Who(%d) = (%s, %s) 与段 %+v 不一致", seg.Start, m, src, seg)
		}
		m, src = s.Who(seg.End - 1)
		if m != seg.Member || src != seg.Source {
			t.Errorf("Who(%d) = (%s, %s) 与段 %+v 不一致", seg.End-1, m, src, seg)
		}
	}
}

// ---------- 轮值公式与边界 ----------

// t 恰为 T0、T0-1、T0-L 及附近时刻的轮值归属。
func TestRotationKeyInstants(t *testing.T) {
	members := []string{"A", "B", "C"}
	const T0, L = 1000, 10
	s := mustNew(t, members, T0, L)

	checkWho(t, s, T0, "A", RotationSource)     // t == T0，第 0 班
	checkWho(t, s, T0-1, "C", RotationSource)   // t == T0-1，向前外推为最后一名成员
	checkWho(t, s, T0-L, "C", RotationSource)   // t == T0-L，恰为上一班起点
	checkWho(t, s, T0-L-1, "B", RotationSource) // 再往前一分钟
	checkWho(t, s, T0+L-1, "A", RotationSource) // 第 0 班最后一分钟
	checkWho(t, s, T0+L, "B", RotationSource)   // 第 1 班起点
	checkWho(t, s, T0+2*L, "C", RotationSource)
	checkWho(t, s, T0+3*L, "A", RotationSource) // 名册循环
}

// 班次边界与覆盖边界重合：覆盖恰占一个整班，结束后恢复轮值；
// 覆盖成员与轮值成员相同也不合并（来源不同）。
func TestShiftBoundaryCoincidesOverrideBoundary(t *testing.T) {
	members := []string{"A", "B", "C"}
	const T0, L = 1000, 10
	s := mustNew(t, members, T0, L)
	mustAdd(t, s, "ov1", "C", T0+L, T0+2*L) // 恰好覆盖 C 的整班

	checkTimeline(t, s, T0, T0+3*L, []Segment{
		{T0, T0 + L, "A", RotationSource},
		{T0 + L, T0 + 2*L, "C", "ov1"},
		{T0 + 2*L, T0 + 3*L, "C", RotationSource}, // 恢复轮值，不与 ov1 合并
	})
	checkWho(t, s, T0+2*L, "C", RotationSource) // 覆盖右端点已恢复轮值
	checkWho(t, s, T0+2*L-1, "C", "ov1")
}

// 三个覆盖相互嵌套与交叉。
func TestNestedAndCrossingOverrides(t *testing.T) {
	members := []string{"A", "B"}
	s := mustNew(t, members, 0, 10)
	mustAdd(t, s, "ov1", "A", 0, 100)  // 外层
	mustAdd(t, s, "ov2", "B", 20, 60)  // 嵌套于 ov1
	mustAdd(t, s, "ov3", "A", 50, 150) // 与 ov2 交叉并越出 ov1 右端

	// 最后添加的 ov3 在 [50,150) 全面优先，跨越 ov2 右端与 ov1 右端合并为一段。
	checkTimeline(t, s, 0, 200, []Segment{
		{0, 20, "A", "ov1"},
		{20, 50, "B", "ov2"},
		{50, 150, "A", "ov3"},
		{150, 160, "B", RotationSource},
		{160, 170, "A", RotationSource},
		{170, 180, "B", RotationSource},
		{180, 190, "A", RotationSource},
		{190, 200, "B", RotationSource},
	})

	// 删除最后添加者 ov3 后，让位给更早的 ov2 / ov1 / 轮值。
	mustRemove(t, s, "ov3")
	checkTimeline(t, s, 0, 130, []Segment{
		{0, 20, "A", "ov1"},
		{20, 60, "B", "ov2"},
		{60, 100, "A", "ov1"},
		{100, 110, "A", RotationSource}, // 轮值与 ov1 同为 A 但来源不同，不合并
		{110, 120, "B", RotationSource},
		{120, 130, "A", RotationSource},
	})

	// 再删除 ov2，ov1 独占 [0,100)。
	mustRemove(t, s, "ov2")
	checkTimeline(t, s, 0, 120, []Segment{
		{0, 100, "A", "ov1"},
		{100, 110, "A", RotationSource},
		{110, 120, "B", RotationSource},
	})
}

// 删除后最后添加者让位给更早者；全部删除后恢复轮值。
func TestRemoveRevealsEarlierOverride(t *testing.T) {
	s := mustNew(t, []string{"A", "B"}, 0, 10)
	mustAdd(t, s, "early", "A", 0, 50)
	mustAdd(t, s, "late", "B", 20, 30)

	checkWho(t, s, 25, "B", "late")
	mustRemove(t, s, "late")
	checkWho(t, s, 25, "A", "early") // 让位给更早添加者
	mustRemove(t, s, "early")
	checkWho(t, s, 25, "A", RotationSource) // 恢复轮值（slot 2 -> A）

	// 已删除的 id 可再次添加，视为全新的最后添加者。
	mustAdd(t, s, "early", "A", 0, 50)
	mustAdd(t, s, "late", "B", 20, 30)
	mustRemove(t, s, "early")
	mustAdd(t, s, "early", "A", 0, 50) // 重新添加，成为最新
	checkWho(t, s, 25, "A", "early")   // 比 late 更晚添加，优先生效
}

// 单人名册：所有班合并为一段；覆盖打断但不与轮值合并。
func TestSingleMemberRosterMerges(t *testing.T) {
	s := mustNew(t, []string{"solo"}, 0, 10)
	checkTimeline(t, s, 0, 45, []Segment{
		{0, 45, "solo", RotationSource}, // 跨 4 个班合并为一段
	})

	mustAdd(t, s, "ov", "solo", 15, 25)
	checkTimeline(t, s, 0, 45, []Segment{
		{0, 15, "solo", RotationSource},
		{15, 25, "solo", "ov"}, // 成员相同但来源不同，不合并
		{25, 45, "solo", RotationSource},
	})
}

// 同成员不同 id 的覆盖不合并。
func TestSameMemberDifferentIDNoMerge(t *testing.T) {
	s := mustNew(t, []string{"A", "B"}, 0, 10)
	mustAdd(t, s, "ov1", "A", 0, 10)
	mustAdd(t, s, "ov2", "A", 10, 20)
	checkTimeline(t, s, 0, 20, []Segment{
		{0, 10, "A", "ov1"},
		{10, 20, "A", "ov2"}, // 相邻、同成员、不同 id，不合并
	})
}

// 覆盖结束后恢复轮值。
func TestOverrideEndRestoresRotation(t *testing.T) {
	s := mustNew(t, []string{"A", "B"}, 0, 10)
	mustAdd(t, s, "ov", "B", 5, 15)
	checkTimeline(t, s, 0, 30, []Segment{
		{0, 5, "A", RotationSource},
		{5, 15, "B", "ov"},
		{15, 20, "B", RotationSource}, // 恢复轮值，来源不同不合并
		{20, 30, "A", RotationSource},
	})
}

// ---------- 校验与拒绝 ----------

func TestSchedulerValidation(t *testing.T) {
	if _, err := NewScheduler(nil, 0, 10); !errors.Is(err, ErrEmptyRoster) {
		t.Errorf("空名册: got %v, want ErrEmptyRoster", err)
	}
	if _, err := NewScheduler([]string{"A", "A"}, 0, 10); !errors.Is(err, ErrDuplicateMember) {
		t.Errorf("重复成员: got %v, want ErrDuplicateMember", err)
	}
	if _, err := NewScheduler([]string{"A"}, 0, 0); !errors.Is(err, ErrNonPositivePeriod) {
		t.Errorf("L=0: got %v, want ErrNonPositivePeriod", err)
	}
	if _, err := NewScheduler([]string{"A"}, 0, -5); !errors.Is(err, ErrNonPositivePeriod) {
		t.Errorf("L<0: got %v, want ErrNonPositivePeriod", err)
	}
}

func TestAddOverrideValidationOrder(t *testing.T) {
	s := mustNew(t, []string{"A", "B"}, 0, 10)
	mustAdd(t, s, "dup", "A", 0, 10)

	// 检查顺序：区间 -> 成员 -> id。三种错误同时存在时按顺序报第一个。
	if err := s.AddOverride("dup", "ghost", 10, 10); !errors.Is(err, ErrInvalidInterval) {
		t.Errorf("空区间优先: got %v, want ErrInvalidInterval", err)
	}
	if err := s.AddOverride("dup", "ghost", 20, 10); !errors.Is(err, ErrInvalidInterval) {
		t.Errorf("颠倒区间优先: got %v, want ErrInvalidInterval", err)
	}
	if err := s.AddOverride("dup", "ghost", 0, 10); !errors.Is(err, ErrMemberNotInRoster) {
		t.Errorf("成员次之: got %v, want ErrMemberNotInRoster", err)
	}
	if err := s.AddOverride("dup", "A", 0, 10); !errors.Is(err, ErrDuplicateOverlayID) {
		t.Errorf("id 最后: got %v, want ErrDuplicateOverlayID", err)
	}
	if err := s.AddOverride("ok", "ghost", 0, 10); !errors.Is(err, ErrMemberNotInRoster) {
		t.Errorf("成员不在名册: got %v, want ErrMemberNotInRoster", err)
	}

	// 被拒绝的操作不得改变覆盖集合。
	before, err := s.Timeline(0, 20)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][4]interface{}{
		{"x", "A", int64(5), int64(5)},
		{"y", "ghost", int64(0), int64(5)},
		{"dup", "A", int64(0), int64(5)},
	} {
		_ = s.AddOverride(bad[0].(string), bad[1].(string), bad[2].(int64), bad[3].(int64))
	}
	after, err := s.Timeline(0, 20)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Errorf("被拒绝的添加改变了覆盖集合: before=%v after=%v", before, after)
	}
}

func TestRemoveAndTimelineValidation(t *testing.T) {
	s := mustNew(t, []string{"A"}, 0, 10)
	if err := s.RemoveOverride("missing"); !errors.Is(err, ErrOverrideNotFound) {
		t.Errorf("删除不存在的 id: got %v, want ErrOverrideNotFound", err)
	}
	mustAdd(t, s, "ov", "A", 0, 5)
	mustRemove(t, s, "ov")
	if err := s.RemoveOverride("ov"); !errors.Is(err, ErrOverrideNotFound) {
		t.Errorf("重复删除: got %v, want ErrOverrideNotFound", err)
	}
	if _, err := s.Timeline(10, 10); !errors.Is(err, ErrInvalidInterval) {
		t.Errorf("空查询区间: got %v, want ErrInvalidInterval", err)
	}
	if _, err := s.Timeline(20, 10); !errors.Is(err, ErrInvalidInterval) {
		t.Errorf("颠倒查询区间: got %v, want ErrInvalidInterval", err)
	}
}

func TestTooManySegments(t *testing.T) {
	s := mustNew(t, []string{"A", "B"}, 0, 1) // L=1，每分钟换班
	if _, err := s.Timeline(0, MaxSegments); err != nil {
		t.Errorf("恰好 %d 段应被接受: %v", MaxSegments, err)
	}
	if _, err := s.Timeline(0, MaxSegments+1); !errors.Is(err, ErrTooManySegments) {
		t.Errorf("%d 段应被拒绝: got %v, want ErrTooManySegments", MaxSegments+1, err)
	}
	// 大覆盖合并掉轮值边界后不应误报。
	s2 := mustNew(t, []string{"A", "B"}, 0, 1)
	mustAdd(t, s2, "big", "A", 0, 20000)
	segs, err := s2.Timeline(0, 20000)
	if err != nil {
		t.Fatalf("覆盖合并后不应触发段数上限: %v", err)
	}
	if len(segs) != 1 || segs[0].Source != "big" {
		t.Errorf("期望单段覆盖, got %v", segs)
	}
}

// ---------- 与朴素实现对照 ----------

// 固定场景 + 随机场景，均与逐分钟扫描的朴素实现对照。
func TestAgainstNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(20261001))
	for trial := 0; trial < 60; trial++ {
		n := 1 + rng.Intn(4)
		members := make([]string, n)
		for i := range members {
			members[i] = fmt.Sprintf("m%d", i)
		}
		t0 := int64(rng.Intn(121) - 60)
		period := int64(1 + rng.Intn(12))

		s := mustNew(t, members, t0, period)
		nv := newNaive(members, t0, period)

		// 随机添加/删除覆盖。
		active := map[string]bool{}
		for op := 0; op < 8; op++ {
			id := fmt.Sprintf("ov%d", rng.Intn(6))
			if rng.Intn(3) == 0 && active[id] {
				if err := s.RemoveOverride(id); err != nil {
					t.Fatalf("trial %d: RemoveOverride(%q): %v", trial, id, err)
				}
				nv.remove(id)
				delete(active, id)
				continue
			}
			if active[id] {
				continue
			}
			a := t0 + int64(rng.Intn(121)-60)
			b := a + int64(1+rng.Intn(40))
			m := members[rng.Intn(len(members))]
			if err := s.AddOverride(id, m, a, b); err != nil {
				t.Fatalf("trial %d: AddOverride(%q,%q,%d,%d): %v", trial, id, m, a, b, err)
			}
			nv.add(id, m, a, b)
			active[id] = true
		}

		a := t0 + int64(rng.Intn(81)-40)
		b := a + int64(1+rng.Intn(120))
		got, err := s.Timeline(a, b)
		if err != nil {
			t.Fatalf("trial %d: Timeline(%d,%d): %v", trial, a, b, err)
		}
		want := nv.timeline(a, b)
		t.Logf("trial %d 输入: members=%v T0=%d L=%d 覆盖=%v 查询=[%d,%d)",
			trial, members, t0, period, nv.ovs, a, b)
		t.Logf("trial %d 输出: %v", trial, got)
		t.Logf("trial %d 判定依据: 与逐分钟朴素扫描结果 %v 完全一致", trial, want)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("trial %d: Timeline(%d,%d) = %v, 朴素实现 = %v", trial, a, b, got, want)
		}
		checkTimelineInvariants(t, s, a, b, got)

		// 区间内逐分钟 Who 与朴素实现一致。
		for x := a; x < b; x++ {
			gm, gs := s.Who(x)
			wm, ws := nv.who(x)
			if gm != wm || gs != ws {
				t.Fatalf("trial %d: Who(%d) = (%s,%s), 朴素实现 = (%s,%s)", trial, x, gm, gs, wm, ws)
			}
		}
	}
}

// 相同操作序列重放得到完全相同的结果。
func TestReplayDeterminism(t *testing.T) {
	play := func() []Segment {
		s := mustNew(t, []string{"A", "B", "C"}, 100, 7)
		mustAdd(t, s, "o1", "B", 90, 130)
		mustAdd(t, s, "o2", "C", 100, 120)
		mustRemove(t, s, "o1")
		mustAdd(t, s, "o1", "A", 95, 125)
		segs, err := s.Timeline(80, 150)
		if err != nil {
			t.Fatal(err)
		}
		return segs
	}
	first := play()
	for i := 0; i < 5; i++ {
		if got := play(); !reflect.DeepEqual(first, got) {
			t.Fatalf("重放结果不一致: %v vs %v", first, got)
		}
	}
}

// 并发调用：结果等价于某个串行顺序（配合 -race 验证）。
func TestConcurrentAccess(t *testing.T) {
	s := mustNew(t, []string{"A", "B", "C"}, 0, 10)
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			id := fmt.Sprintf("w%d", w)
			for i := 0; i < 50; i++ {
				if err := s.AddOverride(id, "A", int64(i), int64(i+5)); err != nil {
					t.Errorf("AddOverride: %v", err)
					return
				}
				if err := s.RemoveOverride(id); err != nil {
					t.Errorf("RemoveOverride: %v", err)
					return
				}
			}
		}(w)
	}
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				_, _ = s.Who(int64(i))
				if _, err := s.Timeline(0, 100); err != nil {
					t.Errorf("Timeline: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
}
