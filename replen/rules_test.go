package replen

import (
	"testing"

	"ontology/task"
)

// TestBoundaries eff 恰等 min 触发（AddSlot 即触发但储备为 0 -> Starved）。
func TestBoundaries(t *testing.T) {
	g, n := newEng(), "eq-min"
	g.addSlot(t, n, "K", "S", 10, 20, 30, 6, 10)
	g.check(t, n, "K", "S", state{onHand: 10, starved: 1, tasks: []int64{}})
	g.reserve(t, n, "S", 100)
	// AddReserve 不触发；onHand=10 已在临界，但必须等下一次受检操作。Pick 不合法（qty>=1）
	// 会使 onHand 变 9 后 eff=9<=10，want=floor(11/6)*6=6。
	g.pick(t, n, "K", 1)
	g.check(t, n, "K", "S", state{onHand: 9, inTransit: 6, starved: 1, reserve: 100, avail: 94, tasks: []int64{1}})
}

// TestMinPlusOne 在途为 0、onHand=min+1 不触发；Pick 一件到 min 才触发。
func TestMinPlusOne(t *testing.T) {
	g, n := newEng(), "min+1"
	g.addSlot(t, n, "K", "S", 10, 40, 60, 12, 11)
	g.reserve(t, n, "S", 100)
	g.check(t, n, "K", "S", state{onHand: 11, reserve: 100, avail: 100, tasks: []int64{}})
	g.pick(t, n, "K", 1)
	g.check(t, n, "K", "S", state{onHand: 10, inTransit: 24, reserve: 100, avail: 76, tasks: []int64{1}})
}

// TestInTransitBlocks 在途量阻止重复触发。
func TestInTransitBlocks(t *testing.T) {
	g, n := newEng(), "block"
	g.addSlot(t, n, "K", "S", 10, 40, 60, 12, 15)
	g.reserve(t, n, "S", 100)
	g.pick(t, n, "K", 5) // on=10 T1=24, eff=34
	g.pick(t, n, "K", 9) // on=1, eff=25>10 不新建
	g.check(t, n, "K", "S", state{onHand: 1, inTransit: 24, reserve: 100, avail: 76, tasks: []int64{1}})
}

// TestUrgentRoundings 紧急向上取整、cap 与储备双封顶。
func TestUrgentRoundings(t *testing.T) {
	// cap 封顶：min=10 max=20 cap=25 c=12。
	g, n := newEng(), "urgent-cap"
	g.addSlot(t, n, "K", "S", 10, 20, 25, 12, 12)
	g.reserve(t, n, "S", 100)
	g.pick(t, n, "K", 1) // on=11，eff=11>10 常规不触发
	g.demand(t, n, "K", 24, []int64{}, []int64{1})
	// want=ceil(13/12)*12=24；cap 限 floor(14/12)*12=12 -> 新建 12
	if q := taskQty(g.e, 1); q != 12 {
		t.Fatalf("cap 封顶 qty=%d 期望 12", q)
	}
	g.check(t, n, "K", "S", state{onHand: 11, inTransit: 12, urgent: 12, reserve: 100, avail: 88, tasks: []int64{1}})

	// 储备双封顶：cap 充足但储备只够 12。
	g2, n2 := newEng(), "urgent-reserve"
	g2.addSlot(t, n2, "K", "S", 10, 40, 60, 12, 12)
	g2.reserve(t, n2, "S", 12)
	g2.pick(t, n2, "K", 1)
	g2.demand(t, n2, "K", 24, []int64{}, []int64{1})
	if q := taskQty(g2.e, 1); q != 12 {
		t.Fatalf("储备封顶 qty=%d 期望 12", q)
	}
	g2.check(t, n2, "K", "S", state{onHand: 11, inTransit: 12, urgent: 12, reserve: 12, avail: 0, tasks: []int64{1}})

	// 储备不足一箱：不新建，需求不保证满足，且不计 Starved（Starved 仅常规路径）。
	g3, n3 := newEng(), "urgent-none"
	g3.addSlot(t, n3, "K", "S", 10, 40, 60, 12, 12)
	g3.reserve(t, n3, "S", 11)
	g3.pick(t, n3, "K", 1)
	g3.demand(t, n3, "K", 24, []int64{}, []int64{})
	g3.check(t, n3, "K", "S", state{onHand: 11, reserve: 11, avail: 11, tasks: []int64{}})
}

// TestUpgradeStop 升级按任务号升序、恰好够用即停。
func TestUpgradeStop(t *testing.T) {
	// 两个常规任务 T1、T2：先用受限储备制造 T1=12，补储备后取消再连锁制造 T2=12。
	g, n := newEng(), "upgrade-stop"
	g.addSlot(t, n, "K", "S", 10, 100, 200, 12, 15)
	g.reserve(t, n, "S", 12)
	g.pick(t, n, "K", 5) // on=10, want=floor(90/12)*12=84，avail 限 12 -> T1=12
	g.reserve(t, n, "S", 100)
	g.cancel(t, n, 1) // 释放后 eff=10<=min，want=84，avail=100 -> T2=84
	g.check(t, n, "K", "S", state{onHand: 10, inTransit: 84, reserve: 112, avail: 28, tasks: []int64{2}})

	// 为验证"恰好够用即停"，用小题给场景：on=2、一条常规 24，need=26。
	g2, n2 := newEng(), "upgrade-exact"
	g2.addSlot(t, n2, "K", "S", 10, 40, 60, 12, 15)
	g2.reserve(t, n2, "S", 100)
	g2.pick(t, n2, "K", 5) // on=10 T1=24
	g2.pick(t, n2, "K", 8) // on=2, eff=26 不新建
	g2.demand(t, n2, "K", 26, []int64{1}, []int64{})
	if tk, _ := g2.e.testTask(1); tk.Kind != task.Urgent {
		t.Fatalf("T1 应已升级为紧急")
	}
	g2.check(t, n2, "K", "S", state{onHand: 2, inTransit: 24, urgent: 24, reserve: 100, avail: 76, tasks: []int64{1}})
}
