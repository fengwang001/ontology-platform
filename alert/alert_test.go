package alert

import (
	"fmt"
	"reflect"
	"testing"
)

func newPotassium(t *testing.T) *Center {
	t.Helper()
	c, err := New(60, 30, 10)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := c.AddTest("K", 25, 65, 5); err != nil {
		t.Fatalf("AddTest: %v", err)
	}
	return c
}

func mustResult(t *testing.T, c *Center, now int64, patient, code string, v int64) (int, bool) {
	t.Helper()
	id, crit, err := c.Result(now, patient, code, v)
	if err != nil {
		t.Fatalf("Result(now=%d,%s,%s,%d): %v", now, patient, code, v, err)
	}
	return id, crit
}

func TestNewValidation(t *testing.T) {
	for _, d := range [][3]int64{{0, 1, 1}, {1, -1, 1}, {1, 1, 10_001}} {
		if _, err := New(d[0], d[1], d[2]); err != ErrInvalidParam {
			t.Errorf("New%v: got %v, want ErrInvalidParam", d, err)
		}
	}
	if _, err := New(1, 1, 10_000); err != nil {
		t.Errorf("边界时限应合法: %v", err)
	}
}

func TestResultCreateUpgradeMerge(t *testing.T) {
	c := newPotassium(t)
	if err := c.SetWard("p1", "w1"); err != nil {
		t.Fatal(err)
	}
	id, crit := mustResult(t, c, 0, "p1", "K", 66)
	if !crit || id != 1 {
		t.Fatalf("首报: got id=%d crit=%v, want 1,true", id, crit)
	}
	ev := c.events[1]
	if ev.Sev != 1 || ev.Rep != 66 || ev.Deadline != 60 || ev.Status != PendingNotify {
		t.Fatalf("新建事件不符: %+v", ev)
	}
	// t=20 上报 70：升级为 sev2，deadline=min(60,50)=50，退回待通知。
	id, _ = mustResult(t, c, 20, "p1", "K", 70)
	if id != 1 {
		t.Fatalf("升级应并入同一事件, got id=%d", id)
	}
	if ev.Sev != 2 || ev.Rep != 70 || ev.Deadline != 50 || ev.Status != PendingNotify {
		t.Fatalf("升级后不符: %+v", ev)
	}
	// t=30 上报 71：同档并入，rep/sev/deadline 不变，结果追加。
	mustResult(t, c, 30, "p1", "K", 71)
	if ev.Sev != 2 || ev.Rep != 70 || ev.Deadline != 50 {
		t.Fatalf("同档并入不应改代表值: %+v", ev)
	}
	if len(ev.Readings) != 3 || ev.Readings[2].V != 71 {
		t.Fatalf("结果列表应追加: %+v", ev.Readings)
	}
}

func TestResultNormalNoEffect(t *testing.T) {
	c := newPotassium(t)
	c.SetWard("p1", "w1")
	mustResult(t, c, 0, "p1", "K", 66)
	id, crit := mustResult(t, c, 10, "p1", "K", 50)
	if crit || id != 0 {
		t.Fatalf("正常结果: got id=%d crit=%v, want 0,false", id, crit)
	}
	ev := c.events[1]
	if len(ev.Readings) != 1 || ev.Status != PendingNotify {
		t.Fatalf("复查正常不应影响事件: %+v", ev)
	}
}

func TestEscalationOnlyShortens(t *testing.T) {
	c := newPotassium(t)
	c.SetWard("p1", "w1")
	c.SetWard("p2", "w1")
	// e1: sev2, deadline=30；t=25 升级 sev3，now+T3=35 > 30，不延后。
	mustResult(t, c, 0, "p1", "K", 70)
	mustResult(t, c, 25, "p1", "K", 75)
	if ev := c.events[1]; ev.Deadline != 30 || ev.Sev != 3 {
		t.Fatalf("升级不应延后时限: %+v", ev)
	}
	// e2: sev2, deadline=60+30；t=45 升级 sev3，deadline=min(90,55)=55。
	mustResult(t, c, 30, "p2", "K", 70)
	mustResult(t, c, 45, "p2", "K", 75)
	if ev := c.events[2]; ev.Deadline != 55 || ev.Sev != 3 {
		t.Fatalf("升级应提前到新时限: %+v", ev)
	}
}

func TestLandingOrder(t *testing.T) {
	c, err := New(60, 5, 3)
	if err != nil {
		t.Fatal(err)
	}
	c.AddTest("K", 25, 65, 5)
	for _, p := range []string{"p1", "p2", "p3", "p4"} {
		c.SetWard(p, "w")
	}
	mustResult(t, c, 0, "p1", "K", 70) // e1 sev2 deadline 5
	mustResult(t, c, 0, "p2", "K", 70) // e2 sev2 deadline 5
	mustResult(t, c, 0, "p3", "K", 75) // e3 sev3 deadline 3
	// t=6 的正常结果操作触发落地：按 (deadline, 事件号) 升序。
	mustResult(t, c, 6, "p4", "K", 50)
	if got, want := c.Overdue(), []int{3, 1, 2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("落地次序: got %v, want %v", got, want)
	}
	for _, id := range []int{1, 2, 3} {
		if !c.events[id].Late {
			t.Fatalf("事件 %d 应粘滞逾期", id)
		}
	}
}

