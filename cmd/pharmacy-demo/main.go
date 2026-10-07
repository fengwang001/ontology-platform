// 门诊药房库存预留与欠药补发系统的演示程序：
// 跑通“整盒取整 → 部分发药与欠药 → 到货补发 → 预留超时级联”的完整链路。
package main

import (
	"fmt"

	"ontology/pharmacy"
)

func check(err error) {
	if err != nil {
		fmt.Println("  错误:", err)
	}
}

func printDrug(e *pharmacy.Engine, now int, id string) {
	d, err := e.QueryDrug(now, id)
	if err != nil {
		fmt.Println("  查询失败:", err)
		return
	}
	fmt.Printf("  药品 %s: 在库=%d 有效预留=%d 可用=%d 欠药总量=%d\n",
		id, d.OnHand, d.Reserved, d.Available, d.BackorderTotal)
}

func printRx(e *pharmacy.Engine, now int, id string) {
	p, err := e.QueryPrescription(now, id)
	if err != nil {
		fmt.Println("  查询失败:", err)
		return
	}
	fmt.Printf("  处方 %s [%s]:\n", id, p.Status)
	for _, l := range p.Lines {
		fmt.Printf("    行 %s: 需求=%d 预留=%d 已发放=%d 欠药=%d 状态=%s\n",
			l.DrugID, l.Demand, l.Reserved, l.Dispensed, l.Backorder, l.Status)
	}
}

func main() {
	e, err := pharmacy.NewEngine(pharmacy.Config{R: 10})
	if err != nil {
		panic(err)
	}

	fmt.Println("== t=0 登记药品（阿莫西林：整盒 4 粒、不可拆零；布洛芬：可拆零）并入库 ==")
	check(e.RegisterDrug(0, "阿莫西林", 4, false))
	check(e.RegisterDrug(0, "布洛芬", 1, true))
	check(e.Inbound(0, "阿莫西林", 6))
	check(e.Inbound(0, "布洛芬", 3))

	fmt.Println("== t=1 受理处方 rx1：阿莫西林 5 粒（向上取整预留 8? 可用 6 → 向下取整 4，欠 1）+ 布洛芬 10（欠 7） ==")
	check(e.AcceptPrescription(1, pharmacy.RxInput{
		ID: "rx1", Patient: "张三", IssueTime: 1,
		Lines: []pharmacy.LineInput{{DrugID: "阿莫西林", Qty: 5}, {DrugID: "布洛芬", Qty: 10}},
	}))
	printRx(e, 1, "rx1")

	fmt.Println("== t=2 取药（取走当前有效预留，欠药保持） ==")
	check(e.Dispense(2, "rx1"))
	printRx(e, 2, "rx1")

	fmt.Println("== t=3 到货：布洛芬 7、阿莫西林 4 → 按欠药登记次序补发 ==")
	check(e.Inbound(3, "布洛芬", 7))
	check(e.Inbound(3, "阿莫西林", 4))
	printRx(e, 3, "rx1")
	printDrug(e, 3, "阿莫西林")

	fmt.Println("== t=4 再次取药 → 处方完成 ==")
	check(e.Dispense(4, "rx1"))
	printRx(e, 4, "rx1")

	fmt.Println("== t=5 整单处方 rx2（布洛芬 100）因缺药被整单拒绝，无副作用 ==")
	check(e.AcceptPrescription(5, pharmacy.RxInput{
		ID: "rx2", Patient: "李四", IssueTime: 5, WholeOrder: true,
		Lines: []pharmacy.LineInput{{DrugID: "布洛芬", Qty: 100}},
	}))
	printDrug(e, 5, "布洛芬")

	fmt.Println("== t=5 布洛芬到货 5；受理 rx3（布洛芬 2，R=10 失效时刻 16） ==")
	check(e.Inbound(5, "布洛芬", 5))
	check(e.AcceptPrescription(5, pharmacy.RxInput{
		ID: "rx3", Patient: "王五", IssueTime: 5,
		Lines: []pharmacy.LineInput{{DrugID: "布洛芬", Qty: 2}},
	}))
	fmt.Println("  t=15（第 R 分钟，预留仍有效）:")
	printDrug(e, 15, "布洛芬")
	fmt.Println("  t=16（晚一分钟，预留自动失效回到可用量）:")
	printDrug(e, 16, "布洛芬")
	check(e.Dispense(16, "rx3"))
}
