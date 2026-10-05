package notify_test

import (
	"reflect"
	"sync"
	"testing"

	"ontology/alert"
	"ontology/notify"
)

func newManager(t *testing.T) *notify.Manager {
	t.Helper()
	m, err := notify.New(60, 30, 10)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := m.AddTest("K", 25, 65, 5); err != nil {
		t.Fatalf("AddTest: %v", err)
	}
	if err := m.SetWard("p1", "w1"); err != nil {
		t.Fatal(err)
	}
	if err := m.Grant("n1", "w1", notify.Nurse); err != nil {
		t.Fatal(err)
	}
	if err := m.Grant("d1", "w1", notify.Doctor); err != nil {
		t.Fatal(err)
	}
	return m
}

func event(t *testing.T, m *notify.Manager, id int) alert.Event {
	t.Helper()
	for _, ev := range m.Snapshot() {
		if ev.ID == id {
			return ev
		}
	}
	t.Fatalf("事件 %d 不存在", id)
	return alert.Event{}
}

// walkthroughPrefix 执行题目示例的公共前缀（t=0..30），返回事件号。
func walkthroughPrefix(t *testing.T, m *notify.Manager) int {
	t.Helper()
	id, crit, err := m.Result(0, "p1", "K", 66)
	if err != nil || !crit || id != 1 {
		t.Fatalf("t=0 新建: id=%d crit=%v err=%v", id, crit, err)
	}
	if err := m.Notify(5, 1, "tech", "n1"); err != nil {
		t.Fatalf("t=5 Notify: %v", err)
	}
	if _, _, err := m.Result(20, "p1", "K", 70); err != nil {
		t.Fatalf("t=20 升级: %v", err)
	}
	if ev := event(t, m, 1); ev.Sev != 2 || ev.Rep != 70 || ev.Deadline != 50 || ev.Status != alert.PendingNotify {
		t.Fatalf("t=20 升级后: %+v", ev)
	}
	if _, err := m.ReadBack(22, 1, "n1", 70); err != alert.ErrBadState {
		t.Fatalf("t=22 ReadBack 应状态不符: %v", err)
	}
	if err := m.Notify(25, 1, "tech", "n1"); err != nil {
		t.Fatalf("t=25 Notify: %v", err)
	}
	mismatch, err := m.ReadBack(26, 1, "n1", 66)
	if err != nil || !mismatch {
		t.Fatalf("t=26 ReadBack(66) 应不符: mismatch=%v err=%v", mismatch, err)
	}
	mismatch, err = m.ReadBack(27, 1, "n1", 70)
	if err != nil || mismatch {
		t.Fatalf("t=27 ReadBack(70) 应通过: mismatch=%v err=%v", mismatch, err)
	}
	if _, _, err := m.Result(30, "p1", "K", 71); err != nil {
		t.Fatalf("t=30 同档并入: %v", err)
	}
	if ev := event(t, m, 1); ev.Rep != 70 || len(ev.Readings) != 3 {
		t.Fatalf("t=30 并入不应改代表值: %+v", ev)
	}
	return 1
}

func TestWalkthroughActExactDeadline(t *testing.T) {
	m := newManager(t)
	walkthroughPrefix(t, m)
	late, err := m.Act(50, 1, "d1")
	if err != nil {
		t.Fatalf("t=50 Act: %v", err)
	}
	if late {
		t.Fatal("恰等 deadline 不应记 late")
	}
	if got := m.Overdue(); len(got) != 0 {
		t.Fatalf("恰等 deadline 不应落地: %v", got)
	}
}

func TestWalkthroughActAfterDeadline(t *testing.T) {
	m := newManager(t)
	walkthroughPrefix(t, m)
	late, err := m.Act(51, 1, "d1")
	if err != nil {
		t.Fatalf("t=51 Act: %v", err)
	}
	if !late {
		t.Fatal("逾期后闭环应记 late")
	}
	if got := m.Overdue(); !reflect.DeepEqual(got, []int{1}) {
		t.Fatalf("逾期清单: %v", got)
	}
}

