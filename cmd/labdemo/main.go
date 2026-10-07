// labdemo 演示临床检验标本采集、送检、签收与拒收系统的主要流程，
// 并在两档历史规模上对照签收与查询的开销。
package main

import (
	"fmt"
	"time"

	"ontology/lab"
)

func check(err error) {
	if err != nil {
		fmt.Println("  错误:", err)
	}
}

func check2(_ any, err error) { check(err) }

func scenario() {
	s := lab.NewSystem()
	fmt.Println("== 场景：采集 -> 送检 -> 签收/拒收 -> 重采合并 -> 终止 ==")

	check(s.RegisterItem(0, "glu", "tubeA", 100, false, 2))
	check(s.RegisterItem(0, "k", "tubeA", 100, true, 2))
	check(s.RegisterItem(0, "na", "tubeA", 100, false, 2))
	fmt.Println("已登记项目: glu(常温) k(冷藏) na(常温)，管 tubeA，时限 100s，容忍溶血 2")

	check(s.SubmitApplication(10, "app1", "p1", []string{"glu", "k"}, 1))
	check(s.SubmitApplication(11, "app2", "p1", []string{"na"}, 2))
	fmt.Println("已提交申请: app1{glu,k} app2{na}")

	check(s.Collect(20, "tube1", "tubeA", "p1", []string{"glu", "k"}, 20))
	check(s.RegisterTransport(21, "tube1", false))
	fmt.Println("tube1 采集 {glu,k}，常温运送")
	verdicts, err := s.Sign(30, "tube1", 1)
	check(err)
	fmt.Println("签收 tube1:", verdicts, "// k 要求冷藏被拒，glu 合格")

	check(s.Collect(40, "tube2", "tubeA", "p1", []string{"k", "na"}, 40))
	fmt.Println("tube2 跨申请合并重采 {k(app1),na(app2)}")
	check(s.RegisterTransport(41, "tube2", true))
	verdicts, err = s.Sign(50, "tube2", 3)
	check(err)
	fmt.Println("签收 tube2:", verdicts, "// 溶血 3 超容忍 2，双双被拒回待采集")

	for round := 3; ; round++ {
		tubeID := fmt.Sprintf("tube%d", round)
		now := int64(50 + round*10)
		if err := s.Collect(now, tubeID, "tubeA", "p1", []string{"k", "na"}, now); err != nil {
			break
		}
		verdicts, err := s.Sign(now+1, tubeID, 4)
		check(err)
		fmt.Printf("签收 %s: %v\n", tubeID, verdicts)
		done := true
		for _, v := range verdicts {
			if v.NewStatus == lab.StatusPending {
				done = false
			}
		}
		if done {
			break
		}
	}

	views, err := s.QueryPatient(200, "p1")
	check(err)
	fmt.Println("患者 p1 未终结项目:", views, "// 累计拒收达 3 次的项目已终止")
}

func complexity() {
	fmt.Println("== 两档规模复杂度对照（扫描记录数与墙钟时间）==")
	for _, n := range []int{2_000, 40_000} {
		s, now := buildHistory(n)
		s.Metrics.ResetItemsScanned()
		start := time.Now()
		check2(s.Sign(now, "tube-target", 0))
		signScan, signDur := s.Metrics.ItemsScanned(), time.Since(start)

		s.Metrics.ResetItemsScanned()
		start = time.Now()
		check2(s.QueryPatient(now, "p1"))
		queryScan, queryDur := s.Metrics.ItemsScanned(), time.Since(start)

		fmt.Printf("历史规模=%6d: 签收扫描=%d 耗时=%v; 查询扫描=%d 耗时=%v\n",
			n, signScan, signDur, queryScan, queryDur)
	}
}

// buildHistory 构造一名患者：finished 个已终结项目、7 个待采集项目、
// 一支含 5 个项目的待签收管。
func buildHistory(finished int) (*lab.System, int64) {
	s := lab.NewSystem()
	total := finished + 7 + 5
	for i := 0; i < total; i++ {
		check(s.RegisterItem(0, fmt.Sprintf("item%d", i), "tubeA", 1<<40, false, 4))
	}
	now := int64(1)
	idx := 0
	for idx < finished {
		var items []string
		for j := 0; j < 10 && idx < finished; j++ {
			items = append(items, fmt.Sprintf("item%d", idx))
			idx++
		}
		check(s.SubmitApplication(now, fmt.Sprintf("app-h%d", idx), "p1", items, 1))
		check(s.Collect(now, fmt.Sprintf("tube-h%d", idx), "tubeA", "p1", items, now))
		check2(s.Sign(now, fmt.Sprintf("tube-h%d", idx), 0))
		now++
	}
	var pending []string
	for j := 0; j < 7; j++ {
		pending = append(pending, fmt.Sprintf("item%d", idx))
		idx++
	}
	check(s.SubmitApplication(now, "app-pending", "p1", pending, 1))
	now++
	var inTube []string
	for j := 0; j < 5; j++ {
		inTube = append(inTube, fmt.Sprintf("item%d", idx))
		idx++
	}
	check(s.SubmitApplication(now, "app-tube", "p1", inTube, 1))
	check(s.Collect(now, "tube-target", "tubeA", "p1", inTube, now))
	now++
	return s, now
}

func main() {
	scenario()
	fmt.Println()
	complexity()
}
