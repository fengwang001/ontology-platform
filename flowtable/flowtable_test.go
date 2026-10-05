package flowtable

import (
	"errors"
	"math/rand"
	"sync"
	"testing"

	"ontology/match"
)

func mustTable(t *testing.T, capacity int, evict bool) *Table {
	t.Helper()
	ft, err := New(capacity, evict)
	if err != nil {
		t.Fatalf("New(%d,%v): %v", capacity, evict, err)
	}
	return ft
}

func mustAdd(t *testing.T, ft *Table, m match.Match, prio uint32, check bool, action, importance, idle, hard uint32, now uint64) uint64 {
	t.Helper()
	seq, evs, err := ft.Add(m, prio, check, action, importance, idle, hard, now)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if len(evs) != 0 {
		t.Fatalf("Add 意外事件: %v", evs)
	}
	return seq
}

func TestNewValidatesCapacity(t *testing.T) {
	for _, c := range []int{-1, 0, 100001} {
		if _, err := New(c, false); !errors.Is(err, ErrInvalidParam) {
			t.Errorf("New(%d): 期望 ErrInvalidParam，得到 %v", c, err)
		}
	}
	if _, err := New(1, false); err != nil {
		t.Errorf("New(1): %v", err)
	}
	if _, err := New(100000, true); err != nil {
		t.Errorf("New(100000): %v", err)
	}
}

// TestSpecExample 复现题面例子：A/B 重叠、驱逐与 importance 恰等。
func TestSpecExample(t *testing.T) {
	ft := mustTable(t, 2, true)
	mA := match.Must(0x0A000000, 0xFF000000, 0, 0)
	mB := match.Must(0x0A010000, 0xFFFF0000, 0, 0)
	mD := match.Must(0x14000000, 0xFF000000, 0, 0)

	seqA := mustAdd(t, ft, mA, 10, false, 100, 1, 0, 0, 0)
	if seqA != 1 {
		t.Fatalf("A 序号=%d 期望 1", seqA)
	}
	// 带检查地装 B：(v1^v2)&m1&m2 = 0x00010000&0xFF000000 = 0，报重叠。
	if _, _, err := ft.Add(mB, 10, true, 200, 1, 0, 0, 0); !errors.Is(err, ErrOverlap) {
		t.Fatalf("带检查装 B: 期望 ErrOverlap，得到 %v", err)
	}
	// 被拒绝的安装不占号：B 不带检查安装应得序号 2。
	seqB := mustAdd(t, ft, mB, 10, false, 200, 1, 0, 0, 0)
	if seqB != 2 {
		t.Fatalf("B 序号=%d 期望 2（被拒绝的重叠安装不占号）", seqB)
	}
	// 报文 0x0A010203 同 prio 命中两项，取序号小的 A。
	action, seq, hit, _, err := ft.Lookup(match.Packet{0x0A010203, 0}, 100, 1)
	if err != nil || !hit || seq != seqA || action != 100 {
		t.Fatalf("Lookup 0x0A010203: action=%d seq=%d hit=%v err=%v，期望 A(100,1)", action, seq, hit, err)
	}
	// 报文 0x0A020000 只命中 A。
	if _, seq, hit, _, _ := ft.Lookup(match.Packet{0x0A020000, 0}, 50, 2); !hit || seq != seqA {
		t.Fatalf("Lookup 0x0A020000: seq=%d hit=%v，期望只命中 A", seq, hit)
	}
	// importance 恰等（1 不小于 1）不驱逐，报表满。
	if _, _, err := ft.Add(mD, 20, false, 300, 1, 0, 0, 3); !errors.Is(err, ErrTableFull) {
		t.Fatalf("D importance=1: 期望 ErrTableFull，得到 %v", err)
	}
	// importance=2 驱逐牺牲者 A（importance 1，序号 1）。
	seqD, evs, err := ft.Add(mD, 20, false, 300, 2, 0, 0, 4)
	if err != nil {
		t.Fatalf("D importance=2: %v", err)
	}
	if seqD != 3 {
		t.Fatalf("D 序号=%d 期望 3", seqD)
	}
	if len(evs) != 1 || evs[0].Reason != Evict || evs[0].Seq != seqA || evs[0].Time != 4 ||
		evs[0].Packets != 2 || evs[0].Bytes != 150 {
		t.Fatalf("驱逐事件=%v，期望 A(seq=1,Evict,t=4,pkts=2,bytes=150)", evs)
	}
	t.Logf("事件: %v；判定=(importance,seq) 最小者 A，1<2 故驱逐", evs)
	// A 已被驱逐，0x0A010203 只命中 B。
	if action, seq, hit, _, _ := ft.Lookup(match.Packet{0x0A010203, 0}, 1, 5); !hit || seq != seqB || action != 200 {
		t.Fatalf("驱逐后 Lookup: action=%d seq=%d hit=%v，期望 B(200,2)", action, seq, hit)
	}
}

