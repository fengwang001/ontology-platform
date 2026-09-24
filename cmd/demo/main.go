package main

import "fmt"

type result struct {
	name string
	ok   bool
}

func main() {
	results := make([]result, 0, 11)

	// 随实现逐条追加：
	// 格式样例 / 往返 / 拒绝清单与偏移 / 大次数 / 组合字符 / 切分点 / 计数器
	results = append(results, result{"骨架", true})

	fail := 0
	for _, r := range results {
		mark := "OK"
		if !r.ok {
			mark = "FAIL"
			fail++
		}
		fmt.Printf("%s %s\n", mark, r.name)
	}
	fmt.Printf("总计 %d/%d 通过\n", len(results)-fail, len(results))
}
