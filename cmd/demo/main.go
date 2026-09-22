// demo 演示安全算术表达式求值器：解析唯一性、精确数值语义与错误分类。
// 不读命令行参数、不联网，退出码恒为 0。
package main

import (
	"errors"
	"fmt"

	"ontology"
)

var passed, total int

func check(name string, ok bool, detail string) {
	total++
	mark := "OK  "
	if !ok {
		mark = "FAIL"
	} else {
		passed++
	}
	fmt.Printf("%s %-14s %s\n", mark, name, detail)
}

func main() {
	// 1. 左结合：1-2-3 必须是 (1-2)-3 = -4。
	n, err := ontology.Parse("1-2-3")
	v, verr := ontology.Eval("1-2-3")
	check("1-2-3", err == nil && verr == nil && n.String() == "((1-2)-3)" && v == int64(-4),
		fmt.Sprintf("结构=%s 结果=%v", n.String(), v))

	// 2. 双重一元负号。
	v, err = ontology.Eval("--3")
	check("--3", err == nil && v == int64(3), fmt.Sprintf("结果=%v", v))

	// 3. 一元负号比乘除更紧。
	v, err = ontology.Eval("-2*-3")
	check("-2*-3", err == nil && v == int64(6), fmt.Sprintf("结果=%v", v))

	// 4. int64 溢出必须报错。
	_, err = ontology.Eval("9223372036854775807+1")
	check("溢出", errors.Is(err, ontology.ErrOverflow), fmt.Sprintf("err=%v", err))

	// 5. 除零必须报错。
	_, err = ontology.Eval("1/0")
	check("除零", errors.Is(err, ontology.ErrDivZero), fmt.Sprintf("err=%v", err))

	// 6. 0.1 无法被 float64 精确表示。
	_, err = ontology.Eval("0.1")
	check("精确性", errors.Is(err, ontology.ErrInexact), fmt.Sprintf("err=%v", err))

	// 7. 2(3) 隐式乘法非法，报非法字符并定位。
	_, err = ontology.Eval("2(3)")
	var e *ontology.Error
	ok := errors.Is(err, ontology.ErrIllegalChar) && errors.As(err, &e) && e.Pos == 1
	check("2(3)定位", ok, fmt.Sprintf("err=%v", err))

	// 8. 含任意空白的大表达式。
	v, err = ontology.Eval(" \t ( 1 + 2 ) * 3 \n - 8 / 4 + 10 * ( 2 - 5 ) ")
	check("大表达式", err == nil && v == int64(-23), fmt.Sprintf("结果=%v", v))

	fmt.Printf("TOTAL %d/%d 通过\n", passed, total)
}
