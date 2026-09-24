package main

import "fmt"

var pass, total int

func check(name string, ok bool) {
	total++
	if ok {
		pass++
	}
	fmt.Printf("%-44s %s\n", name, map[bool]string{true: "OK", false: "FAIL"}[ok])
}

func pend(name string) {
	total++
	fmt.Printf("%-44s PEND\n", name)
}

func main() {
	pend("空输入合法, 输出长度 0")
	pend("规范样例: QQ==→A, QUI=→AB")
	pend("非规范尾部拒绝: QR==")
	pend("非规范尾部拒绝: QUJ=")
	pend("残缺填充拒绝: QQ / QQ= / QQ===")
	pend("填充位置错误: QQ==QQ==")
	pend("MIME 组间 \\r\\n 与 \\n 合法")
	pend("组内换行/单\\r/空格/TAB 拒绝")
	pend("五类错误可区分且偏移正确")
	pend("长度不是 4 的倍数, 结束时报错")
	pend("任意切分点结果逐字节相同")
	pend("往返: MIME 开/关, 余数 0/1/2")
	pend("输出上限: 组边界停下并进入终态")
	pend("计数器 == 输入字节数(1MB 逐字节)")
	fmt.Printf("总计: %d/%d\n", pass, total)
}
