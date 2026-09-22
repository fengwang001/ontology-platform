package main

import (
	"errors"
	"fmt"

	"ontology/safeexpr"
)

type report struct {
	pass  int
	total int
}

func (r *report) check(name string, ok bool, detail string) {
	r.total++
	tag := "FAIL"
	if ok {
		tag = "OK"
		r.pass++
	}
	fmt.Printf("%s %s %s\n", tag, name, detail)
}

func main() {
	r := &report{}

	v, err := safeexpr.Eval("1-2-3")
	node, _ := safeexpr.Parse("1-2-3")
	r.check("1-2-3 左结合",
		err == nil && v == int64(-4) && node.String() == "(- (- 1 2) 3)",
		fmt.Sprintf("结果=%v 结构=%s", v, node.String()))

	v, err = safeexpr.Eval("--3")
	node, _ = safeexpr.Parse("--3")
	r.check("--3 双重一元负号",
		err == nil && v == int64(3) && node.String() == "(- (- 3))",
		fmt.Sprintf("结果=%v 结构=%s", v, node.String()))

	v, err = safeexpr.Eval("-2*-3")
	r.check("-2*-3 一元负号紧于乘法",
		err == nil && v == int64(6),
		fmt.Sprintf("结果=%v", v))

	_, err = safeexpr.Eval("9223372036854775807+1")
	r.check("9223372036854775807+1 溢出",
		errors.Is(err, safeexpr.ErrOverflow),
		fmt.Sprintf("错误=%v", err))

	_, err = safeexpr.Eval("1/0")
	r.check("1/0 除零",
		errors.Is(err, safeexpr.ErrDivisionByZero),
		fmt.Sprintf("错误=%v", err))

	_, err = safeexpr.Eval("0.1")
	r.check("0.1 不可精确表示",
		errors.Is(err, safeexpr.ErrInexact),
		fmt.Sprintf("错误=%v", err))

	_, err = safeexpr.Eval("2(3)")
	pe, _ := err.(*safeexpr.Error)
	r.check("2(3) 非法字符定位",
		errors.Is(err, safeexpr.ErrIllegalChar) && pe != nil && pe.Pos == 1,
		fmt.Sprintf("错误=%v", err))

	big := "  ( 1 + 2 ) * ( 3 + 4 ) - 10 / 2 + -3 * 2  "
	v, err = safeexpr.Eval(big)
	r.check("含空白大表达式",
		err == nil && v == int64(10),
		fmt.Sprintf("%s = %v", big, v))

	fmt.Printf("总计: %d/%d 项通过\n", r.pass, r.total)
}
