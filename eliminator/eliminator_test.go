package eliminator

import (
	"errors"
	"fmt"
	"testing"
)

func reasonOf(err error) RejectReason {
	var re *RejectError
	if errors.As(err, &re) {
		return re.Reason
	}
	return ""
}

func mustNext(t *testing.T, e *Eliminator) NextResult {
	t.Helper()
	r, err := e.Next()
	if err != nil {
		t.Fatalf("Next 被拒: %v", err)
	}
	return r
}

func aliveIDs(s Snapshot) []int {
	var ids []int
	for _, a := range s.Arms {
		if !a.Eliminated {
			ids = append(ids, a.ID)
		}
	}
	return ids
}

// sR 并列时取编号小者：首轮按 0,1,2,3 循环两轮。
func TestTiePickLowestID(t *testing.T) {
	e, _ := New(4, 2, 1_000_000_000)
	var got []int
	for i := 0; i < 8; i++ {
		got = append(got, mustNext(t, e).Arm)
	}
	want := []int{0, 1, 2, 3, 0, 1, 2, 3}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("分流顺序=%v, 期望%v", got, want)
	}
}

// 护栏恰在 sT==G 且 cT==0 时触发；在 sT==G-1 拿到点击后永不触发。
func TestGuardExactG(t *testing.T) {
	// b=100, G=3：前三次都给创意 0（1 拿到曝光立即点击以免收轮）。
	e, _ := New(2, 100, 3)
	mustNext(t, e) // 0: sT=1
	mustNext(t, e) // 1: sT=1
	if err := e.Click(1); err != nil {
		t.Fatal(err)
	}
	mustNext(t, e) // 0: sT=2
	mustNext(t, e) // sR 1<2，给 1
	r := mustNext(t, e)
	if r.Arm != 0 {
		t.Fatalf("第5次应给0（sR并列），得%d", r.Arm)
	}
	if len(r.Eliminated) != 1 || r.Eliminated[0].Reason != "guard" || r.Winner != 1 {
		t.Fatalf("sT=G 护栏应触发并由1胜出: %+v", r)
	}

	// sT==G-1 时拿到一次点击，之后即便零点击条件不再成立，护栏不触发。
	e2, _ := New(2, 2, 3)
	mustNext(t, e2) // 0 sT=1
	mustNext(t, e2) // 1 sT=1
	mustNext(t, e2) // 0 sT=2 = G-1
	if err := e2.Click(0); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		r := mustNext(t, e2)
		if r.Arm == 1 && e2.Snapshot().Arms[1].Clicks < e2.Snapshot().Arms[1].Exposures {
			_ = e2.Click(1)
		}
		if r.Winner != -1 {
			break
		}
	}
	a0 := e2.Snapshot().Arms[0]
	if a0.Eliminated && a0.ElimReason == "guard" {
		t.Fatalf("G-1 处已有点击，护栏不应触发: %+v", a0)
	}
}

// 护栏淘汰使“最后一个未满配额”的创意消失：其余活跃创意均已打满配额，
// 于是同一次 Next 立即收轮并淘汰轮次末位、产生胜出者。
//
// 构造：n=3,b=2,G=4。首轮 0、1 每次曝光即点击、2 零点击，收轮保留 0、1；
// 第 2 轮 q=4。0、1 各分流 3 次（sR=3）后 Join(9)：9 以 sR=0 补配额，
// 连续 3 次曝光追平到 sR=3；随后 0、1 各再得 1 次打满 sR=4，9 仍为 3；
// 下一次只能给 9，其 sT 从 3 变为 4=G 且 cT=0 -> 护栏淘汰 9；
// 此时 0、1 均 sR=4 -> 立即收轮，点击率相等保留编号 0，1 被轮次淘汰。
func TestGuardLeadsImmediateClose(t *testing.T) {
	e, _ := New(3, 2, 4)
	for i := 0; i < 6; i++ {
		r := mustNext(t, e)
		if r.Arm != 2 {
			if err := e.Click(r.Arm); err != nil {
				t.Fatal(err)
			}
		}
	}
	s := e.Snapshot()
	if s.Round != 2 || fmt.Sprint(aliveIDs(s)) != "[0 1]" {
		t.Fatalf("前置: round=%d alive=%v", s.Round, aliveIDs(s))
	}
	for i := 0; i < 6; i++ { // 0,1 各到 sR=3
		r := mustNext(t, e)
		if err := e.Click(r.Arm); err != nil { // 保持 cT>0，护栏不触发
			t.Fatal(err)
		}
	}
	if err := e.Join(9); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		r := mustNext(t, e)
		if r.Arm != 9 {
			t.Fatalf("新创意应被优先分流, 得%d", r.Arm)
		}
	}
	mustNext(t, e) // 0 sR=4
	mustNext(t, e) // 1 sR=4
	r := mustNext(t, e)
	if r.Arm != 9 || r.Winner != 0 || !r.RoundClosed {
		t.Fatalf("最后一次Next: %+v", r)
	}
	reasons := map[[2]int]string{}
	for _, ev := range r.Eliminated {
		reasons[[2]int{ev.Arm, ev.Round}] = ev.Reason
	}
	if reasons[[2]int{9, 2}] != "guard" || reasons[[2]int{1, 2}] != "round" {
		t.Fatalf("淘汰事件异常: %+v", r.Eliminated)
	}
}

