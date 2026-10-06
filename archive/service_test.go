package archive_test

import (
	"reflect"
	"testing"

	"ontology/archive"
)

func testCfg() archive.Config {
	return archive.Config{
		LoanDays:         [4]int{5, 5, 5, 5},
		PickupDays:       2,
		RenewWindowDays:  3,
		MaxRenews:        1,
		OverdueThreshold: 10,
		CooldownDays:     5,
	}
}

func newSvc(t *testing.T, cfg archive.Config) *archive.Service {
	t.Helper()
	s, err := archive.New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func mustOK(t *testing.T, res archive.Result, what string) {
	t.Helper()
	if res.Err != nil {
		t.Fatalf("%s: 期望成功，实际被拒绝: %v", what, res.Err)
	}
}

func mustErr(t *testing.T, res archive.Result, code archive.ErrCode, what string) *archive.Error {
	t.Helper()
	if res.Err == nil {
		t.Fatalf("%s: 期望被拒绝(%s)，实际成功", what, code)
	}
	if res.Err.Code != code {
		t.Fatalf("%s: 期望错误类别 %s，实际 %s (%v)", what, code, res.Err.Code, res.Err)
	}
	return res.Err
}

// 基础场景：卷 v1(公开)，借阅人 b1(绝密)、b2(绝密)、b3(绝密)。
func basicSvc(t *testing.T) *archive.Service {
	t.Helper()
	s := newSvc(t, testCfg())
	mustOK(t, s.AddVolume(0, "v1", archive.Public), "add v1")
	mustOK(t, s.AddBorrower(0, "b1", archive.TopSecret), "add b1")
	mustOK(t, s.AddBorrower(0, "b2", archive.TopSecret), "add b2")
	mustOK(t, s.AddBorrower(0, "b3", archive.TopSecret), "add b3")
	return s
}

// 借期最后一日恰等归还日不逾期，晚一日逾期一天。
func TestLoanDueBoundary(t *testing.T) {
	s := basicSvc(t)
	res := s.Borrow(10, "b1", "v1")
	mustOK(t, res, "borrow")
	if res.BorrowResults[0].DueDay != 15 {
		t.Fatalf("借期最后一日应为 15，实际 %d", res.BorrowResults[0].DueDay)
	}
	res = s.Return(15, "v1")
	mustOK(t, res, "最后一日归还")
	if res.OverdueDays != 0 {
		t.Fatalf("最后一日归还不应逾期，实际逾期 %d 天", res.OverdueDays)
	}
	mustOK(t, s.Borrow(20, "b1", "v1"), "再次借阅")
	res = s.Return(26, "v1")
	mustOK(t, res, "晚一日归还")
	if res.OverdueDays != 1 {
		t.Fatalf("晚一日应逾期 1 天，实际 %d", res.OverdueDays)
	}
}

// 时钟：now 不得小于上一次被接受操作的 now；被拒绝操作不推进水位。
func TestClockWatermark(t *testing.T) {
	s := basicSvc(t)
	mustOK(t, s.Borrow(10, "b1", "v1"), "day10 borrow")
	mustErr(t, s.Return(9, "v1"), archive.ErrClockRollback, "时钟回退")
	// 被拒绝的 op（day 20 归还不存在的卷）不推进水位，day 15 仍被接受。
	mustErr(t, s.Return(20, "ghost"), archive.ErrNotFound, "拒绝不推进水位")
	mustOK(t, s.Return(15, "v1"), "day15 归还")
	// now 恰等于水位允许。
	mustOK(t, s.Borrow(15, "b2", "v1"), "day15 恰等水位")
}

// 续借窗口：起点恰等允许，起点前一日拒绝，最后一日允许，逾期后拒绝。
func TestRenewWindowBoundary(t *testing.T) {
	// 借期 5，day10 借出，最后一日 15，窗口 [13,15]。
	s := basicSvc(t)
	mustOK(t, s.Borrow(10, "b1", "v1"), "borrow")
	mustErr(t, s.Renew(12, "b1", "v1", ""), archive.ErrRenew, "窗口前一日")
	res := s.Renew(13, "b1", "v1", "")
	mustOK(t, res, "窗口起点恰等")
	if res.NewDueDay != 21 {
		t.Fatalf("续借后新借期最后一日应为 21，实际 %d", res.NewDueDay)
	}

	s2 := basicSvc(t)
	mustOK(t, s2.Borrow(10, "b1", "v1"), "borrow")
	res = s2.Renew(15, "b1", "v1", "")
	mustOK(t, res, "借期最后一日续借")
	if res.NewDueDay != 21 {
		t.Fatalf("新借期最后一日应为 21，实际 %d", res.NewDueDay)
	}

	s3 := basicSvc(t)
	mustOK(t, s3.Borrow(10, "b1", "v1"), "borrow")
	mustErr(t, s3.Renew(16, "b1", "v1", ""), archive.ErrRenew, "逾期后续借")
}

// 续借后新借期自原借期最后一日的下一日起算，与申请日无关。
func TestRenewNewDueStartsAfterOldDue(t *testing.T) {
	for _, renewDay := range []int{13, 14, 15} {
		s := basicSvc(t)
		mustOK(t, s.Borrow(10, "b1", "v1"), "borrow")
		res := s.Renew(renewDay, "b1", "v1", "")
		mustOK(t, res, "续借")
		if res.NewDueDay != 21 { // 15 + 1 + 5
			t.Fatalf("申请日 %d: 新借期最后一日应为 21，实际 %d", renewDay, res.NewDueDay)
		}
	}
}

// 续借次数上限。
func TestRenewLimit(t *testing.T) {
	s := basicSvc(t)
	mustOK(t, s.Borrow(10, "b1", "v1"), "borrow")
	mustOK(t, s.Renew(13, "b1", "v1", ""), "第一次续借")
	mustErr(t, s.Renew(19, "b1", "v1", ""), archive.ErrRenew, "超过续借上限")
}

// 机密及以上续借需审批人，且审批人不得是本人；低密级无需审批。
func TestRenewApproval(t *testing.T) {
	s := newSvc(t, testCfg())
	mustOK(t, s.AddVolume(0, "vc", archive.Confidential), "add vc")
	mustOK(t, s.AddVolume(0, "vp", archive.Public), "add vp")
	mustOK(t, s.AddBorrower(0, "b1", archive.TopSecret), "add b1")
	mustOK(t, s.AddBorrower(0, "b2", archive.TopSecret), "add b2")
	mustOK(t, s.Borrow(0, "b1", "vc", "vp"), "borrow")
	mustErr(t, s.Renew(3, "b1", "vc", ""), archive.ErrInvalidParam, "机密缺审批人")
	mustErr(t, s.Renew(3, "b1", "vc", "b1"), archive.ErrInvalidParam, "审批人是本人")
	mustErr(t, s.Renew(3, "b1", "vc", "ghost"), archive.ErrNotFound, "审批人不存在")
	mustOK(t, s.Renew(3, "b1", "vc", "b2"), "审批通过")
	mustOK(t, s.Renew(3, "b1", "vp", ""), "公开卷无需审批")
}

// 卷上存在有效预约时不得续借。
func TestRenewBlockedByReservation(t *testing.T) {
	s := basicSvc(t)
	mustOK(t, s.Borrow(10, "b1", "v1"), "borrow")
	mustOK(t, s.Reserve(11, "b2", "v1"), "预约")
	mustErr(t, s.Renew(13, "b1", "v1", ""), archive.ErrReservation, "有预约不得续借")
}

// 队首资格不满足时被跳过但保留序位，恢复资格后仍按原序位获得分配。
func TestQueueHeadSkippedKeepsPosition(t *testing.T) {
	s := newSvc(t, testCfg())
	mustOK(t, s.AddVolume(0, "v1", archive.Internal), "add v1")
	mustOK(t, s.AddBorrower(0, "b1", archive.TopSecret), "add b1")
	mustOK(t, s.AddBorrower(0, "b2", archive.Internal), "add b2")
	mustOK(t, s.AddBorrower(0, "b3", archive.Internal), "add b3")
	mustOK(t, s.AddBorrower(0, "b4", archive.Internal), "add b4")
	mustOK(t, s.Borrow(0, "b1", "v1"), "b1 borrow")
	mustOK(t, s.Reserve(1, "b2", "v1"), "b2 reserve")
	mustOK(t, s.Reserve(2, "b3", "v1"), "b3 reserve")
	mustOK(t, s.Reserve(3, "b4", "v1"), "b4 reserve")
	// b2 密级被下调，归还时被跳过，b3 获得分配。
	mustOK(t, s.SetBorrowerLevel(4, "b2", archive.Public), "downgrade b2")
	mustOK(t, s.Return(5, "v1"), "return")
	snap := s.Snapshot(5)
	asg := snap.Volumes[0].Assignment
	if asg == nil || asg.BorrowerID != "b3" {
		t.Fatalf("应分配给 b3，实际 %+v", asg)
	}
	if !reflect.DeepEqual(snap.Volumes[0].Queue, []string{"b2", "b4"}) {
		t.Fatalf("b2 应保留队首位置，实际队列 %v", snap.Volumes[0].Queue)
	}
	// b3 取卷并归还；b2 密级恢复后仍居队首，优先于 b4。
	mustOK(t, s.Pickup(5, "b3", "v1"), "b3 pickup")
	mustOK(t, s.SetBorrowerLevel(6, "b2", archive.Internal), "restore b2")
	mustOK(t, s.Return(7, "v1"), "b3 return")
	snap = s.Snapshot(7)
	asg = snap.Volumes[0].Assignment
	if asg == nil || asg.BorrowerID != "b2" {
		t.Fatalf("应分配给队首 b2，实际 %+v", asg)
	}
	if !reflect.DeepEqual(snap.Volumes[0].Queue, []string{"b4"}) {
		t.Fatalf("队列应为 [b4]，实际 %v", snap.Volumes[0].Queue)
	}
}

// 暂停中的预约被冻结：不被分配但不出队。
func TestQueueHeadFrozenWhileSuspended(t *testing.T) {
	s := basicSvc(t)
	mustOK(t, s.AddVolume(0, "v2", archive.Public), "add v2")
	mustOK(t, s.Borrow(0, "b1", "v1"), "b1 borrow v1")
	mustOK(t, s.Borrow(0, "b2", "v2"), "b2 borrow v2")
	mustOK(t, s.Reserve(1, "b2", "v1"), "b2 reserve v1")
	mustOK(t, s.Reserve(2, "b3", "v1"), "b3 reserve v1")
	// b2 逾期达到阈值进入暂停（借期 5，day0 借出，最后一日 5，day16 归还逾期 11 天）。
	res := s.Return(16, "v2")
	mustOK(t, res, "b2 return v2")
	if res.OverdueDays != 11 {
		t.Fatalf("b2 应逾期 11 天，实际 %d", res.OverdueDays)
	}
	// b1 归还 v1：队首 b2 暂停被跳过，分配给 b3；b2 不出队。
	mustOK(t, s.Return(16, "v1"), "b1 return v1")
	snap := s.Snapshot(16)
	if asg := snap.Volumes[0].Assignment; asg == nil || asg.BorrowerID != "b3" {
		t.Fatalf("应分配给 b3，实际 %+v", asg)
	}
	if !reflect.DeepEqual(snap.Volumes[0].Queue, []string{"b2"}) {
		t.Fatalf("b2 应冻结在队首，实际 %v", snap.Volumes[0].Queue)
	}
}

// 取卷期限最后一日仍可取；届满未取视为放弃并顺延给下一位。
func TestPickupDeadlineBoundary(t *testing.T) {
	// 恰等期限最后一日取卷成功。
	s := basicSvc(t)
	mustOK(t, s.Borrow(0, "b1", "v1"), "b1 borrow")
	mustOK(t, s.Reserve(1, "b2", "v1"), "b2 reserve")
	mustOK(t, s.Return(10, "v1"), "return day10")
	snap := s.Snapshot(10)
	if asg := snap.Volumes[0].Assignment; asg == nil || asg.Deadline != 12 {
		t.Fatalf("分配期限应为 12，实际 %+v", asg)
	}
	mustOK(t, s.Pickup(12, "b2", "v1"), "期限最后一日取卷")

	// 届满未取：b2 放弃，b3 顺延。
	s2 := basicSvc(t)
	mustOK(t, s2.Borrow(0, "b1", "v1"), "b1 borrow")
	mustOK(t, s2.Reserve(1, "b2", "v1"), "b2 reserve")
	mustOK(t, s2.Reserve(1, "b3", "v1"), "b3 reserve")
	mustOK(t, s2.Return(10, "v1"), "return day10")
	// day13 已超过期限 12：b2 取卷被拒绝，且状态回滚（分配仍记录为 b2）。
	mustErr(t, s2.Pickup(13, "b2", "v1"), archive.ErrInvalidState, "逾期取卷")
	snap = s2.Snapshot(12)
	if asg := snap.Volumes[0].Assignment; asg == nil || asg.BorrowerID != "b2" {
		t.Fatalf("被拒绝操作不得改变状态，分配应仍为 b2，实际 %+v", asg)
	}
	// b3 取卷：触发期满处理，b2 失去位置，b3 获得分配并取走。
	res := s2.Pickup(13, "b3", "v1")
	mustOK(t, res, "b3 顺延取卷")
	if res.DueDay != 18 {
		t.Fatalf("b3 借期最后一日应为 18，实际 %d", res.DueDay)
	}
	snap = s2.Snapshot(13)
	if len(snap.Volumes[0].Queue) != 0 {
		t.Fatalf("b2 应已失去位置，队列应为空，实际 %v", snap.Volumes[0].Queue)
	}
}

// 累计逾期恰等阈值即进入暂停；差一天不暂停。
func TestOverdueThresholdExact(t *testing.T) {
	// 阈值 10：两次逾期 4+6=10 恰等阈值。
	s := basicSvc(t)
	mustOK(t, s.AddVolume(0, "v2", archive.Public), "add v2")
	mustOK(t, s.AddVolume(0, "v3", archive.Public), "add v3")
	mustOK(t, s.Borrow(0, "b1", "v1"), "borrow v1") // 最后一日 5
	res := s.Return(9, "v1")
	if res.OverdueDays != 4 {
		t.Fatalf("第一次逾期应为 4 天，实际 %d", res.OverdueDays)
	}
	mustOK(t, s.Borrow(9, "b1", "v2"), "borrow v2") // 最后一日 14
	res = s.Return(20, "v2")
	if res.OverdueDays != 6 {
		t.Fatalf("第二次逾期应为 6 天，实际 %d", res.OverdueDays)
	}
	mustErr(t, s.Borrow(20, "b1", "v3"), archive.ErrSuspended, "恰等阈值应暂停")

	// 对照：累计 9 不暂停。
	s2 := basicSvc(t)
	mustOK(t, s2.AddVolume(0, "v2", archive.Public), "add v2")
	mustOK(t, s2.Borrow(0, "b1", "v1"), "borrow v1")
	mustOK(t, s2.Return(9, "v1"), "逾期 4 天")
	mustOK(t, s2.Borrow(9, "b1", "v2"), "borrow v2")
	mustOK(t, s2.Return(19, "v2"), "逾期 5 天，累计 9")
	mustOK(t, s2.Borrow(19, "b1", "v1"), "累计 9 不暂停")
}

// 在借的逾期卷归还前不计入累计，归还时一次计入。
func TestOverdueCountedAtReturn(t *testing.T) {
	s := basicSvc(t)
	mustOK(t, s.AddVolume(0, "v2", archive.Public), "add v2")
	mustOK(t, s.Borrow(0, "b1", "v1"), "borrow v1") // 最后一日 5
	// day 20 已逾期 15 天，但未归还不计入累计，仍可借第二卷。
	mustOK(t, s.Borrow(20, "b1", "v2"), "在借逾期卷未归还不暂停")
	mustOK(t, s.Return(20, "v1"), "归还时一次计入 15 天")
	mustErr(t, s.Borrow(20, "b1", "v1"), archive.ErrSuspended, "归还后累计达阈值暂停")
}

// 暂停解除：全部在借卷归还且自最近一次归还起满冷静天数，恰等即解除；
// 解除时累计逾期清零。
func TestCooldownExact(t *testing.T) {
	s := basicSvc(t)
	mustOK(t, s.AddVolume(0, "v2", archive.Public), "add v2")
	mustOK(t, s.Borrow(0, "b1", "v1", "v2"), "borrow 2 卷") // 最后一日均为 5
	mustOK(t, s.Return(15, "v1"), "归还 v1 逾期 10 天，进入暂停")
	// 仍有在借卷，冷静期不起算。
	mustErr(t, s.Borrow(100, "b1", "v1"), archive.ErrSuspended, "有在借卷不解除")
	mustOK(t, s.Return(20, "v2"), "归还 v2，最近一次归还为 day20")
	// 冷静期 5：day24 仍暂停，day25 恰等解除。
	mustErr(t, s.Borrow(24, "b1", "v1"), archive.ErrSuspended, "冷静期未满")
	mustOK(t, s.Borrow(25, "b1", "v1"), "恰等冷静期解除")
	snap := s.Snapshot(25)
	for _, bs := range snap.Borrowers {
		if bs.ID == "b1" {
			if bs.Suspended {
				t.Fatalf("day25 应已解除暂停")
			}
			if bs.CumOverdue != 0 {
				t.Fatalf("解除时累计逾期应清零，实际 %d", bs.CumOverdue)
			}
		}
	}
	// 解除后新的逾期从零累计：逾期 9 天不再暂停。
	mustOK(t, s.Return(39, "v1"), "逾期 9 天归还") // 借期最后一日 30
	mustOK(t, s.Borrow(39, "b1", "v2"), "累计清零后逾期 9 不暂停")
}

// 封存：借出中的卷封存后仍须归还，归还后不再分配，预约一并取消。
func TestSealThenReturn(t *testing.T) {
	s := basicSvc(t)
	mustOK(t, s.Borrow(0, "b1", "v1"), "b1 borrow")
	mustOK(t, s.Reserve(1, "b2", "v1"), "b2 reserve")
	mustOK(t, s.Reserve(1, "b3", "v1"), "b3 reserve")
	mustOK(t, s.Seal(2, "v1"), "借出中封存")
	mustErr(t, s.Borrow(2, "b2", "v1"), archive.ErrInvalidState, "封存不得借出")
	mustErr(t, s.Reserve(2, "b2", "v1"), archive.ErrInvalidState, "封存不得预约")
	mustOK(t, s.Return(3, "v1"), "封存后仍须归还")
	snap := s.Snapshot(3)
	vs := snap.Volumes[0]
	if vs.Status != archive.StatusSealed {
		t.Fatalf("归还后应保持封存，实际 %s", vs.Status)
	}
	if vs.Assignment != nil || len(vs.Queue) != 0 {
		t.Fatalf("封存归还后不应分配且预约取消，实际 assignment=%+v queue=%v",
			vs.Assignment, vs.Queue)
	}
}

// 多卷同借全有或全无：任一卷不可借整批拒绝并报下标最小的失败项，
// 批内重复卷为参数非法；被拒绝的整批不留痕。
func TestBatchBorrowAtomic(t *testing.T) {
	s := basicSvc(t)
	mustOK(t, s.AddVolume(0, "v2", archive.Public), "add v2")
	mustOK(t, s.AddVolume(0, "v3", archive.Public), "add v3")
	mustOK(t, s.Seal(1, "v2"), "seal v2")
	before := s.Snapshot(1)
	// v2 封存导致整批失败，失败项下标为 1。
	e := mustErr(t, s.Borrow(2, "b1", "v1", "v2", "v3"), archive.ErrInvalidState, "整批拒绝")
	if e.Index != 1 {
		t.Fatalf("失败项下标应为 1，实际 %d", e.Index)
	}
	after := s.Snapshot(1)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("被拒绝的整批不得留痕\n前: %+v\n后: %+v", before, after)
	}
	// 批内重复卷为参数非法。
	e = mustErr(t, s.Borrow(2, "b1", "v1", "v3", "v1"), archive.ErrInvalidParam, "批内重复")
	if e.Index != 2 {
		t.Fatalf("重复项下标应为 2，实际 %d", e.Index)
	}
	// 不存在的卷报不存在及下标。
	e = mustErr(t, s.Borrow(2, "b1", "v1", "ghost"), archive.ErrNotFound, "卷不存在")
	if e.Index != 1 {
		t.Fatalf("失败项下标应为 1，实际 %d", e.Index)
	}
	// 全部可借时整批成功。
	res := s.Borrow(2, "b1", "v1", "v3")
	mustOK(t, res, "整批成功")
	if len(res.BorrowResults) != 2 {
		t.Fatalf("应借出 2 卷，实际 %d", len(res.BorrowResults))
	}
}

