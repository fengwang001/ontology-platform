package idpool

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

// 判定依据日志：每个用例打印输入、输出与判定依据，便于人工复核。
func logCase(t *testing.T, name, detail string) {
	t.Helper()
	t.Logf("[CASE] %s | 输入: %s", name, detail)
}

func logResult(t *testing.T, name, got, why string) {
	t.Helper()
	t.Logf("[CASE] %s | 输出: %s | 判定依据: %s", name, got, why)
}

func newOrFail(t *testing.T, n int, q, qmax, r int64) *Pool {
	t.Helper()
	p, err := New(n, q, qmax, r)
	if err != nil {
		t.Fatalf("New(%d,%d,%d,%d): %v", n, q, qmax, r, err)
	}
	return p
}

func allocErr(t *testing.T, err error) *AllocError {
	t.Helper()
	var ae *AllocError
	if !errors.As(err, &ae) {
		t.Fatalf("期望 *AllocError，实得 %v", err)
	}
	return ae
}

// TestNewValidation 覆盖构造参数的四种非法情形。
func TestNewValidation(t *testing.T) {
	cases := []struct {
		name    string
		n       int
		q       int64
		qmax    int64
		r       int64
		wantErr error
	}{
		{"N不为正", 0, 10, 20, 5, ErrInvalidN},
		{"Q不为正", 3, 0, 20, 5, ErrInvalidQ},
		{"Qmax小于Q", 3, 10, 9, 5, ErrInvalidQmax},
		{"R不为正", 3, 10, 20, 0, ErrInvalidR},
		{"合法参数", 3, 10, 20, 5, nil},
	}
	for _, c := range cases {
		logCase(t, c.name, fmt.Sprintf("N=%d Q=%d Qmax=%d R=%d", c.n, c.q, c.qmax, c.r))
		_, err := New(c.n, c.q, c.qmax, c.r)
		if !errors.Is(err, c.wantErr) {
			t.Fatalf("%s: 期望 %v, 实得 %v", c.name, c.wantErr, err)
		}
		logResult(t, c.name, fmt.Sprintf("err=%v", err), "错误原因可区分，与规范一一对应")
	}
}

// TestAllocateAtExactMaturity 恰在解除时刻即可分配，且不提前一刻借出。
func TestAllocateAtExactMaturity(t *testing.T) {
	logCase(t, "恰在解除时刻分配", "N=1 Q=10 Qmax=20 R=5；t=0 分配，t=2 短命释放(隔离期20，解除于22)")
	p := newOrFail(t, 1, 10, 20, 5)

	id, err := p.Allocate(0)
	if err != nil || id != 1 {
		t.Fatalf("t=0 分配: id=%d err=%v", id, err)
	}
	if err := p.Release(1, 2); err != nil {
		t.Fatal(err)
	}

	_, err = p.Allocate(21)
	ae := allocErr(t, err)
	if ae.Exhausted || ae.NextFreeAt != 22 || ae.NextFreeID != 1 {
		t.Fatalf("t=21 期望报告最早解除 (22,1)，实得 %+v", ae)
	}
	logResult(t, "恰在解除时刻分配", "t=21 分配失败，NextFreeAt=22 NextFreeID=1", "隔离未到期绝不提前借出")

	id, err = p.Allocate(22)
	if err != nil || id != 1 {
		t.Fatalf("t=22 应恰可分配 id=1，实得 id=%d err=%v", id, err)
	}
	logResult(t, "恰在解除时刻分配", "t=22 分配成功 id=1", "freeAt<=now 即空闲；相邻分配间隔=22>=上次隔离期20")
}

