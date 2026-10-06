// 命令行演示：构造三分区车厢，依次演示装货确定位置、四类失败归并、
// 批量全有或全无、卸货顺序校验与中途装货停靠点已过。
package main

import (
	"errors"
	"fmt"

	"ontology/van"
)

func show(s *van.Service, title string) {
	fmt.Printf("--- %s（已到达停靠点 %d）---\n", title, s.ArrivedStop())
	for i, r := range s.Remaining() {
		fmt.Printf("  分区%d 剩余载重=%d克 剩余容积=%dcm³\n", i+1, r.Weight, r.Volume)
	}
}

func load(s *van.Service, c van.Cargo) {
	z, err := s.Load(c)
	if err == nil {
		fmt.Printf("装入 %s -> 分区%d\n", c, z)
		return
	}
	var re *van.RejectError
	switch {
	case errors.As(err, &re):
		fmt.Printf("拒绝 %s -> 原因: %s\n", c, re.Reason)
	case errors.Is(err, van.ErrDuplicate):
		fmt.Printf("拒绝 %s -> 编号重复\n", c)
	case errors.Is(err, van.ErrStopPassed):
		fmt.Printf("拒绝 %s -> 停靠点已过\n", c)
	default:
		fmt.Printf("拒绝 %s -> %v\n", c, err)
	}
}

func main() {
	s := van.New([]van.ZoneSpec{
		{WeightLimit: 100, VolumeLimit: 100},
		{WeightLimit: 100, VolumeLimit: 100},
		{WeightLimit: 100, VolumeLimit: 100},
	})

	// 后卸货物先装，占编号最小分区。
	load(s, van.Cargo{ID: 1, Weight: 30, Volume: 30, Stop: 3, Kind: van.General})
	load(s, van.Cargo{ID: 2, Weight: 40, Volume: 40, Stop: 2, Kind: van.Food})
	// 易燃与食品隔离：被推到不与食品同区的分区。
	load(s, van.Cargo{ID: 3, Weight: 20, Volume: 20, Stop: 3, Kind: van.Flammable})
	show(s, "单件装货后")

	// 氧化货物停3：分区1食品/氧化隔离；分区2、分区3虽空，但停2食品已在更靠前的分区1，
	// 停3货物进后区会违反后卸靠前。故唯一顺序可行的分区1又隔离冲突 -> 整体报隔离。
	load(s, van.Cargo{ID: 4, Weight: 10, Volume: 10, Stop: 3, Kind: van.Oxidizer})
	// 靠容量溢出把货物放到分区2，演示确定的多分区定位。
	load(s, van.Cargo{ID: 5, Weight: 40, Volume: 1, Stop: 2, Kind: van.General})
	show(s, "单件装货后")

	// 批量装货：第一件超重导致第二件失败 -> 全有或全无。
	_, err := s.LoadBatch([]van.Cargo{
		{ID: 10, Weight: 95, Volume: 1, Stop: 3, Kind: van.General},
		{ID: 11, Weight: 95, Volume: 1, Stop: 3, Kind: van.General},
	})
	if be := new(van.BatchError); errors.As(err, &be) {
		fmt.Printf("批量失败于下标 %d：%v（车厢无变化）\n", be.Index, be.Err)
	}

	// 越级卸货：停靠点1仍在车上时卸3 -> 顺序错误。
	if _, err := s.Unload(3); errors.Is(err, van.ErrOrderError) {
		fmt.Println("卸停靠点3 -> 顺序错误")
	}
	// 按序卸货。
	ids, _ := s.Unload(1)
	fmt.Printf("卸停靠点1 -> 货物 %v（空停靠点也推进进度）\n", ids)
	ids, _ = s.Unload(2)
	fmt.Printf("卸停靠点2 -> 货物 %v\n", ids)
	// 中途装货：停靠点2已过，新货停2 -> 停靠点已过。
	load(s, van.Cargo{ID: 20, Weight: 5, Volume: 5, Stop: 2, Kind: van.General})
	// 未来停靠点可以装。
	load(s, van.Cargo{ID: 21, Weight: 5, Volume: 5, Stop: 4, Kind: van.General})
	ids, _ = s.Unload(3)
	fmt.Printf("卸停靠点3 -> 货物 %v\n", ids)
	// 重复卸货 -> 已处理。
	if _, err := s.Unload(2); errors.Is(err, van.ErrProcessed) {
		fmt.Println("再卸停靠点2 -> 已处理")
	}
	show(s, "全部卸货后")
}