// 批量中不同卷以不同原因失败时，按类别优先级只报第一个。
func TestBatchErrorPriority(t *testing.T) {
	s := basicSvc(t)
	mustOK(t, s.AddVolume(0, "v2", archive.Public), "add v2")
	mustOK(t, s.AddVolume(0, "v3", archive.Public), "add v3")
	mustOK(t, s.Borrow(0, "b2", "v3"), "b2 借走 v3")
	mustOK(t, s.Seal(1, "v2"), "封存 v2")
	// v3(下标1) 已借出、v2(下标2) 封存：状态不允许优先于卷已借出。
	e := mustErr(t, s.Borrow(2, "b1", "v3", "v2"), archive.ErrInvalidState, "状态优先")
	if e.Index != 1 {
		t.Fatalf("封存卷下标应为 1，实际 %d", e.Index)
	}
}

// 错误类别优先级：参数非法 > 时钟回退 > 不存在 > 状态不允许 >
// 密级不足 > 借阅人暂停 > 卷已借出。
func TestErrorPriority(t *testing.T) {
	s := basicSvc(t)
	mustOK(t, s.Borrow(10, "b1", "v1"), "b1 borrow day10")
	// 参数非法优先于时钟回退。
	mustErr(t, s.Borrow(5, "", "v1"), archive.ErrInvalidParam, "参数优先于时钟")
	// 时钟回退优先于不存在。
	mustErr(t, s.Return(5, "ghost"), archive.ErrClockRollback, "时钟优先于不存在")
	// 密级不足优先于借阅人暂停、卷已借出。
	mustOK(t, s.AddVolume(11, "vt", archive.TopSecret), "add vt")
	mustOK(t, s.AddVolume(11, "v2", archive.Public), "add v2")
	mustOK(t, s.AddBorrower(11, "low", archive.Public), "add low")
	mustOK(t, s.Borrow(11, "b2", "vt"), "b2 借走 vt")
	mustErr(t, s.Borrow(12, "low", "vt"), archive.ErrClearance, "密级不足优先于卷已借出")
	// 构造暂停借阅人 b3：累计逾期 10。
	mustOK(t, s.Borrow(12, "b3", "v2"), "b3 borrow v2") // 最后一日 17
	mustOK(t, s.Return(27, "v2"), "b3 逾期 10 天暂停")
	// 暂停优先于卷已借出。
	mustErr(t, s.Borrow(28, "b3", "v1"), archive.ErrSuspended, "暂停优先于卷已借出")
	// 正常借阅人借已借出卷报卷已借出。
	mustErr(t, s.Borrow(28, "b2", "v1"), archive.ErrBorrowed, "卷已借出")
}

