// demo 逐条演示 bizday 日历的判定结果，全部符合预期时退出码为 0。
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
	fmt.Printf("%s %s: got=%v want=%v\n", status, name, got, want)
}

func main() {
	c, err := bizday.New(
		[]int{20260922, 20261001, 20260921},
		[]int{20260919, 20260921},
	)
	if err != nil {
		fmt.Println("FAIL New:", err)
		os.Exit(1)
	}

	b1, _ := c.IsBusinessDay(20260921)
	check("holiday-beats-workday", b1, false)
	b2, _ := c.IsBusinessDay(20260919)
	check("saturday-workday", b2, true)
	b3, _ := c.IsBusinessDay(20260920)
	check("plain-sunday", b3, false)

	a1, _ := c.AddBusinessDays(20260918, 1)
	check("fri+1", a1, 20260919)
	a2, _ := c.AddBusinessDays(20260919, 1)
	check("sat+1", a2, 20260923)
	a3, _ := c.AddBusinessDays(20260930, 1)
	check("cross-month", a3, 20261002)
	a4, _ := c.AddBusinessDays(20261231, 1)
	check("cross-year", a4, 20270101)
	a5, _ := c.AddBusinessDays(20280228, 1)
	check("leap-day", a5, 20280229)
	a6, _ := c.AddBusinessDays(20260921, -1)
	check("backward", a6, 20260919)
	a7, _ := c.AddBusinessDays(20260916, 0)
	check("zero-on-workday", a7, 20260916)

	_, err0 := c.AddBusinessDays(20260920, 0)
	check("zero-on-sunday-err", errors.Is(err0, bizday.ErrNotBusinessDay), true)

	n1, _ := c.CountBusinessDays(20260921, 20260928)
	check("count-week", n1, 3)
	n2, _ := c.CountBusinessDays(20260921, 20260921)
	check("count-empty", n2, 0)
	_, errR := c.CountBusinessDays(20260922, 20260921)
	check("count-reversed-err", errors.Is(errR, bizday.ErrInvalidRange), true)
	_, errD := c.IsBusinessDay(20260230)
	check("invalid-date-err", errors.Is(errD, bizday.ErrInvalidDate), true)

	if failed {
		os.Exit(1)
	}
}
