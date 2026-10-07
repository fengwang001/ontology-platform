// demo 运行一个端到端示例场景：登记住宿与病例、追补、改正、
// 查询状态与清单，并打印每一步的输入、输出与判定依据。
package main

import (
	"fmt"

	"ontology"
)

func main() {
	e := ontology.NewEngine()

	step := func(format string, args ...any) {
		fmt.Printf("== "+format+"\n", args...)
	}
	ok := func(err error) {
		if err != nil {
			fmt.Println("   拒绝:", err)
		}
	}
	status := func(p string, now int64) {
		st, err := e.Status(p, now)
		if err != nil {
			fmt.Printf("   Status(%s) 拒绝: %v\n", p, err)
			return
		}
		fmt.Printf("   Status(%s) @%d => %s，解除时刻 %d\n", p, now, st.Status, st.ReleaseAt)
		for _, s := range st.Sources {
			fmt.Printf("      依据: 病例 %s，等级 %s，最后接触 %d，解除 %d，期内=%v\n",
				s.CaseID, s.Level, s.LastContact, s.ReleaseAt, s.InPeriod)
		}
	}
	contacts := func(c string, now int64) {
		ct, err := e.CaseContacts(c, now)
		if err != nil {
			fmt.Printf("   CaseContacts(%s) 拒绝: %v\n", c, err)
			return
		}
		fmt.Printf("   CaseContacts(%s) @%d => 密接 %v，次密接 %v\n", c, now, ct.Close, ct.Secondary)
	}

	step("1. 登记住宿：P 住 W1 [1000,2000)，X 与 P 同室 [1000,1120)，Q 同室 [1000,1300)")
	ok(e.BackfillStay("P", "W1", 1000, 2000, 5000))
	ok(e.BackfillStay("X", "W1", 1000, 1120, 5000))
	ok(e.BackfillStay("Q", "W1", 1000, 1300, 5000))

	step("2. Q 后来在 W2 [3000,3400)，Z 同室 [3000,3400)")
	ok(e.BackfillStay("Q", "W2", 3000, 3400, 5000))
	ok(e.BackfillStay("Z", "W2", 3000, 3400, 5000))

	step("3. 登记病例 C1：P 发病于 1500，确诊登记时刻 5000")
	ok(e.RegisterCase("C1", "P", 1500, 5000))
	contacts("C1", 5000)
	status("X", 5000)
	status("Z", 5000)

	step("4. 追补 X 更晚一段同室住宿 [1900,2000)，最后接触时刻后移")
	ok(e.BackfillStay("X", "W1", 1900, 2000, 5000))
	status("X", 5000)

	step("5. 改正发病时刻为 5200（传染期起点 2320），接触者消失")
	ok(e.CorrectOnset("C1", 5200, 6000))
	status("X", 6000)
	contacts("C1", 6000)

	step("6. 改正回 1500，接触者恢复；登记隔离时刻 4000 截断传染期")
	ok(e.CorrectOnset("C1", 1500, 6000))
	ok(e.RegisterIsolation("C1", 4000, 6000))
	contacts("C1", 6000)

	step("7. 时间推进到解除时刻附近（恰到解除时刻即已解除）")
	status("Z", 3400+3*24*60-1)
	status("Z", 3400+3*24*60)
	status("X", 2000+7*24*60-1)
	status("X", 2000+7*24*60)

	step("8. 撤销病例 C1，接触者全部消失")
	ok(e.RevokeCase("C1", 2000+7*24*60))
	status("X", 2000+7*24*60)
	contacts("C1", 2000+7*24*60)

	step("9. 错误示例：空标识、时钟回退、撤销后登记隔离")
	ok(e.RegisterCase("", "P", 100, 2000+7*24*60))
	ok(e.RegisterCase("C2", "P", 100, 100))
	ok(e.RegisterIsolation("C1", 100, 2000+7*24*60))
}