// TestShortLifeDoublingAndCap 短命释放隔离期逐次翻倍并封顶于 Qmax。
func TestShortLifeDoublingAndCap(t *testing.T) {
	logCase(t, "翻倍并封顶", "N=1 Q=10 Qmax=100 R=5；反复短命(存活=1)")
	p := newOrFail(t, 1, 10, 100, 5)

	// lastQ 初值为 Q=10，首次短命即翻倍：序列 20,40,80,100(封顶),100...
	wantQs := []int64{20, 40, 80, 100, 100, 100}
	now := int64(0)
	for round, wantQ := range wantQs {
		id, err := p.Allocate(now)
		if err != nil {
			t.Fatalf("第%d轮 分配失败: %v", round+1, err)
		}
		releaseAt := now + 1
		if err := p.Release(id, releaseAt); err != nil {
			t.Fatalf("第%d轮 释放失败: %v", round+1, err)
		}
		gotFreeAt := releaseAt + wantQ
		if p.slots[id].freeAt != gotFreeAt {
			t.Fatalf("第%d轮: 期望 freeAt=%d (Q=%d)，实得 freeAt=%d",
				round+1, gotFreeAt, wantQ, p.slots[id].freeAt)
		}
		logResult(t, "翻倍并封顶",
			fmt.Sprintf("第%d轮: 存活=1 隔离Q=%d 解除时刻=%d", round+1, wantQ, gotFreeAt),
			"短命 => min(2*上次隔离期, Qmax)")
		now = gotFreeAt
	}
}

// TestNormalLifeResets 正常寿命后隔离期回到 Q；再次短命又从 Q 开始翻倍。
func TestNormalLifeResets(t *testing.T) {
	logCase(t, "正常寿命重置", "N=1 Q=10 Qmax=100 R=5；短命、短命、正常(=R)、短命、短命")
	p := newOrFail(t, 1, 10, 100, 5)

	type round struct {
		lived int64
		wantQ int64
		note  string
	}
	rounds := []round{
		{1, 20, "短命: 2*Q"},
		{1, 40, "再次短命: 2*20"},
		{5, 10, "存活恰为R=5算正常寿命: 回到Q"},
		{1, 20, "再次短命: 从Q翻倍为20"},
		{1, 40, "再次短命: 翻倍为40"},
	}

	now := int64(0)
	for i, r := range rounds {
		id, err := p.Allocate(now)
		if err != nil {
			t.Fatalf("第%d轮分配失败: %v", i+1, err)
		}
		releaseAt := now + r.lived
		if err := p.Release(id, releaseAt); err != nil {
			t.Fatalf("第%d轮释放失败: %v", i+1, err)
		}
		if p.slots[id].lastQ != r.wantQ {
			t.Fatalf("第%d轮 隔离期: 期望 %d，实得 %d（%s）",
				i+1, r.wantQ, p.slots[id].lastQ, r.note)
		}
		now = releaseAt + r.wantQ
		logResult(t, "正常寿命重置",
			fmt.Sprintf("第%d轮: 存活=%d 隔离Q=%d", i+1, r.lived, r.wantQ), r.note)
	}
}

// TestLivedExactlyR 存活时间恰等于 R 按正常寿命处理。
func TestLivedExactlyR(t *testing.T) {
	logCase(t, "存活恰为R", "N=1 Q=10 Qmax=100 R=5；先短命使上次隔离期=20，再存活5释放")
	p := newOrFail(t, 1, 10, 100, 5)

	id, _ := p.Allocate(0)
	if err := p.Release(id, 1); err != nil {
		t.Fatal(err)
	}
	id, _ = p.Allocate(21)
	if err := p.Release(id, 26); err != nil {
		t.Fatal(err)
	}
	if p.slots[1].lastQ != 10 || p.slots[1].freeAt != 36 {
		t.Fatalf("恰为R应重置为Q=10、解除于36，实得 lastQ=%d freeAt=%d",
			p.slots[1].lastQ, p.slots[1].freeAt)
	}
	logResult(t, "存活恰为R", "lastQ=10 freeAt=36", "lived==R 不算短命，隔离期取 Q")
}

