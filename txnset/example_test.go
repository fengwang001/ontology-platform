package txnset_test

import (
	"fmt"

	"ontology/txnset"
)

// 演示复制断点续传中的核心用法：源端已执行事务集合减去本地已执行集合，
// 得到仍需补偿的事务，并输出逐字节确定的规范文本。
func ExampleSet_Difference() {
	source, err := txnset.Parse("mysql-a:100-200,201-300,500;pg-b:1-9")
	if err != nil {
		panic(err)
	}
	local, err := txnset.Parse("pg-b:1-9;mysql-a:100-250")
	if err != nil {
		panic(err)
	}

	pending := source.Difference(local)
	fmt.Println(pending.Canonical())
	// Output: mysql-a:251-300,500
}

// 解析失败时返回可区分的错误：第一个非法字符是位置 4 的空格，属语法错误。
func ExampleParse_invalid() {
	_, err := txnset.Parse("a:1; b.bad:2")
	if pe, ok := err.(*txnset.ParseError); ok {
		fmt.Println(pe.Kind, pe.Pos)
	}
	// Output: syntax error 4
}