// 被拒绝的操作不得改变任何状态与时钟。
func TestRejectedOpNoStateChange(t *testing.T) {
	s := basicSvc(t)
	mustOK(t, s.Borrow(0, "b1", "v1"), "b1 borrow")
	mustOK(t, s.Reserve(1, "b2", "v1"), "b2 reserve")
	mustOK(t, s.Reserve(1, "b3", "v1"), "b3 reserve")
	mustOK(t, s.Return(10, "v1"), "归还并分配给 b2")
	before := s.Snapshot(10)
	// 各类拒绝：时钟回退、不存在、状态不允许、密级不足、重复预约、
	// 非被分配者取卷（day13 已逾 b2 的取卷期限，触发期满后回滚）。
	mustErr(t, s.Return(9, "v1"), archive.ErrClockRollback, "时钟回退")
	mustErr(t, s.Return(10, "ghost"), archive.ErrNotFound, "卷不存在")
	mustErr(t, s.Return(10, "v1"), archive.ErrInvalidState, "未借出")
	mustErr(t, s.Reserve(10, "b2", "v1"), archive.ErrInvalidState, "重复预约")
	mustErr(t, s.Pickup(13, "b1", "v1"), archive.ErrInvalidState, "非被分配者取卷")
	mustErr(t, s.Pickup(13, "b2", "v1"), archive.ErrInvalidState, "逾期取卷")
	after := s.Snapshot(10)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("被拒绝的操作改变了状态\n前: %+v\n后: %+v", before, after)
	}
	// 时钟水位未被拒绝操作推进：day10 的操作仍被接受。
	mustOK(t, s.Pickup(12, "b2", "v1"), "期限最后一日仍可取")
}