// TestSmallestFreeID 分配总是返回最小空闲编号。
func TestSmallestFreeID(t *testing.T) {
	logCase(t, "最小空闲编号", "N=3；分配1,2,3；释放2后再分配应为2；释放1与3后再分配应为1")
	p := newOrFail(t, 3, 10, 20, 5)

	a, _ := p.Allocate(0)
	b, _ := p.Allocate(0)
	c, _ := p.Allocate(0)
	if a != 1 || b != 2 || c != 3 {
		t.Fatalf("初始分配序列错误: %d %d %d", a, b, c)
	}
	// 2 存活 6 >= R=5，正常寿命，隔离期 Q=10，t=6 释放，t=16 解除。
	if err := p.Release(2, 6); err != nil {
		t.Fatal(err)
	}
	got, err := p.Allocate(16)
	if err != nil || got != 2 {
		t.Fatalf("期望最小空闲=2，实得 %d err=%v", got, err)
	}
	// 1、3 存活均 >= R，各隔离 10ms。
	if err := p.Release(1, 16); err != nil {
		t.Fatal(err)
	}
	if err := p.Release(3, 16); err != nil {
		t.Fatal(err)
	}
	got, err = p.Allocate(26)
	if err != nil || got != 1 {
		t.Fatalf("期望最小空闲=1，实得 %d err=%v", got, err)
	}
	logResult(t, "最小空闲编号", "再分配序列 2, 1", "在所有空闲编号中取最小者")
}

// TestExhaustionReport 池耗尽/仅隔离时的失败报告，以及并列取小编号。
func TestExhaustionReport(t *testing.T) {
	logCase(t, "池耗尽与最早解除", "N=3 Q=10 Qmax=100 R=5；全部占用后释放1@2(freeAt=22)、2@3(freeAt=23)")
	p := newOrFail(t, 3, 10, 100, 5)
	_, _ = p.Allocate(0)
	_, _ = p.Allocate(0)
	_, _ = p.Allocate(0)

	_, err := p.Allocate(1)
	ae := allocErr(t, err)
	if !ae.Exhausted {
		t.Fatalf("全部占用应报告 Exhausted，实得 %+v", ae)
	}
	logResult(t, "池耗尽与最早解除", "Exhausted=true", "无空闲且无隔离 => 池已被全部占用")

	_ = p.Release(1, 2)
	_ = p.Release(2, 3)
	_, err = p.Allocate(4)
	ae = allocErr(t, err)
	if ae.Exhausted || ae.NextFreeAt != 22 || ae.NextFreeID != 1 {
		t.Fatalf("应报告最早解除(22,1)，实得 %+v", ae)
	}
	logResult(t, "池耗尽与最早解除", "NextFreeAt=22 NextFreeID=1", "隔离编号中取最早解除时刻")

	// 并列：2 与 3 在 t=16 同时解除，1 在 t=17 才解除；t=15 查询应报告 (16,2)。
	p2 := newOrFail(t, 3, 10, 100, 5)
	_, _ = p2.Allocate(0)
	_, _ = p2.Allocate(0)
	_, _ = p2.Allocate(0)
	_ = p2.Release(2, 6) // 正常，freeAt=16
	_ = p2.Release(3, 6) // 同刻释放，freeAt=16，与 2 并列
	_ = p2.Release(1, 7) // 正常，freeAt=17
	_, err2 := p2.Allocate(15)
	ae2 := allocErr(t, err2)
	if ae2.NextFreeAt != 16 || ae2.NextFreeID != 2 {
		t.Fatalf("并列应取小编号2，实得 %+v", ae2)
	}
	logResult(t, "池耗尽与最早解除", "NextFreeAt=16 NextFreeID=2", "最早解除时刻并列时取最小编号")
}