func TestMismatchTwiceFallback(t *testing.T) {
	m := newManager(t)
	m.Result(0, "p1", "K", 66)
	m.Notify(1, 1, "tech", "n1")
	if mm, _ := m.ReadBack(2, 1, "n1", 65); !mm {
		t.Fatal("第一次不符应返回 mismatch")
	}
	if mm, _ := m.ReadBack(3, 1, "n1", 65); !mm {
		t.Fatal("第二次不符应返回 mismatch")
	}
	if ev := event(t, m, 1); ev.Status != alert.PendingNotify || ev.Mismatch != 0 {
		t.Fatalf("两次不符应退回待通知并清零: %+v", ev)
	}
	if _, err := m.ReadBack(4, 1, "n1", 66); err != alert.ErrBadState {
		t.Fatalf("退回后 ReadBack 应状态不符: %v", err)
	}
	// 重新通知，接收人可以不同（医生亦可接收）。
	if err := m.Notify(5, 1, "tech", "d1"); err != nil {
		t.Fatalf("重新 Notify: %v", err)
	}
	if _, err := m.ReadBack(6, 1, "n1", 66); err != alert.ErrNoQual {
		t.Fatalf("旧接收人应无资格: %v", err)
	}
	if mm, err := m.ReadBack(6, 1, "d1", 66); err != nil || mm {
		t.Fatalf("新接收人回读: mm=%v err=%v", mm, err)
	}
}

func TestNotifyQualification(t *testing.T) {
	m := newManager(t)
	m.Result(0, "p1", "K", 66)
	if err := m.Notify(1, 1, "tech", "ghost"); err != alert.ErrNoQual {
		t.Fatalf("无资格接收人: %v", err)
	}
	m.Grant("n2", "w2", notify.Nurse)
	if err := m.Notify(1, 1, "tech", "n2"); err != alert.ErrNoQual {
		t.Fatalf("其他病区护士: %v", err)
	}
	if err := m.Notify(1, 1, "tech", "n1"); err != nil {
		t.Fatalf("本病区护士: %v", err)
	}
	if err := m.Notify(2, 1, "tech", "n1"); err != alert.ErrBadState {
		t.Fatalf("重复 Notify 应状态不符: %v", err)
	}
}

func TestActQualification(t *testing.T) {
	m := newManager(t)
	m.Result(0, "p1", "K", 66)
	m.Notify(1, 1, "tech", "n1")
	m.ReadBack(2, 1, "n1", 66)
	if _, err := m.Act(3, 1, "n1"); err != alert.ErrNoQual {
		t.Fatalf("护士不能处置: %v", err)
	}
	m.Grant("d2", "w2", notify.Doctor)
	if _, err := m.Act(3, 1, "d2"); err != alert.ErrNoQual {
		t.Fatalf("其他病区医生: %v", err)
	}
	if _, err := m.Act(3, 1, "d1"); err != nil {
		t.Fatalf("本病区医生: %v", err)
	}
	if _, err := m.Act(4, 1, "d1"); err != alert.ErrBadState {
		t.Fatalf("闭环后再 Act 应状态不符: %v", err)
	}
}

func TestWardChangeAffectsQualification(t *testing.T) {
	m := newManager(t)
	m.Result(0, "p1", "K", 66)
	m.SetWard("p1", "w2")
	if err := m.Notify(1, 1, "tech", "n1"); err != alert.ErrNoQual {
		t.Fatalf("换病区后原护士应无资格: %v", err)
	}
	m.Grant("n2", "w2", notify.Nurse)
	if err := m.Notify(1, 1, "tech", "n2"); err != nil {
		t.Fatalf("新病区护士应有资格: %v", err)
	}
}

