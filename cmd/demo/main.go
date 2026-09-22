package main

import (
	"errors"
	"fmt"

	"ontology/eval"
)

func main() {
	pass, total := 0, 0
	check := func(name string, ok bool, detail string) {
		total++
		if ok {
			pass++
			fmt.Printf("OK   %s: %s\n", name, detail)
		} else {
			fmt.Printf("FAIL %s: %s\n", name, detail)
		}
	}

	v, tree, err := eval.Eval("1-2-3")
	check("1-2-3 左结合", err == nil && tree.String() == "((1-2)-3)" && v == int64(-4),
		fmt.Sprintf("结构=%s 结果=%v", tree.String(), v))

	v, tree, err = eval.Eval("--3")
	check("--3 双重负号", err == nil && tree.String() == "(-(-3))" && v == int64(3),
		fmt.Sprintf("结构=%s 结果=%v", tree.String(), v))

	v, tree, err = eval.Eval("-2*-3")
	check("-2*-3 一元负号更紧", err == nil && tree.String() == "((-2)*(-3))" && v == int64(6),
		fmt.Sprintf("结构=%s 结果=%v", tree.String(), v))

	_, _, err = eval.Eval("9223372036854775807+1")
	check("int64 上界+1 溢出", errors.Is(err, eval.ErrOverflow), fmt.Sprintf("err=%v", err))

	_, _, err = eval.Eval("1/0")
	check("1/0 除零", errors.Is(err, eval.ErrDivideByZero), fmt.Sprintf("err=%v", err))

	_, _, err = eval.Eval("0.1")
	check("0.1 不可精确表示", errors.Is(err, eval.ErrNotRepresentable),
		fmt.Sprintf("err=%v", err))

	_, _, err = eval.Eval("2(3)")
	var pe *eval.PosError
	errors.As(err, &pe)
	pos := -1
	if pe != nil {
		pos = pe.Pos
	}
	check("2(3) 非法字符定位", errors.Is(err, eval.ErrIllegalChar) && pos == 1,
		fmt.Sprintf("err=%v", err))

	big := "( 1 + 2 ) * 3 - 4 / 2 + -2 * -3"
	v, _, err = eval.Eval(big)
	check("含空白大表达式求值", err == nil && v == int64(13),
		fmt.Sprintf("%s = %v (类型 %T)", big, v, v))

	fmt.Printf("总计: %d/%d 通过\n", pass, total)
}