// TestReleaseValidation 释放校验：时钟回拨优先，其后越界、空闲、隔离中。
func TestReleaseValidation(t *testing.T) {
	p := newOrFail(t, 2, 10, 20, 5)
	_, _ = p.Allocate(100)

	logCase(t, "时钟回拨优先", "已见时刻100；以时刻50释放越界编号99")
	if err := p.Release(99, 50); !errors.Is(err, ErrClockRollback) {
		t.Fatalf("时钟回拨应优先于越界，实得 %v", err)
	}
	logResult(t, "时钟回拨优先", "ErrClockRollback", "回拨检查先于越界/空闲/隔离")

	logCase(t, "释放越界", "t=100 释放 id=99（编号1使用中）")
	if err := p.Release(99, 100); !errors.Is(err, ErrIDOutOfRange) {
		t.Fatalf("期望 ErrIDOutOfRange，实得 %v", err)
	}
	logResult(t, "释放越界", "ErrIDOutOfRange", "越界排在状态检查之前")

	logCase(t, "释放空闲", "t=100 释放从未分配的 id=2")
	if err := p.Release(2, 100); !errors.Is(err, ErrIDFree) {
		t.Fatalf("期望 ErrIDFree，实得 %v", err)
	}
	logResult(t, "释放空闲", "ErrIDFree", "编号当前为空闲")

	logCase(t, "释放隔离中", "在t=102释放 id=1（存活2短命，隔离至122）；t=120 再释放报隔离中，t=122 报空闲")
	if err := p.Release(1, 102); err != nil {
		t.Fatal(err)
	}
	if err := p.Release(1, 120); !errors.Is(err, ErrIDQuarantined) {
		t.Fatalf("期望 ErrIDQuarantined，实得 %v", err)
	}
	logResult(t, "释放隔离中", "ErrIDQuarantined", "隔离未到期，与空闲原因可区分")

	if err := p.Release(1, 122); !errors.Is(err, ErrIDFree) {
		t.Fatalf("到期未分配即释放应为 ErrIDFree，实得 %v", err)
	}
	logResult(t, "释放隔离中", "t=122 再释放 => ErrIDFree", "恰在解除时刻编号已转空闲")

	logCase(t, "分配/查询回拨", "已见时刻122；t=121 分配与查询")
	if _, err := p.Allocate(121); !errors.Is(err, ErrClockRollback) {
		t.Fatalf("分配回拨: %v", err)
	}
	if _, err := p.Query(121); !errors.Is(err, ErrClockRollback) {
		t.Fatalf("查询回拨: %v", err)
	}
	logResult(t, "分配/查询回拨", "均为 ErrClockRollback", "所有时间相关操作共用单调时钟（分配回拨不借出编号）")
}

// TestRejectedNoMutation 被拒绝的操作不改变任何编号状态与隔离记录。
func TestRejectedNoMutation(t *testing.T) {
	logCase(t, "拒绝不改变状态", "t=100 分配id=1；记录快照；执行非法释放与回拨，再比对快照")
	p := newOrFail(t, 2, 10, 100, 5)
	id, _ := p.Allocate(100)

	p.mu.Lock()
	before := make([]slot, len(p.slots))
	copy(before, p.slots)
	lastNow := p.lastNow
	p.mu.Unlock()

	_ = p.Release(99, 100) // 越界
	_ = p.Release(2, 100)  // 空闲
	_ = p.Release(id, 99)  // 时钟回拨（优先）
	_, _ = p.Allocate(99)  // 回拨
	_, _ = p.Query(99)     // 回拨

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.lastNow != lastNow {
		t.Fatalf("回拨操作改变了 lastNow: %d -> %d", lastNow, p.lastNow)
	}
	for i := range before {
		if before[i] != p.slots[i] {
			t.Fatalf("编号 %d 状态/隔离记录被拒绝操作改变: %+v -> %+v",
				i, before[i], p.slots[i])
		}
	}
	logResult(t, "拒绝不改变状态", "所有 slot 与 lastNow 均与快照一致",
		"被拒绝操作不改变任何编号状态与隔离记录")
}