func TestRejectionOrder(t *testing.T) {
	m := newManager(t)
	m.Result(10, "p1", "K", 66)
	// 参数非法 > 时钟回退。
	if err := m.Notify(5, 1, "tech", ""); err != alert.ErrInvalidParam {
		t.Fatalf("参数非法应最先: %v", err)
	}
	// 时钟回退 > 事件不存在。
	if err := m.Notify(5, 99, "tech", "n1"); err != alert.ErrClockBack {
		t.Fatalf("时钟回退应优先: %v", err)
	}
	// 事件不存在 > 无资格。
	if err := m.Notify(11, 99, "tech", "ghost"); err != alert.ErrNotFound {
		t.Fatalf("不存在应优先于无资格: %v", err)
	}
	// 无资格 > 状态不符：事件待通知，用无资格接收人。
	if err := m.Notify(12, 1, "tech", "ghost"); err != alert.ErrNoQual {
		t.Fatalf("无资格应优先于状态不符: %v", err)
	}
	// ReadBack：接收人不符报无资格，优先于状态不符。
	m.Notify(13, 1, "tech", "n1")
	m.ReadBack(14, 1, "n1", 66) // 转待处置
	if _, err := m.ReadBack(15, 1, "ghost", 66); err != alert.ErrNoQual {
		t.Fatalf("非接收人应报无资格: %v", err)
	}
	if _, err := m.ReadBack(15, 1, "n1", 66); err != alert.ErrBadState {
		t.Fatalf("接收人但状态不对应报状态不符: %v", err)
	}
}

func TestClosedEventImmutable(t *testing.T) {
	m := newManager(t)
	m.Result(0, "p1", "K", 66)
	m.Notify(1, 1, "tech", "n1")
	m.ReadBack(2, 1, "n1", 66)
	m.Act(3, 1, "d1")
	before := event(t, m, 1)
	// 闭环后同患者同项目再报危急：新建事件，闭环事件不再变化。
	id, crit, err := m.Result(4, "p1", "K", 75)
	if err != nil || !crit || id != 2 {
		t.Fatalf("闭环后应新建事件: id=%d crit=%v err=%v", id, crit, err)
	}
	m.Notify(5, 2, "tech", "n1")
	after := event(t, m, 1)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("闭环事件不应再变化:\n前 %+v\n后 %+v", before, after)
	}
	if after.Status != alert.Closed {
		t.Fatalf("闭环状态: %+v", after)
	}
}

func TestOverdueFlowContinues(t *testing.T) {
	m := newManager(t)
	m.Result(0, "p1", "K", 66) // deadline 60
	if err := m.Notify(61, 1, "tech", "n1"); err != nil {
		t.Fatalf("逾期后 Notify 应照常: %v", err)
	}
	if got := m.Overdue(); !reflect.DeepEqual(got, []int{1}) {
		t.Fatalf("逾期清单: %v", got)
	}
	if mm, err := m.ReadBack(62, 1, "n1", 66); err != nil || mm {
		t.Fatalf("逾期后 ReadBack 应照常: mm=%v err=%v", mm, err)
	}
	late, err := m.Act(63, 1, "d1")
	if err != nil || !late {
		t.Fatalf("逾期闭环: late=%v err=%v", late, err)
	}
	if got := m.Overdue(); !reflect.DeepEqual(got, []int{1}) {
		t.Fatalf("逾期清单不应重复: %v", got)
	}
}

func TestConcurrent(t *testing.T) {
	m := newManager(t)
	for _, p := range []string{"p2", "p3", "p4"} {
		m.SetWard(p, "w1")
	}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			patients := []string{"p1", "p2", "p3", "p4"}
			for i := 0; i < 200; i++ {
				now := int64(i)
				p := patients[(g+i)%len(patients)]
				id, crit, err := m.Result(now, p, "K", int64(60+i%20))
				if err != nil || !crit {
					continue
				}
				if err := m.Notify(now, id, "tech", "n1"); err == nil {
					if mm, _ := m.ReadBack(now, id, "n1", 66); mm {
					}
				}
				m.Act(now, id, "d1")
			}
		}(g)
	}
	wg.Wait()
	seen := map[int]bool{}
	for _, id := range m.Overdue() {
		if seen[id] {
			t.Fatalf("逾期清单事件号重复: %d", id)
		}
		seen[id] = true
	}
}
