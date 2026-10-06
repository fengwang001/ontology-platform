// Command demo 演示特种设备检验周期与超期管控系统的完整业务流，
// 打印每一步输入、输出与判定依据。
package main

import (
	"fmt"

	"ontology/speq"
)

func mustOrd(y, m, d int) int {
	o, ok := speq.DateToOrdinal(y, m, d)
	if !ok {
		panic("bad date")
	}
	return o
}

func date(o int) string {
	y, m, d, _ := speq.OrdinalToDate(o)
	return fmt.Sprintf("%04d-%02d-%02d", y, m, d)
}

func kind(k speq.ObjectKind) string {
	switch k {
	case speq.KindDevice:
		return "设备"
	case speq.KindSafetyValve:
		return "安全阀"
	case speq.KindPressureGauge:
		return "压力表"
	}
	return "?"
}

func check(prefix string, err error) {
	if err != nil {
		fmt.Printf("%s -> 拒绝：%v\n", prefix, err)
		return
	}
	fmt.Printf("%s -> 接受\n", prefix)
}

func main() {
	s := speq.New()
	cats := []speq.CategoryConfig{
		{Code: "BOILER", Kind: speq.KindDevice, PeriodMonths: 12, EarlyWindowDays: 30, MinUnsealDays: 10, WarningLeadDays: 15},
		{Code: "SV", Kind: speq.KindSafetyValve, PeriodMonths: 12, EarlyWindowDays: 30, MinUnsealDays: 5, WarningLeadDays: 10},
		{Code: "PG", Kind: speq.KindPressureGauge, PeriodMonths: 6, EarlyWindowDays: 15, MinUnsealDays: 5, WarningLeadDays: 7},
	}
	for _, c := range cats {
		if err := s.AddCategory(c); err != nil {
			panic(err)
		}
	}

	d0 := mustOrd(2020, 1, 1)
	exp, err := s.RegisterDevice(d0, "B-001", "BOILER", d0)
	fmt.Printf("登记设备 B-001（首次检验 %s） -> 到期日 %s（首检日+12 个月）err=%v\n",
		date(d0), date(exp), err)

	svExp, _ := s.RegisterAttachment(d0, "SV-01", "SV", speq.KindSafetyValve, d0)
	fmt.Printf("登记安全阀 SV-01 -> 到期日 %s\n", date(svExp))
	pgFirst := mustOrd(2020, 6, 1)
	pgExp, _ := s.RegisterAttachment(pgFirst, "PG-01", "PG", speq.KindPressureGauge, pgFirst)
	fmt.Printf("登记压力表 PG-01 -> 到期日 %s\n", date(pgExp))

	check("挂接 SV-01 -> B-001 @2020-06-01", s.MountAttachment(pgFirst, "SV-01", "B-001"))
	check("挂接 PG-01 -> B-001 @2020-06-01", s.MountAttachment(pgFirst, "PG-01", "B-001"))
	// B-002 用于演示到期日当天/次日，趁操作日期尚早登记。
	_, _ = s.RegisterDevice(pgFirst, "B-002", "BOILER", d0)
	_, _ = s.RegisterAttachment(pgFirst, "SV-02", "SV", speq.KindSafetyValve, d0)
	_ = s.MountAttachment(pgFirst, "SV-02", "B-002")
	// B-003 用于演示提前窗口恰等边界，必须在全局日期越过 2020-12 前检验。
	_, _ = s.RegisterDevice(pgFirst, "B-003", "BOILER", d0)
	_, _ = s.RegisterAttachment(pgFirst, "SV-03", "SV", speq.KindSafetyValve, d0)
	_ = s.MountAttachment(pgFirst, "SV-03", "B-003")
	insp := exp - 30
	ne, _ := s.Inspect(insp, "B-003", speq.ResultPass, 0)
	fmt.Printf("B-003 窗口边界检验 @%s（到期前 30 天）合格 -> 新到期日 %s（以原到期日为基准）\n",
		date(insp), date(ne))

	d2 := mustOrd(2020, 6, 2)
	report, _ := s.CheckUsable(d2, "B-001")
	fmt.Printf("只读判定 B-001 可使用性 @2020-06-02 -> usable=%v（不推进操作日期）\n", report.Usable)

	// 到期日当天与次日。
	check("使用登记 B-002 @2021-01-01（到期当天，有效）", s.RegisterUse(mustOrd(2021, 1, 1), "B-002"))
	check("使用登记 B-002 @2021-01-02（到期次日，超期）", s.RegisterUse(mustOrd(2021, 1, 2), "B-002"))

	// 有条件合格取较早日期。
	ce, _ := s.Inspect(mustOrd(2021, 6, 1), "PG-01", speq.ResultConditional, 10)
	fmt.Printf("PG-01 有条件合格 @2021-06-01 整改 10 天 -> 到期日 %s（取较早者）\n", date(ce))

	// 封存顺延。
	sealD := mustOrd(2021, 6, 1)
	// B-001 与 SV-01 此时都已超期；先复检合格（同日多次检验，操作日期不回退），
	// 再封存安全阀以演示"附件封存导致设备不可使用"。
	bRe, _ := s.Inspect(sealD, "B-001", speq.ResultPass, 0)
	fmt.Printf("B-001 超期复检合格 @2021-06-01 -> 新到期日 %s（已超期按检验日基准）\n", date(bRe))
	svRe, _ := s.Inspect(sealD, "SV-01", speq.ResultPass, 0)
	fmt.Printf("SV-01 超期复检合格 @2021-06-01 -> 新到期日 %s（已超期按检验日基准）\n", date(svRe))
	check("封存 SV-01 @2021-06-01", s.Seal(sealD, "SV-01"))
	report2, _ := s.CheckUsable(mustOrd(2021, 6, 2), "B-001")
	fmt.Printf("只读判定 B-001 @2021-06-02 -> usable=%v 原因=%s（附件封存）\n",
		report2.Usable, report2.Reason)
	ue, e := s.Unseal(sealD+30, "SV-01")
	if e != nil {
		fmt.Printf("启封 SV-01 被拒绝：%v\n", e)
	} else {
		fmt.Printf("启封 SV-01（封存 30 天） -> 到期日 %s（顺延 30 天）\n", date(ue))
	}

	// 不合格停用。
	_, _ = s.Inspect(mustOrd(2021, 7, 1), "B-001", speq.ResultFail, 0)
	check("使用登记 B-001 @2021-07-02（不合格停用中）", s.RegisterUse(mustOrd(2021, 7, 2), "B-001"))
	_, _ = s.Inspect(mustOrd(2021, 8, 1), "B-001", speq.ResultPass, 0)
	fmt.Printf("复检合格 B-001 @2021-08-01 -> 停用清除\n")

	// 预警：在 2021-08-01 复检一个安全阀使其到期日落在 8 月窗口内（有条件合格）。
	warnD := mustOrd(2021, 8, 1)
	_, _ = s.RegisterAttachment(warnD, "SV-09", "SV", speq.KindSafetyValve, warnD)
	// SV-09 到期 warnD+365；用整改限期 8 天把到期日压到 warnD+8（窗口 10 天内）。
	we, _ := s.Inspect(warnD, "SV-09", speq.ResultConditional, 8)
	_ = s.MountAttachment(warnD, "SV-03", "B-003")
	_ = s.MountAttachment(warnD, "SV-09", "B-003")
	fmt.Printf("SV-09 有条件合格 -> 到期日 %s，挂接到 B-003\n", date(we))
	ws, _ := s.QueryWarnings(warnD)
	fmt.Printf("预警查询 @2021-08-01 -> %d 条：\n", len(ws))
	for _, w := range ws {
		fmt.Printf("  - %s %s 到期=%s 触发附件=%v\n", w.ID, kind(w.Kind), date(w.Expiry), w.Triggers)
	}

	// 报废自动脱离。
	check("报废 B-003 @2021-09-01", s.Scrap(mustOrd(2021, 9, 1), "B-003"))
	if snap, ok := s.Get("SV-09"); ok {
		fmt.Printf("报废后 SV-09 所在设备 = %q（设备报废，附件自动脱离）\n", snap.Host)
	}
	check("报废 B-001 @2021-09-02", s.Scrap(mustOrd(2021, 9, 2), "B-001"))
	if snap, ok := s.Get("SV-01"); ok {
		fmt.Printf("报废后 SV-01 所在设备 = %q（自动脱离）\n", snap.Host)
	}
}
