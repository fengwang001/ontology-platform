package replen

import (
	"errors"
	"sync"
	"testing"

	"ontology/task"
)

// TestRejectionOrder 拒绝次序：非法 > 不存在 > 状态 > 冲突 > 库存不足 > 超量；
// 被拒绝操作不改任何状态（含 Starved），不占任务号。
func TestRejectionOrder(t *testing.T) {
	g, n := newEng(), "rej"
	g.mustErr(t, n, "AddSlot(min<1)", g.e.AddSlot("K", "S", 0, 2, 3, 1, 0), ErrInvalid)
	g.mustErr(t, n, "AddSlot(min>=max)", g.e.AddSlot("K", "S", 5, 4, 6, 1, 0), ErrInvalid)
	g.mustErr(t, n, "AddSlot(max>cap)", g.e.AddSlot("K", "S", 1, 2, 1, 1, 0), ErrInvalid)
	g.mustErr(t, n, "AddSlot(cap>1e9)", g.e.AddSlot("K", "S", 1, 2, 1_000_000_001, 1, 0), ErrInvalid)
	g.mustErr(t, n, "AddSlot(c=0)", g.e.AddSlot("K", "S", 1, 2, 3, 0, 0), ErrInvalid)
	g.mustErr(t, n, "AddSlot(c>1e6)", g.e.AddSlot("K", "S", 1, 2, 3, 1_000_001, 0), ErrInvalid)
	g.mustErr(t, n, "AddSlot(on<0)", g.e.AddSlot("K", "S", 1, 2, 3, 1, -1), ErrInvalid)
	g.mustErr(t, n, "AddSlot(on>cap)", g.e.AddSlot("K", "S", 1, 2, 3, 1, 4), ErrInvalid)
	g.mustErr(t, n, "AddSlot(loc空)", g.e.AddSlot("", "S", 1, 2, 3, 1, 0), ErrInvalid)
	if g.e.testSeq() != 0 {
		t.Fatalf("非法 AddSlot 不应占号")
	}

	g.addSlot(t, n, "K", "S", 10, 40, 60, 12, 15)
	g.addSlot(t, n, "CAP", "BIG", 1, 2, 3, 1, 1)
	// 非法先于冲突
	g.mustErr(t, n, "AddSlot(冲突但非法)", g.e.AddSlot("K", "S2", 0, 40, 60, 12, 15), ErrInvalid)
	g.mustErr(t, n, "AddSlot(冲突)", g.e.AddSlot("K", "S2", 10, 40, 60, 12, 15), ErrConflict)

	// AddReserve：非法 > SKU 不存在 > 超累计上限
	g.mustErr(t, n, "AddReserve(非法先于不存在)", g.e.AddReserve("NOPE", 0), ErrInvalid)
	g.mustErr(t, n, "AddReserve(SKU不存在)", g.e.AddReserve("NOPE", 5), ErrNotFound)
	for i := 0; i < 1000; i++ { // 合法累加到恰好 1e12
		g.mustOK(t, n, "AddReserve", g.e.AddReserve("BIG", 1_000_000_000))
	}
	g.mustErr(t, n, "AddReserve(超上限)", g.e.AddReserve("BIG", 1), ErrOver)

	// Pick：非法 > 不存在 > 库存不足
	g.mustErr(t, n, "Pick(非法先于不存在)", g.e.Pick("NOPE", 0), ErrInvalid)
	g.mustErr(t, n, "Pick(不存在)", g.e.Pick("NOPE", 1), ErrNotFound)
	g.mustErr(t, n, "Pick(库存不足)", g.e.Pick("K", 16), ErrShort)

	// Demand：非法 > 不存在 > need>cap
	_, _, err := g.e.Demand("NOPE", 0)
	g.mustErr(t, n, "Demand(非法先于不存在)", err, ErrInvalid)
	_, _, err = g.e.Demand("NOPE", 1)
	g.mustErr(t, n, "Demand(不存在)", err, ErrNotFound)
	_, _, err = g.e.Demand("K", 61)
	g.mustErr(t, n, "Demand(need>cap)", err, ErrInvalid)

	// Confirm/Cancel：非法 > 任务不存在 > 状态不符 > 超量
	g.mustErr(t, n, "Confirm(非法先于不存在)", g.e.Confirm(0, 0), ErrInvalid)
	g.mustErr(t, n, "Confirm(actual<0)", g.e.Confirm(1, -1), ErrInvalid)
	g.mustErr(t, n, "Confirm(不存在)", g.e.Confirm(999, 0), ErrNotFound)
	g.mustErr(t, n, "Cancel(非法先于不存在)", g.e.Cancel(0), ErrInvalid)
	g.mustErr(t, n, "Cancel(不存在)", g.e.Cancel(999), ErrNotFound)

	// 造一条任务再验证状态/超量次序
	g.reserve(t, n, "S", 100)
	g.pick(t, n, "K", 5) // T1=24
	g.mustErr(t, n, "Confirm(超量)", g.e.Confirm(1, 25), ErrOver)
	g.confirm(t, n, 1, 24)
	g.mustErr(t, n, "Confirm(已完成)", g.e.Confirm(1, 0), ErrState)
	g.mustErr(t, n, "Cancel(已完成)", g.e.Cancel(1), ErrState)
	// 已完成任务上超量参数：状态不符先于超量
	g.mustErr(t, n, "Confirm(完成态非法actual)", g.e.Confirm(1, 100), ErrState)

	// 取消态
	g.pick(t, n, "K", 1) // onHand: 15+24=39 ->38，无触发
	// 再造一条可取消任务：
	g2, n2 := newEng(), "rej2"
	g2.addSlot(t, n2, "L", "X", 10, 40, 60, 12, 15)
	g2.reserve(t, n2, "X", 100)
	g2.pick(t, n2, "L", 5) // T1
	g2.cancel(t, n2, 1)
	g2.mustErr(t, n2, "Cancel(已取消)", g2.e.Cancel(1), ErrState)
	g2.mustErr(t, n2, "Confirm(已取消)", g2.e.Confirm(1, 0), ErrState)

	// 被拒绝操作不改状态：超量 Pick 被拒后 onHand 保持 33。
	if on, _ := g.e.testOnHand("K"); on != 33 {
		t.Fatalf("拒绝后状态被改变 onHand=%d", on)
	}
	if !errors.Is(ErrShort, ErrShort) || !errors.Is(ErrState, ErrState) {
		t.Fatalf("哨兵错误 errors.Is 失效")
	}
}