// 护栏淘汰后只剩一个活跃创意：立即结束，该创意胜出（不再走收轮）。
func TestGuardLeavesOneWinner(t *testing.T) {
	// n=2,b=100,G=2，每次给 1 补点击，0 保持零点击。
	e, _ := New(2, 100, 2)
	mustNext(t, e) // 0 sT=1
	mustNext(t, e) // 1 sT=1
	if err := e.Click(1); err != nil {
		t.Fatal(err)
	}
	r := mustNext(t, e) // sR：0=1<1=1，给0 -> sT=2=G
	if r.Arm != 0 || len(r.Eliminated) != 1 ||
		r.Eliminated[0].Reason != "guard" || r.Winner != 1 || r.RoundClosed {
		t.Fatalf("护栏后应直接由1胜出且不收轮: %+v", r)
	}
	if !e.Snapshot().Finished {
		t.Fatal("应已结束")
	}
}

// 活跃数为奇数时保留 ceil(a/2)。
func TestOddActiveKeepCeil(t *testing.T) {
	e, _ := New(5, 1, 1_000_000_000)
	var r NextResult
	for i := 0; i < 5; i++ {
		r = mustNext(t, e)
	}
	if !r.RoundClosed || len(r.Eliminated) != 2 {
		t.Fatalf("5 选 ceil(5/2)=3，应淘汰2个: %+v", r)
	}
	if got := fmt.Sprint(aliveIDs(e.Snapshot())); got != "[0 1 2]" {
		t.Fatalf("存活=%s, 期望[0 1 2]", got)
	}
}

// 点击率相等时按编号小者在前。
func TestEqualRateTieByID(t *testing.T) {
	e, _ := New(4, 1, 1_000_000_000)
	for i := 0; i < 3; i++ {
		mustNext(t, e)
	}
	if err := e.Click(0); err != nil { // 0=1/1
		t.Fatal(err)
	}
	if err := e.Click(1); err != nil { // 1=1/1
		t.Fatal(err)
	}
	r := mustNext(t, e) // 第4次收轮
	if !r.RoundClosed {
		t.Fatalf("应收轮: %+v", r)
	}
	if got := fmt.Sprint(aliveIDs(e.Snapshot())); got != "[0 1]" {
		t.Fatalf("点击率相等应按编号保留0,1, 实际%s", got)
	}
}

// 题给完整示例：中途加入者分母不同，用交叉相乘比较点击率
// （3/4 vs 4/6：3*6=18 > 4*4=16，故 4 排在 0 前）。
func TestWorkedExampleWithJoin(t *testing.T) {
	e, _ := New(4, 2, 3)
	for i := 0; i < 4; i++ {
		mustNext(t, e)
	}
	if err := e.Click(0); err != nil {
		t.Fatal(err)
	}
	if err := e.Click(1); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		mustNext(t, e)
	}
	if err := e.Click(0); err != nil {
		t.Fatal(err)
	}
	if err := e.Join(4); err != nil {
		t.Fatal(err)
	}
	// 第2轮顺序 0,1,4 循环；4 的前三次曝光各点击一次（护栏因此不触发），
	// 0、1 在收轮前补到 0:4/6、1:3/6、4:3/4。
	var last NextResult
	for step := 1; step <= 12; step++ {
		// 在第12次曝光（收轮）前补完 0、1 的点击：
		if step == 10 {
			_ = e.Click(0) // 0 第3次曝光后：3/5 时点
		}
		if step == 11 {
			_ = e.Click(1) // 1 第3次曝光后
		}
		r := mustNext(t, e)
		switch {
		case r.Arm == 4 && step <= 9:
			if err := e.Click(4); err != nil {
				t.Fatal(err)
			}
		case r.Arm == 0 && (step == 4 || step == 7):
			if err := e.Click(0); err != nil {
				t.Fatal(err)
			}
		case r.Arm == 1 && (step == 5 || step == 8):
			if err := e.Click(1); err != nil {
				t.Fatal(err)
			}
		}
		last = r
	}
	if !last.RoundClosed || last.Winner != -1 {
		t.Fatalf("第2轮应收轮但不结束（仍有2个存活）: %+v", last)
	}
	if len(last.Eliminated) != 1 || last.Eliminated[0].Arm != 1 ||
		last.Eliminated[0].Reason != "round" {
		t.Fatalf("应仅以轮次原因淘汰1: %+v", last.Eliminated)
	}
	s := e.Snapshot()
	if s.Round != 3 || fmt.Sprint(aliveIDs(s)) != "[0 4]" {
		t.Fatalf("收轮后应进入第3轮且存活4,0: round=%d alive=%v", s.Round, aliveIDs(s))
	}
}