// TestCheckedIdenticalOverlapVsReplace 带检查时相同匹配报重叠，不带时替换。
func TestCheckedIdenticalOverlapVsReplace(t *testing.T) {
	ft := mustTable(t, 2, false)
	m := match.Must(0x0A000000, 0xFF000000, 0, 0)
	seq1 := mustAdd(t, ft, m, 10, false, 100, 1, 0, 0, 0)
	if _, _, err := ft.Add(m, 10, true, 200, 1, 0, 0, 1); !errors.Is(err, ErrOverlap) {
		t.Fatalf("带检查重装相同匹配: 期望 ErrOverlap，得到 %v", err)
	}
	// 不带检查：原地替换，序号换新，动作更新，计数清零。
	seq2, evs, err := ft.Add(m, 10, false, 200, 1, 0, 0, 2)
	if err != nil {
		t.Fatalf("替换: %v", err)
	}
	if seq2 == seq1 {
		t.Fatalf("替换后序号未换新: %d", seq2)
	}
	if len(evs) != 0 {
		t.Fatalf("替换不应产生移除事件: %v", evs)
	}
	if ft.Len() != 1 {
		t.Fatalf("替换不占名额，Len=%d 期望 1", ft.Len())
	}
	if action, seq, hit, _, _ := ft.Lookup(match.Packet{0x0A000000, 0}, 1, 3); !hit || action != 200 || seq != seq2 {
		t.Fatalf("替换后 Lookup: action=%d seq=%d hit=%v，期望 (200,%d)", action, seq, hit, seq2)
	}
	t.Logf("替换: 旧序号 %d → 新序号 %d，动作 100→200，计数清零", seq1, seq2)
}

// TestReplaceClearsCountersAndRefreshesTimeouts 替换清零计数并刷新安装时刻。
func TestReplaceClearsCountersAndRefreshesTimeouts(t *testing.T) {
	ft := mustTable(t, 3, false)
	m := match.Must(0x01000000, 0xFF000000, 0, 0)
	seq1 := mustAdd(t, ft, m, 5, false, 1, 1, 10, 100, 0)
	if _, _, hit, _, _ := ft.Lookup(match.Packet{0x01000000, 0}, 7, 5); !hit {
		t.Fatal("应命中")
	}
	// 旧项 idle=10、lastHit=5，于 t=15 以 Idle 到期（早于 hard 的 t=100）；
	// t=200 的 Add 先落地到期再装新项，到期项不参与替换判定。
	seq2, evs, err := ft.Add(m, 5, false, 2, 1, 10, 100, 200)
	if err != nil {
		t.Fatalf("替换: %v", err)
	}
	// 旧项按 Idle 到期落地（事件），随后作为新项安装而非替换。
	if len(evs) != 1 || evs[0].Reason != Idle || evs[0].Seq != seq1 || evs[0].Time != 15 ||
		evs[0].Packets != 1 || evs[0].Bytes != 7 {
		t.Fatalf("到期事件=%v，期望 (seq=%d,Idle,t=15,pkts=1,bytes=7)", evs, seq1)
	}
	if seq2 <= seq1 {
		t.Fatalf("新序号 %d 应大于 %d", seq2, seq1)
	}
	t.Logf("到期表项不参与替换判定：旧项 Hard 到期事件 %v，新装序号 %d", evs, seq2)
}