func TestExactDeadlineNotOverdue(t *testing.T) {
	c := newPotassium(t)
	c.SetWard("p1", "w1")
	c.SetWard("p2", "w1")
	mustResult(t, c, 0, "p1", "K", 66) // e1 deadline 60
	mustResult(t, c, 60, "p2", "K", 50)
	if got := c.Overdue(); len(got) != 0 {
		t.Fatalf("恰等 deadline 不应逾期: %v", got)
	}
	mustResult(t, c, 61, "p2", "K", 50)
	if got := c.Overdue(); !reflect.DeepEqual(got, []int{1}) {
		t.Fatalf("超过 deadline 应逾期: %v", got)
	}
}

func TestRejectedNoSideEffects(t *testing.T) {
	c := newPotassium(t)
	c.SetWard("p1", "w1")
	c.SetWard("p2", "w1")
	mustResult(t, c, 0, "p1", "K", 75) // e1 deadline 10
	// 被拒绝的操作：不落地、不推进时钟、不占事件号。
	if _, _, err := c.Result(20, "p1", "XX", 70); err != ErrNotFound {
		t.Fatalf("未知项目: got %v, want ErrNotFound", err)
	}
	if got := c.Overdue(); len(got) != 0 {
		t.Fatalf("被拒绝操作不应落地逾期: %v", got)
	}
	if _, _, err := c.Result(5, "p1", "K", 70); err != nil {
		t.Fatalf("时钟不应被被拒绝操作推进: %v", err)
	}
	if _, _, err := c.Result(5, "ghost", "K", 70); err != ErrNotFound {
		t.Fatalf("未知患者: got %v, want ErrNotFound", err)
	}
	if _, _, err := c.Result(5, "p1", "K", 2_000_000_000); err != ErrInvalidParam {
		t.Fatalf("值越界: got %v, want ErrInvalidParam", err)
	}
	id, _ := mustResult(t, c, 5, "p2", "K", 66)
	if id != 2 {
		t.Fatalf("被拒绝操作不占号: got id=%d, want 2", id)
	}
	// 时钟回退拒绝。
	if _, _, err := c.Result(4, "p1", "K", 70); err != ErrClockBack {
		t.Fatalf("时钟回退: got %v, want ErrClockBack", err)
	}
}

func TestRejectionOrder(t *testing.T) {
	c := newPotassium(t)
	c.SetWard("p1", "w1")
	mustResult(t, c, 10, "p1", "K", 66)
	// 参数非法优先于时钟回退。
	if _, _, err := c.Result(5, "", "K", 70); err != ErrInvalidParam {
		t.Fatalf("参数非法应最先: got %v", err)
	}
	// 时钟回退优先于不存在。
	if _, _, err := c.Result(5, "p1", "XX", 70); err != ErrClockBack {
		t.Fatalf("时钟回退应优先于不存在: got %v", err)
	}
}

func TestTouchedLocate(t *testing.T) {
	for _, n := range []int{100, 10000} {
		c := newPotassium(t)
		for i := 0; i < n; i++ {
			p := fmt.Sprintf("p%d", i)
			c.SetWard(p, "w")
			mustResult(t, c, 0, p, "K", 66)
		}
		before := c.touchedLocate
		mustResult(t, c, 1, fmt.Sprintf("p%d", n/2), "K", 70)
		if d := c.touchedLocate - before; d > 1 {
			t.Fatalf("n=%d: 并入定位触碰 %d 个事件, 应 <= 1", n, d)
		}
		before = c.touchedLocate
		c.SetWard("new", "w")
		mustResult(t, c, 2, "new", "K", 66)
		if d := c.touchedLocate - before; d != 0 {
			t.Fatalf("n=%d: 新建定位触碰 %d 个事件, 应为 0", n, d)
		}
	}
}

func TestTouchedLand(t *testing.T) {
	const k = 5
	for _, n := range []int{100, 10000} {
		c := newPotassium(t)
		for i := 0; i < n; i++ {
			p := fmt.Sprintf("p%d", i)
			c.SetWard(p, "w")
			v := int64(66) // sev1, deadline 60
			if i < k {
				v = 15 // sev3, deadline 10
			}
			mustResult(t, c, 0, p, "K", v)
		}
		c.SetWard("px", "w")
		before := c.touchedLand
		mustResult(t, c, 11, "px", "K", 50) // 落地 k 个事件
		if d := c.touchedLand - before; d > k+1 {
			t.Fatalf("n=%d: 落地触碰 %d 个事件, 应 <= %d", n, d, k+1)
		}
		if got := len(c.Overdue()); got != k {
			t.Fatalf("n=%d: 落地 %d 个, 应 %d 个", n, got, k)
		}
	}
}

func TestEscalationNoStaleHeapEntries(t *testing.T) {
	c := newPotassium(t)
	for _, p := range []string{"p1", "p2"} {
		c.SetWard(p, "w")
	}
	mustResult(t, c, 0, "p1", "K", 66) // e1 deadline 60
	mustResult(t, c, 0, "p2", "K", 70) // e2 deadline 30
	mustResult(t, c, 1, "p1", "K", 75) // e1 升级 sev3, deadline=min(60,11)=11
	if ev := c.events[1]; ev.Deadline != 11 {
		t.Fatalf("升级后 deadline: %+v", ev)
	}
	mustResult(t, c, 100, "p2", "K", 50) // 落地全部
	got := c.Overdue()
	if !reflect.DeepEqual(got, []int{1, 2}) {
		t.Fatalf("落地次序: got %v, want [1 2]", got)
	}
	seen := map[int]bool{}
	for _, id := range got {
		if seen[id] {
			t.Fatalf("逾期清单事件号重复: %v", got)
		}
		seen[id] = true
	}
	if len(c.due) != 0 {
		t.Fatalf("到期堆应无作废条目, 剩 %d", len(c.due))
	}
}
