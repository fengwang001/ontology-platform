// vanload-demo 演示配载与卸货顺序校验：日志打印输入、输出与判定依据。
package main

import (
	"fmt"

	"ontology/vanload"
)

func main() {
	// 3 个分区：车头→车尾依次为 1,2,3。
	cmps := []vanload.Compartment{
		{MaxWeight: 1000, MaxVolume: 2000},
		{MaxWeight: 800, MaxVolume: 1500},
		{MaxWeight: 800, MaxVolume: 1500},
	}
	logger := vanload.NewTextLogger(func(line string) { fmt.Println(line) })
	sys, err := vanload.New(cmps, vanload.WithLogger(logger))
	if err != nil {
		panic(err)
	}

	// 晚卸货物（停靠点5）优先占据小编号分区。
	_, _ = sys.Load(vanload.Cargo{ID: 1, Weight: 900, Volume: 1000, Stop: 5, Category: vanload.CategoryGeneral})

	// 批量装货：普通件与食品件，停靠点1。
	_, _ = sys.BatchLoad([]vanload.Cargo{
		{ID: 10, Weight: 300, Volume: 500, Stop: 1, Category: vanload.CategoryGeneral},
		{ID: 11, Weight: 300, Volume: 500, Stop: 1, Category: vanload.CategoryFood},
	})

	// 易燃品：受顺序约束不能进被晚卸货物“挡住”的分区，且受隔离约束避开食品。
	_, err = sys.Load(vanload.Cargo{ID: 20, Weight: 200, Volume: 200, Stop: 1, Category: vanload.CategoryFlammable})
	fmt.Println("易燃品结果:", resultOr(err))

	// 卸货：必须按序号递增。
	if r, e := sys.Unload(3); e != nil {
		fmt.Println("卸3被拒绝:", e)
	} else {
		fmt.Printf("卸%d: %s 卸下%v\n", r.Stop, r.Status, r.Removed)
	}
	if r, e := sys.Unload(1); e == nil {
		fmt.Printf("卸%d: %s 卸下%v\n", r.Stop, r.Status, r.Removed)
	}
	if r, e := sys.Unload(1); e == nil {
		fmt.Printf("重复卸%d: %s\n", r.Stop, r.Status)
	}

	// 中途装货：停靠点必须大于已到达最大序号。
	_, err = sys.Load(vanload.Cargo{ID: 30, Weight: 10, Volume: 10, Stop: 1, Category: vanload.CategoryGeneral})
	fmt.Println("中途装停靠点1:", resultOr(err))

	// 只读查询快照。
	for _, cap := range sys.RemainingCapacities() {
		fmt.Printf("分区%d 剩余载重=%d 剩余容积=%d\n",
			cap.Compartment, cap.RemainWeight, cap.RemainVolume)
	}
	fmt.Println("已到达最大停靠点:", sys.ArrivedStop())
	if k, ok := sys.Locate(1); ok {
		fmt.Println("货物1当前分区:", k)
	}
}

func resultOr(err error) string {
	if err != nil {
		return err.Error()
	}
	return "成功"
}