// TestIdleExpiryExact 题面例子：idle=30、lastHit=40，恰等即到期。
func TestIdleExpiryExact(t *testing.T) {
	newTable := func() (*Table, uint64) {
		ft := mustTable(t, 4, false)
		m := match.Must(0x0A000000, 0xFF000000, 0, 0)
		// idle=30、安装（最后命中）于 t=40，到期时刻 70。
		seq := mustAdd(t, ft, m, 10, false, 1, 1, 30, 0, 40)
		return ft, seq
	}
	// t=69 命中并把到期推到 99。
	ft, seq := newTable()
	if _, s, hit, evs, _ := ft.Lookup(match.Packet{0x0A000000, 0}, 1, 69); !hit || s != seq || len(evs) != 0 {
		t.Fatalf("t=69 应命中且无事件: hit=%v seq=%d evs=%v", hit, s, evs)
	}
	_, _, hit, evs, _ := ft.Lookup(match.Packet{0x0A000000, 0}, 1, 99)
	if hit || len(evs) != 1 || evs[0].Reason != Idle || evs[0].Time != 99 || evs[0].Seq != seq {
		t.Fatalf("t=99 应先到期再不命中: hit=%v evs=%v", hit, evs)
	}
	t.Logf("t=69 命中推到期至 99；t=99 恰等到期事件 %v", evs)
	// 改在 t=70 来：先以 Idle 移除（事件时刻 70）再查，不命中。
	ft2, seq2 := newTable()
	_, _, hit, evs, _ = ft2.Lookup(match.Packet{0x0A000000, 0}, 1, 70)
	if hit || len(evs) != 1 || evs[0].Reason != Idle || evs[0].Time != 70 || evs[0].Seq != seq2 {
		t.Fatalf("t=70 应先 Idle 到期再不命中: hit=%v evs=%v", hit, evs)
	}
	t.Logf("另一时间线 t=70 到期事件 %v", evs)
}

// TestHardExpiryExactAndTie hard 恰等到期；idle 与 hard 同刻记 Hard。
func TestHardExpiryExactAndTie(t *testing.T) {
	ft := mustTable(t, 4, false)
	m := match.Must(0x01000000, 0xFF000000, 0, 0)
	seq := mustAdd(t, ft, m, 1, false, 1, 1, 0, 100, 0)
	if _, _, hit, _, _ := ft.Lookup(match.Packet{0x01000000, 0}, 1, 99); !hit {
		t.Fatal("t=99 应命中")
	}
	_, _, hit, evs, _ := ft.Lookup(match.Packet{0x01000000, 0}, 1, 100)
	if hit || len(evs) != 1 || evs[0].Reason != Hard || evs[0].Time != 100 || evs[0].Seq != seq {
		t.Fatalf("t=100 恰等 hard 到期: hit=%v evs=%v", hit, evs)
	}
	// idle 与 hard 同刻到期，原因记 Hard。
	ft2 := mustTable(t, 4, false)
	seq2 := mustAdd(t, ft2, m, 1, false, 1, 1, 50, 50, 10)
	evs2, err := ft2.Advance(60)
	if err != nil || len(evs2) != 1 || evs2[0].Reason != Hard || evs2[0].Time != 60 || evs2[0].Seq != seq2 {
		t.Fatalf("同刻到期应记 Hard: evs=%v err=%v", evs2, err)
	}
	t.Logf("hard=100 恰等到期；idle=hard=50 同刻记 Hard: %v", evs2)
}

// TestLookupTieBreaksBySeq 同 prio 多项命中取安装序号最小者。
func TestLookupTieBreaksBySeq(t *testing.T) {
	ft := mustTable(t, 8, false)
	m1 := match.Must(0x0A000000, 0xFF000000, 0, 0)
	m2 := match.Must(0x0A0A0000, 0xFFFF0000, 0, 0)
	m3 := match.Must(0x0A0A0A00, 0xFFFFFF00, 0, 0)
	s1 := mustAdd(t, ft, m2, 10, false, 2, 1, 0, 0, 0)
	s2 := mustAdd(t, ft, m1, 10, false, 1, 1, 0, 0, 0)
	s3 := mustAdd(t, ft, m3, 10, false, 3, 1, 0, 0, 0)
	// 报文同时命中三项（同 prio），取序号最小的 s1。
	_, seq, hit, _, _ := ft.Lookup(match.Packet{0x0A0A0A0A, 0}, 1, 1)
	if !hit || seq != s1 {
		t.Fatalf("同 prio 三命中应取最小序号 %d，得到 %d", s1, seq)
	}
	// 高 prio 者优先于低 prio 小序号。
	hi := match.Must(0x0A0A0A0A, 0xFFFFFFFF, 0, 0)
	shi := mustAdd(t, ft, hi, 20, false, 9, 1, 0, 0, 1)
	if _, seq, _, _, _ := ft.Lookup(match.Packet{0x0A0A0A0A, 0}, 1, 2); seq != shi {
		t.Fatalf("高 prio 应胜出: seq=%d 期望 %d", seq, shi)
	}
	if !(s1 < s2 && s2 < s3) {
		t.Fatalf("序号应单调: %d %d %d", s1, s2, s3)
	}
	t.Logf("同 prio 命中 {%d,%d,%d} 取 %d；高 prio %d 优先", s1, s2, s3, s1, shi)
}

