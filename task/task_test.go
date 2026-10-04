package task

import (
	"testing"

	"ontology/slot"
)

func TestChainsAndUpgrade(t *testing.T) {
	st := slot.NewStore()
	sl, err := st.AddSlot("L", "S", 1, 100, 200, 10, 5)
	if err != nil {
		t.Fatal(err)
	}
	mg := NewManager(st)

	t1 := mg.Create("L", "S", 10, Regular)
	t2 := mg.Create("L", "S", 20, Regular)
	t3 := mg.Create("L", "S", 30, Urgent)
	if t1.ID != 1 || t2.ID != 2 || t3.ID != 3 {
		t.Fatalf("任务号应严格递增, got %d %d %d", t1.ID, t2.ID, t3.ID)
	}
	if sl.InTransit != 60 || st.SKUUsed("S") != 60 {
		t.Fatalf("在途占用应增量为 60, got it=%d used=%d", sl.InTransit, st.SKUUsed("S"))
	}

	// 紧急在前、同类按号升序。
	got := mg.OpenTasks()
	if len(got) != 3 || got[0].ID != 3 || got[1].ID != 1 || got[2].ID != 2 {
		ids := make([]int64, len(got))
		for i, x := range got {
			ids[i] = x.ID
		}
		t.Fatalf("排序错误: %v", ids)
	}

	// onHand=5，need=20：升级 T1(10) 后 5+10=15<20，再升级 T2(20) 达 35>=20 即停。
	ups := mg.UpgradeRegular("L", 5, 20)
	if len(ups) != 2 || ups[0].ID != 1 || ups[1].ID != 2 {
		t.Fatalf("应按序升级 T1,T2, got %v", ups)
	}
	for _, x := range ups {
		if x.Kind != Urgent {
			t.Fatalf("升级后应为紧急: #%d", x.ID)
		}
	}

	// 摘除 T3 后释放 30，在途余 30。
	st.ApplyCancel(sl, t3.Qty)
	mg.Remove(t3)
	if sl.InTransit != 30 {
		t.Fatalf("摘除后在途应为 30, got %d", sl.InTransit)
	}
	if len(mg.OpenTasks()) != 2 {
		t.Fatalf("剩余 2 条未完成")
	}
	if got := mg.Get(3); got != t3 {
		t.Fatalf("摘除后记录应保留以供终态判定")
	}
}

func TestUpgradeStopsWhenEnough(t *testing.T) {
	st := slot.NewStore()
	if _, err := st.AddSlot("L", "S", 1, 100, 200, 10, 8); err != nil {
		t.Fatal(err)
	}
	mg := NewManager(st)
	mg.Create("L", "S", 10, Regular)
	mg.Create("L", "S", 10, Regular)
	// onHand=8，need=18：8+10=18 恰好即停，只升级第一条。
	ups := mg.UpgradeRegular("L", 8, 18)
	if len(ups) != 1 || ups[0].ID != 1 {
		t.Fatalf("恰好够用应只升级 T1, got %v", ups)
	}
	// 顺序：紧急 T1 在前，常规 T2 在后。
	got := mg.OpenTasks()
	if got[0].ID != 1 || got[0].Kind != Urgent || got[1].ID != 2 {
		t.Fatalf("升级后链顺序错误")
	}
}
