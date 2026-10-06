// gatesim 用一段确定性脚本演示登机口分配、拆段、延误挤占与拒绝裁决，
// 并打印每步输入、输出与判定依据。
package main

import (
	"fmt"

	gate "ontology/gate"
)

func main() {
	cfg := gate.Config{Buffer: 10, MaxStay: 120, DeplaneDur: 20, BoardingDur: 30}
	gates := []gate.GateSpec{
		{ID: "G1", MaxClass: gate.Class3, Kind: gate.GateDual},
		{ID: "G2", MaxClass: gate.Class3, Kind: gate.GateDual},
		{ID: "G3", MaxClass: gate.Class2, Kind: gate.GateDomestic},
	}
	flights := []gate.FlightSpec{
		{ID: "CA101", Class: gate.Class3, Kind: gate.KindIntl, SchedArr: 100, SchedDep: 200, BoardLead: 40},
		{ID: "CA202", Class: gate.Class2, Kind: gate.KindDomestic, SchedArr: 150, SchedDep: 240, BoardLead: 30},
		{ID: "CA303", Class: gate.Class3, Kind: gate.KindDomestic, SchedArr: 300, SchedDep: 600, BoardLead: 40},
	}
	sys, err := gate.New(cfg, gates, flights, []gate.Adj{{A: "G1", B: "G2"}})
	must(err)

	step := func(title string) { fmt.Printf("\n== %s ==\n", title) }

	step("指派 CA101(国际三级) 到 G1")
	printAssign(sys.Assign(0, "CA101", "G1", gate.SegWhole))

	step("指派 CA202(国内二级) 到 G3（国内专用）")
	printAssign(sys.Assign(0, "CA202", "G3", gate.SegWhole))

	step("CA101 改派到 G3：三级航班->二级口机型不兼容（先于属性判定）应拒绝，原指派保留")
	printAssign(sys.Assign(1, "CA101", "G3", gate.SegWhole))

	step("CA303 长过站（占用 310 分钟 > 上限 120）初始即为两段，指派卸客段到 G2")
	if split, _ := sys.IsSplit("CA303"); !split {
		panic("CA303 should be split")
	}
	printAssign(sys.Assign(0, "CA303", "G2", gate.SegDeplane))

	step("为 CA303 登机段指派 G2：同一登机口两段不相交（卸客[300,320) 登机[570,610)）")
	printAssign(sys.Assign(0, "CA303", "G2", gate.SegBoarding))

	step("t=120 CA202 起飞延误到 300：跨过上限自动拆段，G3 保留给卸客段")
	printDelay(sys.Delay(120, "CA202", gate.MaskDep, 0, 300))

	step("拆段后为 CA202 登机段指派 G3：[270,310) 与卸客段[150,170) 不相交")
	printAssign(sys.Assign(120, "CA202", "G3", gate.SegBoarding))

	step("t=5 时钟回退到更早时刻应被拒绝")
	printAssign(sys.Assign(5, "CA202", "G3", gate.SegWhole))

	step("t=280 CA202 已开始登机（300-30=270），再改起飞时刻应被拒绝")
	printDelay(sys.Delay(280, "CA202", gate.MaskDep, 0, 320))

	fmt.Println("\n== 最终快照 ==")
	snap := sys.Snapshot()
	fmt.Printf("now=%d\n", snap.Now)
	for id, f := range snap.Flights {
		fmt.Printf("flight %s arr=%d dep=%d split=%v deplane=%q boarding=%q\n",
			id, f.Arr, f.Dep, f.Split, f.DeplaneGate, f.BoardingGate)
	}
	for _, a := range snap.Assignments {
		fmt.Printf("assign %s/%s -> %s [%d,%d)\n", a.Flight, a.Seg, a.Gate, a.Start, a.End)
	}
}

func printAssign(r gate.AssignResult) {
	if r.OK {
		fmt.Println("result: ACCEPT")
		return
	}
	fmt.Printf("result: REJECT reason=%s conflict=%q\n", r.Err.Reason, r.Err.ConflictID)
}

func printDelay(r gate.DelayResult) {
	if r.OK {
		fmt.Printf("result: ACCEPT becameSplit=%v evictions=%v\n", r.BecameSplit, r.Evicted)
		return
	}
	fmt.Printf("result: REJECT reason=%s\n", r.Err.Reason)
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