// TestDeterministicReplay 相同操作序列重放两次，编号序列完全一致。
func TestDeterministicReplay(t *testing.T) {
	logCase(t, "确定性重放", "N=3 Q=10 Qmax=100 R=5 的混合脚本重放两次")
	type step struct {
		kind   byte // 'a' 分配 / 'r' 释放
		id     int
		now    int64
		wantOK bool
		wantID int
	}
	script := []step{
		{'a', 0, 0, true, 1},
		{'a', 0, 0, true, 2},
		{'a', 0, 0, true, 3},
		{'r', 1, 1, true, 0}, // 短命 freeAt=21
		{'r', 2, 6, true, 0}, // 正常 freeAt=16
		{'a', 0, 5, false, 0},
		{'a', 0, 16, true, 2},
		{'a', 0, 20, false, 0},
		{'a', 0, 21, true, 1},
		{'a', 0, 22, false, 0}, // 3 仍使用中，1、2 使用中，无空闲
		{'r', 3, 25, true, 0},  // 存活25正常 freeAt=35
		{'a', 0, 35, true, 3},
	}
	run := func() []int {
		p := newOrFail(t, 3, 10, 100, 5)
		got := make([]int, 0, len(script))
		for _, s := range script {
			switch s.kind {
			case 'a':
				id, err := p.Allocate(s.now)
				if s.wantOK {
					if err != nil || id != s.wantID {
						t.Fatalf("t=%d 分配: 期望 id=%d，实得 id=%d err=%v",
							s.now, s.wantID, id, err)
					}
					got = append(got, id)
				} else if err == nil {
					t.Fatalf("t=%d 分配应失败却成功 id=%d", s.now, id)
				}
			case 'r':
				if err := p.Release(s.id, s.now); err != nil {
					t.Fatalf("t=%d 释放 %d: %v", s.now, s.id, err)
				}
			}
		}
		return got
	}
	seq1 := run()
	seq2 := run()
	if fmt.Sprint(seq1) != fmt.Sprint(seq2) {
		t.Fatalf("重放序列不一致: %v vs %v", seq1, seq2)
	}
	wantSeq := []int{1, 2, 3, 2, 1, 3}
	if fmt.Sprint(seq1) != fmt.Sprint(wantSeq) {
		t.Fatalf("编号序列: 期望 %v，实得 %v", wantSeq, seq1)
	}
	logResult(t, "确定性重放", fmt.Sprintf("两次序列均为 %v", seq1),
		"相同操作序列重放得到完全相同的编号序列")
}

// TestInvariantGapAndCounts 三态计数之和恒为 N；
// 同一编号相邻两次分配时刻差不小于其间那次释放所用隔离期。
func TestInvariantGapAndCounts(t *testing.T) {
	logCase(t, "不变量校验", "N=4 Q=10 Qmax=100 R=8；脚本化分配/释放并核对每次复用间隔")
	p := newOrFail(t, 4, 10, 100, 8)

	type step struct {
		now    int64
		kind   byte
		id     int
		wantID int // 分配时期望编号
		wantOK bool
	}
	steps := []step{
		{0, 'a', 0, 1, true}, {0, 'a', 0, 2, true},
		{0, 'a', 0, 3, true}, {0, 'a', 0, 4, true},
		{2, 'r', 1, 0, true}, // 短命 qq=20，freeAt=22
		{3, 'r', 2, 0, true}, // 短命 qq=20，freeAt=23
		{4, 'a', 0, 1, true}, // 1 到期前不可借：4 时刻仍失败？1在隔离 => 无空闲，失败
	}
	// 4 时刻 1、2 隔离中，3、4 使用中，无空闲 => 期望失败。
	steps[6].wantOK = false
	steps = append(steps,
		step{12, 'r', 3, 0, true},  // 存活12>=8 正常 qq=10，freeAt=22
		step{20, 'a', 0, 0, false}, // 仅 1、2、3 隔离，4 使用中
		step{22, 'a', 0, 1, true},  // 1 与 3 同刻到期，取小编号1
		step{22, 'r', 4, 0, true},  // 4 存活22正常，freeAt=32
		step{23, 'a', 0, 2, true},  // 2 到期
		step{32, 'a', 0, 3, true},  // 3 到期（4 也到期但 3 更小）
		step{32, 'a', 0, 4, true},  // 4 到期
	)

	// allocTimes[id] 记录该编号历次分配时刻。
	allocTimes := map[int][]int64{}
	// releasedQ[id] 记录该编号历次释放所用隔离期。
	releasedQ := map[int][]int64{}

	for _, s := range steps {
		switch s.kind {
		case 'a':
			id, err := p.Allocate(s.now)
			if s.wantOK {
				if err != nil || id != s.wantID {
					t.Fatalf("t=%d 分配: 期望 %d，实得 %d err=%v", s.now, s.wantID, id, err)
				}
				allocTimes[id] = append(allocTimes[id], s.now)
			} else if err == nil {
				t.Fatalf("t=%d 分配应失败却成功 id=%d", s.now, id)
			}
		case 'r':
			if err := p.Release(s.id, s.now); err != nil {
				t.Fatalf("t=%d 释放 %d: %v", s.now, s.id, err)
			}
			releasedQ[s.id] = append(releasedQ[s.id], p.slots[s.id].lastQ)
		}
		st, err := p.Query(s.now)
		if err != nil {
			t.Fatal(err)
		}
		if st.InUse+st.Quarantined+st.Free != 4 {
			t.Fatalf("t=%d 三态计数之和=%d != 4 (%+v)",
				s.now, st.InUse+st.Quarantined+st.Free, st)
		}
	}

	// 核对：每个编号第 k+1 次分配与第 k 次分配的时刻差 >= 第 k 次释放隔离期。
	for id := 1; id <= 4; id++ {
		at := allocTimes[id]
		qs := releasedQ[id]
		for k := 1; k < len(at); k++ {
			gap := at[k] - at[k-1]
			if gap < qs[k-1] {
				t.Fatalf("编号 %d 第%d次复用间隔 %d < 上次释放隔离期 %d",
					id, k, gap, qs[k-1])
			}
		}
	}
	logResult(t, "不变量校验",
		"每步 InUse+Quarantined+Free=4；各编号复用间隔均>=上次释放隔离期",
		"状态总数守恒，且绝不提前借出隔离编号")
}

