package main

import (
	"context"
	"fmt"
	"time"

	"ontology/source"
)

func main() {
	fails := 0
	check := func(name string, ok bool) {
		if ok {
			fmt.Println("OK  " + name)
		} else {
			fmt.Println("FAIL " + name)
			fails++
		}
	}

	// source 包判定：速率/提前结束/中途报错/Seek 重放
	src := &source.Memory{N: 6, Rate: time.Microsecond, StopAt: 4}
	var got []string
	for {
		it, ok, err := src.Next(context.Background())
		if err != nil || !ok {
			break
		}
		got = append(got, it.Line)
	}
	src.Seek(2)
	it, ok, _ := src.Next(context.Background())
	failing := &source.Memory{N: 10, FailAt: 3}
	_, _, failErr := failing.Next(context.Background())
	_, _, failErr = failing.Next(context.Background())
	_, _, failErr = failing.Next(context.Background())
	check("source: 提前结束/速率/Seek/注入错误", len(got) == 4 && ok && it.Off == 3 && failErr != nil)

	fmt.Printf("TOTAL %d/%d OK\n", 1-fails, 1)
	if fails > 0 {
		panic("demo checks failed")
	}
}
