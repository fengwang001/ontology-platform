// Command demo 逐条演示并判定缓存准入/淘汰器的核心性质。
package main

import (
	"fmt"

	"ontology/sketch"
)

var failed int

func check(name string, ok bool, detail string) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failed++
	}
	fmt.Printf("%s %s %s\n", status, name, detail)
}

func main() {
	// 判定 6：频率结构字节数不随条目数变化
	sk := sketch.New(4096)
	before := sk.Bytes()
	for i := 0; i < 100000; i++ {
		sk.Increment(fmt.Sprintf("key-%d", i))
	}
	check("sketch-bytes-fixed", sk.Bytes() == before,
		fmt.Sprintf("10 万键前后均为 %d 字节", sk.Bytes()))

	if failed > 0 {
		fmt.Printf("TOTAL FAIL %d\n", failed)
	} else {
		fmt.Println("TOTAL OK")
	}
}