// 已淘汰创意的迟到点击只记账，不改变淘汰结果；cT 不能超过 sT。
func TestLateClickOnEliminated(t *testing.T) {
	e, _ := New(2, 1, 1_000_000_000)
	mustNext(t, e)
	r0 := mustNext(t, e) // 第 2 次曝光即收轮（零点击率相等，编号0存活）
	if err := e.Click(0); err != nil {
		t.Fatal(err)
	}
	r := r0
	if r.Winner != 0 || !e.Snapshot().Finished {
		t.Fatalf("0 应胜出: %+v", r)
	}
	if err := e.Click(1); err != nil {
		t.Fatalf("已淘汰创意在 sT>cT 时迟到点击应成功: %v", err)
	}
	s := e.Snapshot()
	if s.Arms[1].Clicks != 1 || !s.Arms[1].Eliminated || s.Winner != 0 {
		t.Fatalf("迟到点击只能记账: %+v", s.Arms[1])
	}
	if err := e.Click(1); reasonOf(err) != ReasonClickNoExposure {
		t.Fatalf("cT==sT 后再点应被拒: %v", err)
	}
}

// 点击先于曝光被拒；未登记编号报创意不存在。
func TestClickBeforeExposure(t *testing.T) {
	e, _ := New(2, 1, 10)
	if err := e.Click(0); reasonOf(err) != ReasonClickNoExposure {
		t.Fatalf("无曝光点击: %v", err)
	}
	if err := e.Click(99); reasonOf(err) != ReasonArmNotFound {
		t.Fatalf("未登记: %v", err)
	}
	s := e.Snapshot()
	if s.TotalNext != 0 || s.Arms[0].Clicks != 0 {
		t.Fatal("被拒操作不得改变状态")
	}
}

// 配额 q_r = b*2^min(r-1,20)：翻倍并在第 21 轮封顶。
func TestQuotaDoublingAndCap(t *testing.T) {
	e, _ := New(2, 3, 1_000_000_000)
	cases := []struct {
		r    int
		want int64
	}{
		{1, 3}, {2, 6}, {3, 12}, {4, 24}, {21, 3 << 20}, {22, 3 << 20},
	}
	for _, c := range cases {
		e.round = c.r
		if q := e.quota(); q != c.want {
			t.Fatalf("r=%d q=%d, 期望%d", c.r, q, c.want)
		}
	}
}

// 结束后 Next 与 Join 均以“已结束”拒绝；Click 仍可记账。
func TestRejectsAfterFinished(t *testing.T) {
	e, _ := New(2, 1, 1_000_000_000)
	mustNext(t, e)
	if err := e.Click(0); err != nil {
		t.Fatal(err)
	}
	r := mustNext(t, e) // 第 2 次曝光打满 -> 收轮：0=1/1 胜出
	if r.Winner != 0 {
		t.Fatalf("0 应胜出: %+v", r)
	}
	if _, err := e.Next(); reasonOf(err) != ReasonFinished {
		t.Fatalf("结束后 Next: %v", err)
	}
	if err := e.Join(7); reasonOf(err) != ReasonFinished {
		t.Fatalf("结束后 Join: %v", err)
	}
	if err := e.Click(1); err != nil {
		t.Fatalf("结束后 Click 仍应记账: %v", err)
	}
}

