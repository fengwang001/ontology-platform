package toollife_test

import (
	"fmt"

	"ontology/toollife"
)

// ExampleService 演示一次完整的申请、记账、破损换新与查询流程。
func ExampleService() {
	svc := toollife.New()

	// 刀组 G：按件计寿命，单刀上限 100 件，80% 预警，严格模式，两把可互换的刀。
	err := svc.AddGroup("G", toollife.GroupConfig{
		Basis:        toollife.ByPieces,
		LifeLimit:    100,
		WarnPermille: 800,
		Mode:         toollife.Strict,
		ToolIDs:      []string{"T1", "T2"},
	})
	fmt.Println("add group:", err)

	// 通道申请预计消耗 40 件。
	r, err := svc.Apply("G", "channel-7/req-1", 40)
	fmt.Printf("apply: tool=%s reserved=%d err=%v\n", r.ToolID, r.Reserved, err)

	// 同一申请编号重复提交：回放原结果，不重复预占。
	r2, _ := svc.Apply("G", "channel-7/req-1", 40)
	fmt.Printf("replay: tool=%s replayed=%v\n", r2.ToolID, r2.Replayed)

	// 实际只加工了 35 件：预占转已用，5 件差额释放；未到预警线。
	st, err := svc.Settle("channel-7/req-1", 35)
	fmt.Printf("settle: actual=%d warned=%v exhausted=%v err=%v\n", st.Actual, st.Warned, st.Exhausted, err)

	// T1 破损后换新（无未结算预占时允许），新刀占据原顺序位置。
	fmt.Println("broken:", svc.ReportBroken("G", "T1"))
	fmt.Println("replace:", svc.Replace("G", "T1", "T1-new"))

	v, _ := svc.Query("G")
	for _, t := range v.Tools {
		fmt.Printf("tool %s: status=%s used=%d reserved=%d remaining=%d\n",
			t.ID, t.Status, t.Used, t.Reserved, t.Remaining)
	}
	fmt.Println("current pick:", v.CurrentPick)

	// Output:
	// add group: <nil>
	// apply: tool=T1 reserved=40 err=<nil>
	// replay: tool=T1 replayed=true
	// settle: actual=35 warned=false exhausted=false err=<nil>
	// broken: <nil>
	// replace: <nil>
	// tool T1-new: status=available used=0 reserved=0 remaining=100
	// tool T2: status=available used=0 reserved=0 remaining=100
	// current pick: T1-new
}
