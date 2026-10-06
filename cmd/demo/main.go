// Command demo 演示货位分配系统：构造货位，执行自动/指定上架、移库、冻结、
// 批量上架与查询，通过日志装饰器打印输入、输出与判定依据。
package main

import (
	"os"

	"ontology/slotting"
)

func main() {
	svc := slotting.NewService(slotting.NewStore())
	add := func(c slotting.LocationConfig) {
		if err := svc.Store().AddLocation(c); err != nil {
			panic(err)
		}
	}
	all := map[slotting.Category]bool{
		slotting.CategoryNormal:    true,
		slotting.CategoryFood:      true,
		slotting.CategoryFlammable: true,
	}
	add(slotting.LocationConfig{Coord: slotting.Coord{Aisle: 1, Level: 1, Position: 1},
		WeightLimit: 300, ClearHeight: 120, Allowed: all, Capacity: 2, AllowMixedBatch: true})
	add(slotting.LocationConfig{Coord: slotting.Coord{Aisle: 1, Level: 1, Position: 2},
		WeightLimit: 300, ClearHeight: 120, Allowed: all, Capacity: 1, AllowMixedBatch: true})
	add(slotting.LocationConfig{Coord: slotting.Coord{Aisle: 2, Level: 1, Position: 1},
		WeightLimit: 100, ClearHeight: 80,
		Allowed:  map[slotting.Category]bool{slotting.CategoryNormal: true},
		Capacity: 2, AllowMixedBatch: false})

	log := slotting.NewLoggedService(svc, os.Stdout)

	p1 := slotting.Pallet{ID: "P-1", Product: "MILK", Batch: "B01",
		Category: slotting.CategoryFood, Weight: 100, Height: 100}
	p2 := slotting.Pallet{ID: "P-2", Product: "MILK", Batch: "B02",
		Category: slotting.CategoryFood, Weight: 120, Height: 90}

	log.AutoPutaway(p1)
	log.AutoPutaway(p2)

	// 不同批次进不允许混批的货位 -> 混放冲突。
	log.PutawayTo(slotting.Pallet{ID: "P-3", Product: "RICE", Batch: "B9",
		Category: slotting.CategoryNormal, Weight: 40, Height: 70},
		slotting.Coord{Aisle: 2, Level: 1, Position: 1})
	log.PutawayTo(slotting.Pallet{ID: "P-4", Product: "RICE", Batch: "B8",
		Category: slotting.CategoryNormal, Weight: 40, Height: 70},
		slotting.Coord{Aisle: 2, Level: 1, Position: 1})

	// 易燃品不得与食品相邻。
	log.PutawayTo(slotting.Pallet{ID: "F-1", Product: "ALCOHOL", Batch: "B1",
		Category: slotting.CategoryFlammable, Weight: 30, Height: 60},
		slotting.Coord{Aisle: 1, Level: 1, Position: 2})

	log.Move("P-2", slotting.Coord{Aisle: 2, Level: 1, Position: 1})
	log.Freeze(slotting.Coord{Aisle: 2, Level: 1, Position: 1})
	log.BatchPutaway([]slotting.Pallet{
		{ID: "Q-1", Product: "OIL", Batch: "B1",
			Category: slotting.CategoryNormal, Weight: 10, Height: 10},
		{ID: "Q-2", Product: "OIL", Batch: "B2",
			Category: slotting.CategoryNormal, Weight: 10, Height: 10},
	})
	log.Location(slotting.Coord{Aisle: 1, Level: 1, Position: 1})
	log.PalletLocation("P-1")
	log.ProductLocations("MILK")
}