// 总数达 64 后 Join 新编号报“创意已满”，已登记编号（含淘汰者）仍报“已存在”。
func TestJoinCapacityAndExists(t *testing.T) {
	e, _ := New(64, 1, 1_000_000_000)
	if err := e.Join(900); reasonOf(err) != ReasonFull {
		t.Fatalf("满员新编号: %v", err)
	}
	if err := e.Join(0); reasonOf(err) != ReasonArmAlreadyExists {
		t.Fatalf("已登记编号: %v", err)
	}
	// 63 初始 + Join 1 个新编号 -> 恰好满。
	e2, _ := New(63, 1, 1_000_000_000)
	if err := e2.Join(1000); err != nil {
		t.Fatalf("第64个创意应允许: %v", err)
	}
	if err := e2.Join(999); reasonOf(err) != ReasonFull {
		t.Fatalf("第65个应报满: %v", err)
	}
	if err := e2.Join(1000); reasonOf(err) != ReasonArmAlreadyExists {
		t.Fatalf("新登记编号重复应报已存在: %v", err)
	}
	// 淘汰掉一些后仍不能 Join：总数按“登记过”计，不因淘汰而减少。
	for i := 0; i < 64; i++ {
		mustNext(t, e2)
	}
	if err := e2.Join(999); reasonOf(err) != ReasonFull {
		t.Fatalf("有创意被淘汰后总数仍为64, Join 应报满: %v", err)
	}
}

// 各类拒绝的优先级与参数边界。
func TestRejectPriorityAndBounds(t *testing.T) {
	// 构造参数边界。
	for _, c := range []struct {
		n    int
		b, G int64
	}{
		{1, 1, 1}, {65, 1, 1}, {2, 0, 1}, {2, 1_000_001, 1}, {2, 1, 0}, {2, 1, 1_000_000_001},
	} {
		if _, err := New(c.n, c.b, c.G); reasonOf(err) != ReasonInvalidArgs {
			t.Fatalf("New(%d,%d,%d)=%v", c.n, c.b, c.G, err)
		}
	}
	e, _ := New(2, 1, 10)
	// Join: 参数非法优先于一切。
	if err := e.Join(-1); reasonOf(err) != ReasonInvalidArgs {
		t.Fatalf("Join(-1): %v", err)
	}
	if err := e.Join(1001); reasonOf(err) != ReasonInvalidArgs {
		t.Fatalf("Join(1001): %v", err)
	}
	// Click 不存在优先于“无曝光”：未登记编号报不存在。
	if err := e.Click(123); reasonOf(err) != ReasonArmNotFound {
		t.Fatalf("Click(123): %v", err)
	}
	// 结束后：Next/Join 报已结束；Click 未登记仍报不存在。
	finishFast(t, e)
	if _, err := e.Next(); reasonOf(err) != ReasonFinished {
		t.Fatalf("结束 Next: %v", err)
	}
	if err := e.Join(-1); reasonOf(err) != ReasonInvalidArgs {
		t.Fatalf("结束后 Join 非法参数仍应先报非法: %v", err)
	}
	if err := e.Join(5); reasonOf(err) != ReasonFinished {
		t.Fatalf("结束 Join: %v", err)
	}
	if err := e.Click(5); reasonOf(err) != ReasonArmNotFound {
		t.Fatalf("结束后 Click 未登记: %v", err)
	}
}

func finishFast(t *testing.T, e *Eliminator) {
	t.Helper()
	// b=1,G 大：每轮零点击靠编号保留，直到 1 个胜出。
	for i := 0; i < 100; i++ {
		r := mustNext(t, e)
		if r.Winner != -1 {
			return
		}
	}
	t.Fatal("未能结束")
}

// 不变量：cT<=sT、活跃 sR<=q_r、sT 总和==成功 Next 次数。
func TestInvariants(t *testing.T) {
	e, _ := New(6, 3, 8)
	for i := 0; i < 500; i++ {
		r, err := e.Next()
		if err != nil {
			break
		}
		if (i*7+3)%5 == 0 {
			_ = e.Click(r.Arm)
		}
		if (i*3+1)%11 == 0 {
			_ = e.Join(100 + (i % 50))
		}
		s := e.Snapshot()
		var sumST int64
		for _, a := range s.Arms {
			if a.Clicks > a.Exposures {
				t.Fatalf("cT>sT: %+v", a)
			}
			sumST += a.Exposures
			if q := e.snapshotQuota(); !a.Eliminated && a.RoundExp > q {
				t.Fatalf("活跃 sR>q: %+v q=%d", a, q)
			}
		}
		if sumST != s.TotalNext {
			t.Fatalf("曝光总和%d != Next次数%d", sumST, s.TotalNext)
		}
	}
}

func (e *Eliminator) snapshotQuota() int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.quota()
}
