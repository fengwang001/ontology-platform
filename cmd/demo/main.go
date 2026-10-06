// demo 演示货位分配系统的典型流程，并打印每个操作的输入、输出与判定依据。
package main

import (
	"fmt"
	"os"

	"ontology/slotting"
)

func main() {
	s := slotting.NewSystem(os.Stdout)

	locs := []slotting.Location{
		{ID: slotting.LocationID{Aisle: 1, Level: 1, Index: 1}, MaxWeight: 500, ClearHeight: 200,
			Allowed:  []slotting.Category{slotting.CatGeneral, slotting.CatFood},
			Capacity: 2, AllowMix: true},
		{ID: slotting.LocationID{Aisle: 1, Level: 1, Index: 2}, MaxWeight: 800, ClearHeight: 240,
			Allowed:  []slotting.Category{slotting.CatGeneral, slotting.CatFlammable},
			Capacity: 1, AllowMix: false},
		{ID: slotting.LocationID{Aisle: 1, Level: 1, Index: 3}, MaxWeight: 300, ClearHeight: 180,
			Allowed:  []slotting.Category{slotting.CatGeneral},
			Capacity: 1, AllowMix: false},
		{ID: slotting.LocationID{Aisle: 2, Level: 1, Index: 1}, MaxWeight: 800, ClearHeight: 240,
			Allowed:  []slotting.Category{slotting.CatGeneral, slotting.CatFlammable},
			Capacity: 1, AllowMix: false},
	}
	for _, l := range locs {
		must(s.AddLocation(l))
	}

	pallets := []slotting.Pallet{
		{ID: "T001", Product: "矿泉水", Batch: "B1", Category: slotting.CatFood, Weight: 120, Height: 100},
		{ID: "T002", Product: "矿泉水", Batch: "B2", Category: slotting.CatFood, Weight: 130, Height: 110},
		{ID: "T003", Product: "酒精", Batch: "B9", Category: slotting.CatFlammable, Weight: 200, Height: 150},
		{ID: "T004", Product: "纸箱", Batch: "B3", Category: slotting.CatGeneral, Weight: 50, Height: 160},
	}
	for _, p := range pallets {
		must(s.RegisterPallet(p))
	}

	fmt.Println("--- 自动上架 ---")
	id, err := s.AutoPlace("T001")
	must(err)
	fmt.Printf("T001 -> %s\n", id)

	id, err = s.AutoPlace("T002") // 同商品、允许混批 -> 合并到同一货位
	must(err)
	fmt.Printf("T002 -> %s\n", id)

	id, err = s.AutoPlace("T003") // 易燃：与食品隔通道存放
	must(err)
	fmt.Printf("T003 -> %s\n", id)

	fmt.Println("--- 指定上架被混放规则拒绝（不同商品想并入 1,1,1）---")
	if err := s.PlaceTo("T004", slotting.LocationID{Aisle: 1, Level: 1, Index: 1}); err != nil {
		fmt.Printf("拒绝（原因可区分）: %v\n", err)
	}
	id, err = s.AutoPlace("T004") // 普通品类最小空货位 (1,1,3)
	must(err)
	fmt.Printf("T004 自动改派 -> %s\n", id)

	fmt.Println("--- 批量（全有或全无）---")
	must(s.RegisterPallet(slotting.Pallet{ID: "T005", Product: "纸箱", Batch: "B3",
		Category: slotting.CatGeneral, Weight: 10, Height: 10}))
	must(s.RegisterPallet(slotting.Pallet{ID: "T006", Product: "纸箱", Batch: "B3",
		Category: slotting.CatGeneral, Weight: 10, Height: 10}))
	idx, berr := s.BatchAutoPlace([]string{"T005", "T006"}) // 普通货位已满 -> 失败回滚
	fmt.Printf("批量结果: idx=%d err=%v（系统状态不变）\n", idx, berr)

	fmt.Println("--- 查询快照 ---")
	v, err := s.GetLocation(slotting.LocationID{Aisle: 1, Level: 1, Index: 1})
	must(err)
	fmt.Printf("货位(1,1,1) 在位=%v 已用承重=%d 剩余承重=%d\n",
		v.PalletIDs, v.UsedWeight, v.RemainWeight)
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}
