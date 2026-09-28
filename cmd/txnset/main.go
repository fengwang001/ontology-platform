// 命令 txnset 是事务标识集合组件的本地验证工具。
//
// 用法：
//
//	txnset canon "<文本>"        解析并输出规范文本
//	txnset diff "<源端>" "<本地>" 输出 源端 - 本地 的规范文本
//
// 输入非法时错误打印到 stderr 并以非零码退出。
package main

import (
	"errors"
	"fmt"
	"os"

	"ontology/txnset"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "canon":
		if len(os.Args) != 3 {
			usage()
			os.Exit(2)
		}
		s, err := txnset.Parse(os.Args[2])
		if err != nil {
			fail(err)
		}
		fmt.Println(s.Canonical())
	case "diff":
		if len(os.Args) != 4 {
			usage()
			os.Exit(2)
		}
		source, err := txnset.Parse(os.Args[2])
		if err != nil {
			fail(err)
		}
		local, err := txnset.Parse(os.Args[3])
		if err != nil {
			fail(err)
		}
		fmt.Println(source.Difference(local).Canonical())
	case "-h", "--help", "help":
		usage()
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `txnset — 事务标识集合解析/合并/差集/规范化工具

用法:
  txnset canon "<文本>"          解析并输出规范文本
  txnset diff  "<源端>" "<本地>" 输出 源端集合 - 本地集合 的规范文本

语法:
  文本 = 条目(';'条目)*
  条目 = 来源':'区间(','区间)*
  区间 = 号 | 号'-'号   (闭区间)
  来源 = 字母(字母|数字|'_'|'-')*
  号   = '0' | 非零数字数字*   (uint64，禁前导零)
  任意位置不允许空白`)
}

func fail(err error) {
	var pe *txnset.ParseError
	if errors.As(err, &pe) {
		fmt.Fprintf(os.Stderr, "%s (位置 %d)\n", pe.Kind, pe.Pos)
	} else {
		fmt.Fprintln(os.Stderr, err)
	}
	os.Exit(1)
}