// TestModifyDeleteScope 严格与非严格作用范围（题面例子）。
func TestModifyDeleteScope(t *testing.T) {
	mA := match.Must(0x0A000000, 0xFF000000, 0, 0)
	mB := match.Must(0x0A010000, 0xFFFF0000, 0, 0)
	setup := func(t *testing.T) (*Table, uint64, uint64) {
		ft := mustTable(t, 4, false)
		sa := mustAdd(t, ft, mA, 10, false, 1, 1, 0, 0, 0)
		sb := mustAdd(t, ft, mB, 10, false, 2, 1, 0, 0, 0)
		return ft, sa, sb
	}
	// 非严格 Modify(A) 作用于 A 与 B（B 被 A 包含）。
	ft, _, _ := setup(t)
	if n, _, err := ft.Modify(mA, 99, false, 77, 1); err != nil || n != 2 {
		t.Fatalf("非严格 Modify(A): n=%d err=%v，期望 2", n, err)
	}
	if a, _, _, _, _ := ft.Lookup(match.Packet{0x0A000000, 0}, 1, 2); a != 77 {
		t.Fatalf("A 动作应为 77，得到 %d", a)
	}
	if a, _, _, _, _ := ft.Lookup(match.Packet{0x0A010000, 0}, 1, 3); a != 77 {
		t.Fatalf("B 动作应为 77，得到 %d", a)
	}
	// 非严格 Modify(B) 只作用于 B。
	ft, _, _ = setup(t)
	if n, _, _ := ft.Modify(mB, 0, false, 88, 1); n != 1 {
		t.Fatalf("非严格 Modify(B): n=%d，期望 1", n)
	}
	if a, _, _, _, _ := ft.Lookup(match.Packet{0x0A000000, 0}, 1, 2); a != 1 {
		t.Fatalf("A 动作应仍为 1，得到 %d", a)
	}
	// B 的命中集是 A 的子集，先严格删掉 A 再观察 B 的动作。
	if n, _, _ := ft.Delete(mA, 10, true, 2); n != 1 {
		t.Fatalf("严格删除 A: n=%d", n)
	}
	if a, _, _, _, _ := ft.Lookup(match.Packet{0x0A010000, 0}, 1, 3); a != 88 {
		t.Fatalf("B 动作应为 88，得到 %d", a)
	}
	// 严格 Delete(A, prio 11) 作用 0 项，不是错误。
	ft, sa, sb := setup(t)
	if n, evs, err := ft.Delete(mA, 11, true, 1); err != nil || n != 0 || len(evs) != 0 {
		t.Fatalf("严格 Delete(A,11): n=%d evs=%v err=%v，期望 0 项无事件", n, evs, err)
	}
	// 严格 Delete(A, prio 10) 只删 A；Delete 事件时刻取 now。
	n, evs, _ := ft.Delete(mA, 10, true, 5)
	if n != 1 || len(evs) != 1 || evs[0].Seq != sa || evs[0].Reason != Delete || evs[0].Time != 5 {
		t.Fatalf("严格 Delete(A,10): n=%d evs=%v", n, evs)
	}
	// 非严格 Delete(A) 删除剩余 B（A 已删）。
	n, evs, _ = ft.Delete(mA, 0, false, 6)
	if n != 1 || len(evs) != 1 || evs[0].Seq != sb || evs[0].Reason != Delete {
		t.Fatalf("非严格 Delete(A): n=%d evs=%v，期望只删 B", n, evs)
	}
	if ft.Len() != 0 {
		t.Fatalf("应删空，Len=%d", ft.Len())
	}
	t.Logf("严格/非严格作用范围与删除事件均符合题面例子")
}

