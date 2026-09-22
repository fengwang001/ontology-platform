// 工作日历演示：逐条打印 OK/FAIL 判定，全部通过时退出码为 0。
package main

import (
	"errors"
	"fmt"
	"os"

	"ontology/internal/bizday"
)

var failed bool

func check(name string, got, want any) {
	status := "OK"
	if got != want {
		status = "FAIL"
		failed = true
	}
	fmt.Printf("%s %-28s got=%v want=%v\n", status, name, got, want)
}

func checkErr(name string, err, want error) {
	status := "OK"
	if !errors.Is(err, want) {
		status = "FAIL"
		failed = true
	}
	fmt.Printf("%s %-28s err=%v\n", status, name, err)
}

func main() {
	c, err := bizday.New(
		[]int{20261001, 20261026},
		[]int{20260926, 20261026},
	)
	checkErr("New(重叠日期不报错)", err, nil)

	is, _ := c.IsBusinessDay(20261026) // 同时在两者中，节假日优先
	check("重叠日按非工作日", is, false)
	is, _ = c.IsBusinessDay(20260926) // 周六调休上班
	check("周六调休为工作日", is, true)
	is, _ = c.IsBusinessDay(20261001) // 周四但逢节假日
	check("节假日周四非工作日", is, false)

	got, _ := c.AddBusinessDays(20260925, 1) // 周五
	check("Add(周五,1)=调休周六", got, 20260926)
	got, _ = c.AddBusinessDays(20260927, 1) // 周日自身不计
	check("Add(周日,1)=下周一", got, 20260928)
	got, _ = c.AddBusinessDays(20260923, 0) // 周三是工作日
	check("Add(周三,0)=原样返回", got, 20260923)
	_, err = c.AddBusinessDays(20260927, 0) // 周日不是工作日
	checkErr("Add(周日,0)报错", err, bizday.ErrNotBusinessDay)
	got, _ = c.AddBusinessDays(20261231, 1) // 跨年
	check("Add(跨年末日,1)", got, 20270101)
	got, _ = c.AddBusinessDays(20240228, 1) // 跨闰年 2 月 29 日
	check("Add(闰年2/28,1)=2/29", got, 20240229)

	got, _ = c.CountBusinessDays(20260921, 20260921)
	check("Count(d,d)=0", got, 0)
	got, _ = c.CountBusinessDays(20260921, 20260928)
	check("Count(一周,左闭右开)=6", got, 6)
	_, err = c.CountBusinessDays(20260928, 20260921)
	checkErr("Count(from>to)报错", err, bizday.ErrInvalidRange)
	_, err = c.IsBusinessDay(20260230)
	checkErr("非法日期报错", err, bizday.ErrInvalidDate)

	// 11 月天数修复演练：11 月只有 30 天。
	got, _ = c.CountBusinessDays(20261101, 20261201)
	check("11月工作日天数=21", got, 21)
	got, _ = c.AddBusinessDays(20261130, 1)
	check("11月末顺延=12月1日", got, 20261201)
	_, err = c.IsBusinessDay(20261131)
	checkErr("11月31日被拒绝", err, bizday.ErrInvalidDate)

	if failed {
		os.Exit(1)
	}
}