// TestTasksOrdering 紧急在前、同类按任务号升序。
func TestTasksOrdering(t *testing.T) {
	g, n := newEng(), "order"
	g.addSlot(t, n, "A", "S", 10, 100, 200, 12, 15)
	g.addSlot(t, n, "B", "S", 10, 100, 200, 12, 15)
	g.reserve(t, n, "S", 1000)
	g.pick(t, n, "A", 5) // T1 常规 A
	g.pick(t, n, "B", 5) // T2 常规 B
	g.demand(t, n, "A", 200, []int64{1}, []int64{3})
	// on=10, 升级 T1 -> 10+24=34<200；新建 ceil(166/12)*12=168，cap 限 floor(176/12)*12=168，avail 足够 -> T3=168
	ids := g.openIDs()
	want := []int64{1, 3, 2}
	if !eqIDs(ids, want) {
		t.Fatalf("Tasks 顺序=%v 期望 %v", ids, want)
	}
	for _, id := range []int64{1, 3} {
		if tk, _ := g.e.testTask(id); tk.Kind != task.Urgent {
			t.Fatalf("T%d 应为紧急", id)
		}
	}
	if tk, _ := g.e.testTask(2); tk.Kind != task.Normal {
		t.Fatalf("T2 应为常规")
	}
	// 完成 T1 后顺序 T3(紧急), T2(常规)
	g.confirm(t, n, 1, 24)
	if !eqIDs(g.openIDs(), []int64{3, 2}) {
		t.Fatalf("完成 T1 后顺序=%v", g.openIDs())
	}
}

// TestRejectedNoStarve 被拒绝操作不增加 Starved、不占号。
func TestRejectedNoStarve(t *testing.T) {
	g := newEng()
	g.e.AddSlot("K", "S", 10, 40, 60, 12, 15)
	// 触发常规但无储备 -> Starved=1
	if err := g.e.Pick("K", 5); err != nil {
		t.Fatal(err)
	}
	if g.e.testStarved("K") != 1 || g.e.testSeq() != 0 {
		t.Fatalf("Starved=%d seq=%d", g.e.testStarved("K"), g.e.testSeq())
	}
	// 库存不足的 Pick 被拒：Starved 不变
	if err := g.e.Pick("K", 100); !errors.Is(err, ErrShort) {
		t.Fatalf("err=%v", err)
	}
	if g.e.testStarved("K") != 1 || g.e.testSeq() != 0 {
		t.Fatalf("拒绝后 Starved=%d seq=%d", g.e.testStarved("K"), g.e.testSeq())
	}
}

// TestConcurrent 高并发下不发生竞态、不变量不破坏（-race 验证）。
func TestConcurrent(t *testing.T) {
	g := newEng()
	if err := g.e.AddSlot("K", "S", 10, 1000, 2000, 12, 500); err != nil {
		t.Fatal(err)
	}
	if err := g.e.AddReserve("S", 1_000_000); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				switch (seed + j) % 5 {
				case 0:
					_ = g.e.Pick("K", 1)
				case 1:
					_, _, _ = g.e.Demand("K", int64(100+seed))
				case 2:
					for _, tk := range g.e.Tasks() {
						_ = g.e.Confirm(tk.ID, tk.Qty)
					}
				case 3:
					_ = g.e.AddReserve("S", 1)
				default:
					_ = g.e.Tasks()
				}
			}
		}(i)
	}
	wg.Wait()
	on, _ := g.e.testOnHand("K")
	transit := g.e.testInTransit("K")
	if on < 0 || transit < 0 || on+transit > 2000 {
		t.Fatalf("不变量破坏 on=%d transit=%d", on, transit)
	}
	if g.e.testAvail("S") < 0 {
		t.Fatalf("avail 为负")
	}
}