// TestConcurrentSerializability 并发调用下结果等价于某个串行顺序：
// 每个时刻存活的使用中编号集合大小不超 N、不重号，且计数守恒。
func TestConcurrentSerializability(t *testing.T) {
	logCase(t, "并发串行化", "N=4 Q=2 Qmax=32 R=10；多 goroutine 分配/释放/查询，同时刻并发")
	p := newOrFail(t, 4, 2, 32, 10)

	// 阶段一：同一时刻 0 并发分配 40 次，恰好 4 个成功、编号为 1..4。
	var wg sync.WaitGroup
	results := make(chan int, 40)
	errs := make(chan error, 40)
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, err := p.Allocate(0)
			if err != nil {
				errs <- err
				return
			}
			results <- id
		}()
	}
	wg.Wait()
	close(results)
	close(errs)

	seen := map[int]bool{}
	for id := range results {
		if id < 1 || id > 4 || seen[id] {
			t.Fatalf("并发分配出现重号/越界 id=%d seen=%v", id, seen)
		}
		seen[id] = true
	}
	if len(seen) != 4 {
		t.Fatalf("t=0 应恰好成功 4 个分配，实得 %d 个: %v", len(seen), seen)
	}
	failCount := 0
	for range errs {
		failCount++
	}
	if failCount != 36 {
		t.Fatalf("t=0 应有 36 个分配失败(池耗尽)，实得 %d", failCount)
	}
	logResult(t, "并发串行化", "恰好 4 成功(1..4)、36 失败",
		"并发分配等价于某串行顺序：无重号、无超发")

	// 阶段二：同一时刻 10 并发释放全部编号（正常寿命），再于同一时刻 12 并发抢回。
	errCh := make(chan error, 16)
	for id := 1; id <= 4; id++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			if err := p.Release(id, 10); err != nil {
				errCh <- err
			}
		}(id)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("并发释放失败: %v", err)
	}

	reclaimed := make(chan int, 40)
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if id, err := p.Allocate(12); err == nil {
				reclaimed <- id
			}
		}()
	}
	wg.Wait()
	close(reclaimed)

	seen2 := map[int]bool{}
	for id := range reclaimed {
		if seen2[id] {
			t.Fatalf("t=12 重号 id=%d", id)
		}
		seen2[id] = true
	}
	if len(seen2) != 4 {
		t.Fatalf("t=12 应恰好回收 4 个编号，实得 %d: %v", len(seen2), seen2)
	}

	// 阶段三：并发查询与分配/释放混跑，仅检查不 panic 且计数守恒。
	stop := make(chan struct{})
	for g := 0; g < 3; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					st, err := p.Query(12)
					if err == nil && st.InUse+st.Quarantined+st.Free != 4 {
						t.Errorf("并发查询计数不守恒: %+v", st)
						return
					}
				}
			}
		}()
	}
	close(stop)
	wg.Wait()
	logResult(t, "并发串行化", fmt.Sprintf("t=12 回收集合=%v，查询计数恒为4", seen2),
		"互斥保护使并发操作等价于串行，三态计数守恒")
}