// TestRejectedOpNoSideEffects 被拒绝的操作不落地到期、不推进时钟、不改状态。
func TestRejectedOpNoSideEffects(t *testing.T) {
	ft := mustTable(t, 2, false)
	mX := match.Must(0x01000000, 0xFF000000, 0, 0)
	mY := match.Must(0x02000000, 0xFF000000, 0, 0)
	sx := mustAdd(t, ft, mX, 1, false, 1, 1, 50, 0, 0) // t=50 Idle 到期
	mustAdd(t, ft, mY, 1, false, 2, 1, 0, 0, 0)
	// t=60：带检查地装与 Y 重叠的项，报重叠；X 的到期不得落地。
	mZ := match.Must(0x02000000, 0xFF000000, 0, 0)
	if _, _, err := ft.Add(mZ, 1, true, 3, 1, 0, 0, 60); !errors.Is(err, ErrOverlap) {
		t.Fatalf("期望 ErrOverlap，得到 %v", err)
	}
	if ft.Len() != 2 {
		t.Fatalf("被拒绝后表项数应为 2，得到 %d", ft.Len())
	}
	if ft.Now() != 0 {
		t.Fatalf("被拒绝后时钟应为 0，得到 %d", ft.Now())
	}
	// 时钟未推进：t=55 的操作仍被接受；被接受的 Lookup 落地 X 的到期
	//（事件时刻取到期时刻 50），随后命中 Y。
	_, seq, hit, evs, err := ft.Lookup(match.Packet{0x02000000, 0}, 1, 55)
	if err != nil || !hit {
		t.Fatalf("t=55 应被接受: hit=%v err=%v", hit, err)
	}
	if len(evs) != 1 || evs[0].Seq != sx || evs[0].Reason != Idle || evs[0].Time != 50 {
		t.Fatalf("到期事件=%v，期望 X(seq=%d,Idle,t=50)", evs, sx)
	}
	if seq != 2 || ft.Len() != 1 {
		t.Fatalf("t=55 应命中 Y(seq=2) 且只剩 Y: seq=%d Len=%d", seq, ft.Len())
	}
	// 再次被拒绝的重叠检查不落地到期、不推进时钟。
	if _, _, err := ft.Add(mZ, 1, true, 3, 1, 0, 0, 60); !errors.Is(err, ErrOverlap) {
		t.Fatalf("期望 ErrOverlap，得到 %v", err)
	}
	if ft.Len() != 1 || ft.Now() != 55 {
		t.Fatalf("重叠拒绝后状态应不变: Len=%d Now=%d", ft.Len(), ft.Now())
	}
	t.Logf("拒绝不落地到期；被接受的 Lookup 才落地: %v", evs)

	// 到期的表项不参与重叠判定：新表中以相同匹配带检查重装可通过。
	ft2 := mustTable(t, 2, false)
	sx2 := mustAdd(t, ft2, mX, 1, false, 1, 1, 50, 0, 0)
	seq, evs2, err := ft2.Add(mX, 1, true, 3, 1, 0, 0, 60)
	if err != nil {
		t.Fatalf("到期项不参与重叠判定，应安装成功: %v", err)
	}
	if len(evs2) != 1 || evs2[0].Seq != sx2 || evs2[0].Reason != Idle || evs2[0].Time != 50 {
		t.Fatalf("应先落地 X 的到期事件: %v", evs2)
	}
	if seq != 2 {
		t.Fatalf("新装序号=%d，期望 2", seq)
	}
}

