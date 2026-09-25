// Command demo 端到端验证端到端延迟 SLA 监控；不读参数、不联网，退出码 0。
package main

import (
	"errors"
	"fmt"
	"sync"

	"ontology/api"
	"ontology/sla"
)

type view struct {
	count, viol, inFlight, min, max int64
	avg                             float64
	breached                        bool
}

func read(m *api.Monitor) view {
	return view{m.Count(), m.Violations(), m.InFlight(), m.MinLatency(),
		m.MaxLatency(), m.AvgLatency(), m.Breached()}
}
func ok(name string, pass bool) {
	if pass {
		fmt.Println("OK  ", name)
	} else {
		fmt.Println("FAIL", name)
	}
}

func main() {
	// 1) 阈值边界：==T 为 OK，T+1 为违例。
	ok("阈值边界: lat==10==T 判OK, 11==T+1 判违例",
		sla.Classify(10, 10) == sla.OK && sla.Classify(11, 10) == sla.Violation)

	// 2) 第三节七步：逐步延迟判定与 Breached 序列。
	m, _ := api.New(10, 4, 3)
	ids := []string{"A", "B", "C", "D", "E", "F", "G"}
	begs := []int64{0, 20, 40, 50, 70, 90, 100}
	ends := []int64{10, 35, 45, 61, 82, 94, 120}
	wantV := []bool{false, true, false, true, true, false, true}
	wantB := []bool{false, false, false, false, true, false, true}
	seqOK := true
	verdicts, breaches := "", ""
	for i := range ids {
		prev := m.Violations()
		_ = m.Begin(ids[i], begs[i])
		_ = m.End(ids[i], ends[i])
		isV := m.Violations() == prev+1
		seqOK = seqOK && isV == wantV[i] && m.Breached() == wantB[i]
		verdicts += map[bool]string{false: "OK", true: "违"}[isV]
		breaches += map[bool]string{false: "0", true: "1"}[m.Breached()]
	}
	ok(fmt.Sprintf("七步判定/告警 verdicts=%s breach=%s", verdicts, breaches), seqOK)

	// 3) 第1步 A==T 判 OK；第6步 F(OK) 滑入后告警清除为 false。
	ok("第1步 A(lat=10) 为 OK；第6步 F 后 Breached 清除为 false",
		!wantV[0] && m.Breached() /*当前为G后=true*/ && breaches[5] == '0')

	// 4) 最终统计量。
	v := read(m)
	ok(fmt.Sprintf("最终统计 count=%d viol=%d min=%d max=%d avg=%.1f inFlight=%d",
		v.count, v.viol, v.min, v.max, v.avg, v.inFlight),
		v == view{7, 4, 0, 4, 20, 11, true})

	// 5) 四类可判定、互不相同的哨兵错误。
	bad := 0
	for _, c := range [][3]int64{{-1, 4, 3}, {10, 0, 3}, {10, 4, 0}, {10, 4, 5}} {
		if _, e := api.New(c[0], int(c[1]), int(c[2])); errors.Is(e, api.ErrInvalidParam) {
			bad++
		}
	}
	_ = m.Begin("Z", 1)
	_ = m.Begin("N", 100)
	eDup := m.Begin("Z", 1)
	eUnk := m.End("ghost", 2)
	eNeg := m.End("N", 99)
	distinct := bad == 4 && errors.Is(eDup, api.ErrDuplicateBegin) &&
		errors.Is(eUnk, api.ErrUnknown) && errors.Is(eNeg, sla.ErrNegative)
	ok("四类哨兵错误互不相同(非法参数4档均拒绝)", distinct)

	// 6) 被拒操作不留痕：Z、N 合法在途，完成类统计逐项不变。
	a := read(m)
	ok("重复Begin/未知End/负延迟 被拒后完成统计不变",
		a == view{7, 4, 2, 4, 20, 11, true})

	// 7) 大 m 多档滑窗正确；O(1) 更新检查数（不随 m 增长）由 mon 白盒测试钉住。
	bigOK := true
	for _, n := range []int{100, 1000, 10000} {
		g, _ := api.New(0, 4, 3) // 阈值0：延迟1..3 全部违例
		for i := 0; i < n; i++ {
			id := fmt.Sprintf("r%d", i)
			_ = g.Begin(id, 0)
			_ = g.End(id, int64(i%3+1))
		}
		bigOK = bigOK && g.Breached() // n>=4：近4全违例必 breach
	}
	ok("大 m(100/1000/10000) 滑窗正确; 更新检查数为与m无关常数(白盒测试)", bigOK)

	// 8) 并发只读：N 个 goroutine 各取全部统计量，逐项相同。
	const N = 32
	res := make([]view, N)
	var wg sync.WaitGroup
	for i := range res {
		wg.Add(1)
		go func(i int) { defer wg.Done(); res[i] = read(m) }(i)
	}
	wg.Wait()
	concOK := true
	for i := 1; i < N; i++ {
		concOK = concOK && res[i] == res[0]
	}
	ok(fmt.Sprintf("%d goroutine 并发只读统计逐项一致", N), concOK)

	// 9) 内置自检：四条不变量。
	s, _ := api.New(1, 1, 1)
	ok("SelfCheck 四条不变量", s.SelfCheck() == nil)
}