// 相同操作序列重放得到完全相同的结果与最终状态。
func TestReplayDeterminism(t *testing.T) {
	ops := []archive.Op{
		{Kind: archive.OpAddVolume, Now: 0, VolumeID: "v1", Level: archive.Public},
		{Kind: archive.OpAddVolume, Now: 0, VolumeID: "v2", Level: archive.Confidential},
		{Kind: archive.OpAddBorrower, Now: 0, BorrowerID: "b1", Level: archive.TopSecret},
		{Kind: archive.OpAddBorrower, Now: 0, BorrowerID: "b2", Level: archive.Internal},
		{Kind: archive.OpAddBorrower, Now: 0, BorrowerID: "b3", Level: archive.Public},
		{Kind: archive.OpBorrow, Now: 1, BorrowerID: "b1", VolumeIDs: []string{"v1", "v2"}},
		{Kind: archive.OpReserve, Now: 2, BorrowerID: "b2", VolumeID: "v2"},
		{Kind: archive.OpReserve, Now: 2, BorrowerID: "b3", VolumeID: "v2"}, // 密级不足
		{Kind: archive.OpRenew, Now: 4, BorrowerID: "b1", VolumeID: "v2", ApproverID: "b2"},
		{Kind: archive.OpReturn, Now: 6, VolumeID: "v1"},
		{Kind: archive.OpReturn, Now: 8, VolumeID: "v2"}, // 分配给 b2
		{Kind: archive.OpPickup, Now: 9, BorrowerID: "b2", VolumeID: "v2"},
		{Kind: archive.OpReturn, Now: 20, VolumeID: "v2"}, // b2 逾期
		{Kind: archive.OpBorrow, Now: 21, BorrowerID: "b2", VolumeIDs: []string{"v1"}},
		{Kind: archive.OpSeal, Now: 22, VolumeID: "v1"},
		{Kind: archive.OpBorrow, Now: 23, BorrowerID: "b1", VolumeIDs: []string{"v1"}}, // 封存
		{Kind: archive.OpReturn, Now: 30, VolumeID: "v1"},
	}
	run := func() ([]archive.Result, archive.StateSnapshot) {
		s := newSvc(t, testCfg())
		results := make([]archive.Result, 0, len(ops))
		for _, op := range ops {
			results = append(results, s.Apply(op))
		}
		return results, s.Snapshot(30)
	}
	r1, snap1 := run()
	r2, snap2 := run()
	if !reflect.DeepEqual(r1, r2) {
		t.Fatalf("重放结果不一致\n第一次: %+v\n第二次: %+v", r1, r2)
	}
	if !reflect.DeepEqual(snap1, snap2) {
		t.Fatalf("重放最终状态不一致\n第一次: %+v\n第二次: %+v", snap1, snap2)
	}
}