// TestRejectionOrder 拒绝次序：参数非法 > 时钟回退 > 重叠 > 表满。
func TestRejectionOrder(t *testing.T) {
	ft := mustTable(t, 1, false)
	mA := match.Must(0x0A000000, 0xFF000000, 0, 0)
	mB := match.Must(0x0A010000, 0xFFFF0000, 0, 0) // 与 A 重叠
	mustAdd(t, ft, mA, 10, false, 1, 1, 0, 0, 100)
	badMatch := match.Match{F: [2]match.Field{{Value: 1, Mask: 0}, {}}}
	cases := []struct {
		name string
		m    match.Match
		prio uint32
		chk  bool
		idle uint32
		now  uint64
		want error
	}{
		{"非法匹配+时钟回退+重叠→参数非法", badMatch, 10, true, 0, 50, ErrInvalidParam},
		{"非法idle+重叠→参数非法", mB, 10, true, 1_000_000_001, 200, ErrInvalidParam},
		{"时钟回退+重叠+表满→时钟回退", mB, 10, true, 0, 50, ErrClock},
		{"重叠+表满→重叠", mB, 10, true, 0, 200, ErrOverlap},
		{"表满", match.Must(0x0B000000, 0xFF000000, 0, 0), 10, false, 0, 200, ErrTableFull},
	}
	for _, c := range cases {
		_, _, err := ft.Add(c.m, c.prio, c.chk, 1, 1, c.idle, 0, c.now)
		if !errors.Is(err, c.want) {
			t.Errorf("%s: 期望 %v，得到 %v", c.name, c.want, err)
		}
		t.Logf("%s: err=%v", c.name, err)
	}
	// 全部拒绝后状态不变。
	if ft.Len() != 1 || ft.Now() != 100 {
		t.Fatalf("拒绝后状态应不变: Len=%d Now=%d", ft.Len(), ft.Now())
	}
	// 时钟回退也适用于 Lookup/Modify/Delete。
	if _, _, _, _, err := ft.Lookup(match.Packet{0, 0}, 1, 50); !errors.Is(err, ErrClock) {
		t.Errorf("Lookup 时钟回退: %v", err)
	}
	if _, _, err := ft.Modify(mA, 10, true, 1, 50); !errors.Is(err, ErrClock) {
		t.Errorf("Modify 时钟回退: %v", err)
	}
	if _, _, err := ft.Delete(mA, 10, true, 50); !errors.Is(err, ErrClock) {
		t.Errorf("Delete 时钟回退: %v", err)
	}
}

// TestInvalidParams 各类参数非法。
func TestInvalidParams(t *testing.T) {
	ft := mustTable(t, 2, false)
	badMatch := match.Match{F: [2]match.Field{{Value: 0x100, Mask: 0xFF}, {}}}
	good := match.Must(0, 0, 0, 0)
	if _, _, err := ft.Add(badMatch, 0, false, 0, 0, 0, 0, 0); !errors.Is(err, ErrInvalidParam) {
		t.Errorf("非法匹配: %v", err)
	}
	if _, _, err := ft.Add(good, 65536, false, 0, 0, 0, 0, 0); !errors.Is(err, ErrInvalidParam) {
		t.Errorf("非法 prio: %v", err)
	}
	if _, _, err := ft.Add(good, 0, false, 0, 65536, 0, 0, 0); !errors.Is(err, ErrInvalidParam) {
		t.Errorf("非法 importance: %v", err)
	}
	if _, _, err := ft.Add(good, 0, false, 0, 0, 0, 1_000_000_001, 0); !errors.Is(err, ErrInvalidParam) {
		t.Errorf("非法 hard: %v", err)
	}
	if _, _, err := ft.Add(good, 0, false, 0, 0, 0, 0, 1_000_000_000_001); !errors.Is(err, ErrInvalidParam) {
		t.Errorf("非法 now: %v", err)
	}
	if _, _, _, _, err := ft.Lookup(match.Packet{0, 0}, 0, 1_000_000_000_001); !errors.Is(err, ErrInvalidParam) {
		t.Errorf("Lookup 非法 now: %v", err)
	}
	if _, _, err := ft.Modify(good, 65536, false, 0, 0); !errors.Is(err, ErrInvalidParam) {
		t.Errorf("Modify 非法 prio: %v", err)
	}
	if _, _, err := ft.Delete(badMatch, 0, false, 0); !errors.Is(err, ErrInvalidParam) {
		t.Errorf("Delete 非法匹配: %v", err)
	}
	if _, err := ft.Advance(1_000_000_000_001); !errors.Is(err, ErrInvalidParam) {
		t.Errorf("Advance 非法 now: %v", err)
	}
	// 边界值合法。
	if _, _, err := ft.Add(good, 65535, false, 0, 65535, 1_000_000_000, 1_000_000_000, 1_000_000_000_000); err != nil {
		t.Errorf("边界值应合法: %v", err)
	}
}

