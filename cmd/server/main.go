// 命令 server 是特种设备检验周期与超期管控系统的演示程序：
// 构造一套类别配置，顺序执行一组典型操作并打印每步的输入、
// 输出与判定依据，最后打印一次预警查询结果。
package main

import (
	"fmt"
	"log"

	"ontology/calendar"
	"ontology/domain"
	"ontology/system"
)

func d(y, m, day int) int { return calendar.FromCivil(y, m, day) }

func ymd(z int) string {
	y, m, dd := calendar.ToCivil(z)
	return fmt.Sprintf("%04d-%02d-%02d", y, m, dd)
}

func step(name string, err error) {
	if err != nil {
		fmt.Printf("%-40s => 拒绝: %v\n", name, err)
	} else {
		fmt.Printf("%-40s => 接受\n", name)
	}
}

func main() {
	configs := map[domain.Category]domain.Config{
		domain.CatBoiler:         {PeriodMonths: 12, EarlyWindowDays: 30, MinUnsealDays: 10, WarnAheadDays: 60},
		domain.CatPressureVessel: {PeriodMonths: 24, EarlyWindowDays: 30, MinUnsealDays: 10, WarnAheadDays: 60},
		domain.CatSafetyValve:    {PeriodMonths: 6, EarlyWindowDays: 15, MinUnsealDays: 5, WarnAheadDays: 30},
		domain.CatPressureGauge:  {PeriodMonths: 6, EarlyWindowDays: 15, MinUnsealDays: 5, WarnAheadDays: 30},
	}
	sys, err := system.New(configs)
	if err != nil {
		log.Fatalf("初始化失败: %v", err)
	}

	fmt.Println("== 登记 ==")
	step("Register(B1, boiler, 2024-01-01)", sys.Register("B1", domain.CatBoiler, d(2024, 1, 1)))
	step("Register(V1, safety_valve, 2024-01-01)", sys.Register("V1", domain.CatSafetyValve, d(2024, 1, 1)))
	step("Attach(B1, V1, 2024-01-02)", sys.Attach("B1", "V1", d(2024, 1, 2)))

	fmt.Println("== 使用登记 ==")
	use := func(id string, date int) {
		ok, denial, err := sys.RegisterUse(id, date)
		switch {
		case err != nil:
			fmt.Printf("RegisterUse(%s, %s) => 错误: %v\n", id, ymd(date), err)
		case !ok:
			fmt.Printf("RegisterUse(%s, %s) => 拒绝: %s\n", id, ymd(date), denial.Detail)
		default:
			fmt.Printf("RegisterUse(%s, %s) => 接受\n", id, ymd(date))
		}
	}
	use("B1", d(2024, 6, 1))

	fmt.Println("== 封存 / 启封 ==")
	step("Seal(V1, 2024-06-02)", sys.Seal("V1", d(2024, 6, 2)))
	use("B1", d(2024, 6, 3)) // 附件封存 -> 设备不可用
	step("Inspect(V1, 2024-06-04, pass)", sys.Inspect("V1", d(2024, 6, 4), domain.ResultPass, 0))
	step("Unseal(V1, 2024-06-10)", sys.Unseal("V1", d(2024, 6, 10)))
	use("B1", d(2024, 6, 11))

	fmt.Println("== 检验（提前窗口 / 超期） ==")
	step("Inspect(B1, 2024-12-15, pass) 窗口内", sys.Inspect("B1", d(2024, 12, 15), domain.ResultPass, 0))
	step("Inspect(V1, 2025-03-01, conditional, 20)", sys.Inspect("V1", d(2025, 3, 1), domain.ResultConditional, 20))

	fmt.Println("== 预警（2025-03-05 起） ==")
	entries, err := sys.Warn(d(2025, 3, 5))
	if err != nil {
		log.Fatalf("Warn: %v", err)
	}
	for _, e := range entries {
		fmt.Printf("  %-6s %-14s 到期 %s 触发附件 %v\n", e.ID, e.Cat, ymd(e.Expiry), e.Via)
	}

	fmt.Println("== 报废 ==")
	step("Scrap(B1, 2025-03-02)", sys.Scrap("B1", d(2025, 3, 2)))
	step("Inspect(B1, 2025-03-03, pass)", sys.Inspect("B1", d(2025, 3, 3), domain.ResultPass, 0))
}