// 归还后确定被分配者的开销不随该卷历史预约总数增长：
// 无论历史上有多少预约已被消费，找到下一个有效预约者的扫描步数相同。
func TestQueueScanCostIndependentOfHistory(t *testing.T) {
	type observation struct {
		finalReturnScan  int64 // 最后一次归还的扫描步数
		totalScan        int64 // 全程扫描步数
		historyReservers int
	}
	var observations []observation
	for _, m := range []int{0, 5, 50, 200} {
		s := newSvc(t, testCfg())
		mustOK(t, s.AddVolume(0, "v1", archive.Public), "add v1")
		mustOK(t, s.AddBorrower(0, "holder", archive.Public), "add holder")
		mustOK(t, s.Borrow(0, "holder", "v1"), "首次借出")
		// m 个借阅人依次预约，并依次被分配、取走、归还（历史预约总数=m）。
		day := 1
		for i := 0; i < m; i++ {
			id := string(rune('a'+i%26)) + string(rune('A'+i/26))
			mustOK(t, s.AddBorrower(day, id, archive.Public), "add borrower")
			mustOK(t, s.Reserve(day, id, "v1"), "reserve")
			day++
		}
		for i := 0; i < m; i++ {
			id := string(rune('a'+i%26)) + string(rune('A'+i/26))
			mustOK(t, s.Return(day, "v1"), "归还给下一位")
			mustOK(t, s.Pickup(day, id, "v1"), "取卷")
			day++
		}
		mustOK(t, s.Return(day, "v1"), "清空队列")
		day++
		// 历史之后：再次借出并产生一个新预约，归还时扫描队首。
		mustOK(t, s.Borrow(day, "holder", "v1"), "再次借出")
		mustOK(t, s.AddBorrower(day, "newbie", archive.Public), "add newbie")
		mustOK(t, s.Reserve(day, "newbie", "v1"), "新预约")
		day++
		before := s.Stats().QueueScanSteps
		mustOK(t, s.Return(day, "v1"), "关键归还")
		finalScan := s.Stats().QueueScanSteps - before
		observations = append(observations, observation{
			finalReturnScan:  finalScan,
			totalScan:        s.Stats().QueueScanSteps,
			historyReservers: m,
		})
	}
	for _, o := range observations {
		// 每次归还有效预约者恰在队首，扫描恒为 1 步，与历史总数无关。
		if o.finalReturnScan != 1 {
			t.Fatalf("历史 %d 次预约后，归还扫描步数应为 1，实际 %d",
				o.historyReservers, o.finalReturnScan)
		}
		// 全程扫描步数等于有有效预约者的归还次数（m+1），随历史线性而非平方增长。
		if o.totalScan != int64(o.historyReservers+1) {
			t.Fatalf("历史 %d：总扫描步数应为 %d，实际 %d",
				o.historyReservers, o.historyReservers+1, o.totalScan)
		}
	}
}