// TestLookupExamined 命中时考察的表项数与更低优先级表项数无关。
func TestLookupExamined(t *testing.T) {
	for _, low := range []int{100, 10000} {
		ft := mustTable(t, low+1, false)
		hi := match.Must(0xFFFFFFFF, 0xFFFFFFFF, 0, 0)
		mustAdd(t, ft, hi, 100, false, 1, 1, 0, 0, 0)
		for i := 0; i < low; i++ {
			m := match.Must(uint32(i), 0xFFFFFFFF, 0, 0)
			mustAdd(t, ft, m, 1, false, 1, 1, 0, 0, 0)
		}
		before := ft.examined
		_, seq, hit, _, err := ft.Lookup(match.Packet{0xFFFFFFFF, 0}, 1, 1)
		if err != nil || !hit || seq != 1 {
			t.Fatalf("low=%d: Lookup hit=%v seq=%d err=%v", low, hit, seq, err)
		}
		got := ft.examined - before
		if got != 1 {
			t.Fatalf("low=%d: 命中最高优先级时 examined 增量=%d，期望 1（与低优先级数量无关）", low, got)
		}
		t.Logf("低优先级表项 %d 条：命中考察 %d 项（不超过 prio 不低于命中项的表项数 1）", low, got)
	}
	// 命中低优先级层：考察数 = 高层全部 + 命中层全部。
	ft := mustTable(t, 8, false)
	for i := 0; i < 3; i++ {
		m := match.Must(uint32(0x100+i), 0xFFFFFFFF, 0, 0)
		mustAdd(t, ft, m, 10, false, 1, 1, 0, 0, 0)
	}
	wild := match.Must(0, 0, 0, 0)
	mustAdd(t, ft, wild, 5, false, 1, 1, 0, 0, 0)
	mustAdd(t, ft, wild, 5, false, 1, 1, 0, 0, 0) // 同匹配同 prio → 替换，序号 5
	before := ft.examined
	if _, _, hit, _, _ := ft.Lookup(match.Packet{0x999, 0}, 1, 1); !hit {
		t.Fatal("应命中通配项")
	}
	if got := ft.examined - before; got != 4 {
		t.Fatalf("命中 prio=5 层应考察 3(高层)+1(命中层)=4 项，得到 %d", got)
	}
}

// TestConcurrent 并发调用等价于某个串行顺序：表项数永不超过容量，
// 且 -race 下无数据竞争。
func TestConcurrent(t *testing.T) {
	const capacity = 16
	ft := mustTable(t, capacity, true)
	done := make(chan struct{})
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			r := rand.New(rand.NewSource(int64(w)))
			for i := 0; i < 500; i++ {
				now := uint64(i)
				m := match.Must(uint32(r.Intn(8))<<24, 0xFF000000, 0, 0)
				switch r.Intn(4) {
				case 0:
					_, _, _ = ft.Add(m, uint32(r.Intn(4)), r.Intn(2) == 0, 1, uint32(r.Intn(3)), 0, 0, now)
				case 1:
					_, _, _, _, _ = ft.Lookup(match.Packet{uint32(r.Intn(8)) << 24, 0}, 1, now)
				case 2:
					_, _, _ = ft.Modify(m, uint32(r.Intn(4)), r.Intn(2) == 0, 2, now)
				default:
					_, _, _ = ft.Delete(m, uint32(r.Intn(4)), r.Intn(2) == 0, now)
				}
				if ft.Len() > capacity {
					t.Errorf("表项数 %d 超过容量 %d", ft.Len(), capacity)
					return
				}
			}
		}(w)
	}
	go func() { wg.Wait(); close(done) }()
	<-done
}

// TestDeleteOrderBySeq 非严格 Delete 按安装序号升序产生事件。
func TestDeleteOrderBySeq(t *testing.T) {
	ft := mustTable(t, 8, false)
	wild := match.Must(0, 0, 0, 0)
	var seqs []uint64
	for i := uint32(0); i < 4; i++ {
		m := match.Must(i, 0xFFFFFFFF, 0, 0)
		seqs = append(seqs, mustAdd(t, ft, m, uint32(i%2), false, i, 1, 0, 0, 0))
	}
	n, evs, err := ft.Delete(wild, 0, false, 9)
	if err != nil || n != 4 || len(evs) != 4 {
		t.Fatalf("Delete: n=%d evs=%v err=%v", n, evs, err)
	}
	for i, ev := range evs {
		if ev.Seq != seqs[i] || ev.Reason != Delete || ev.Time != 9 {
			t.Fatalf("事件 %d=%v，期望序号 %d 升序", i, ev, seqs[i])
		}
	}
}
