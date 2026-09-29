// materializer-demo 演示原子批次物化器的批内顺序预演、全或无与失败无痕。
package main

import (
	"errors"
	"fmt"
	"log"
	"os"

	"ontology/materializer"
)

func ptr(s string) *string { return &s }

func main() {
	logger := log.New(os.Stdout, "[demo] ", log.LstdFlags|log.Lmicroseconds)
	m := materializer.New(logger)

	apply := func(name string, batch []materializer.Event) {
		fmt.Printf("\n==== %s ====\n", name)
		if err := m.Apply(batch); err != nil {
			var fe *materializer.FailureError
			if errors.As(err, &fe) {
				fmt.Printf("result: REJECTED at index %d (%v)\n", fe.Index, err)
				return
			}
			fmt.Printf("result: error %v\n", err)
			return
		}
		fmt.Println("result: COMMITTED atomically")
	}

	// 1. 批内顺序：先写 a，再以“看到前序新值”为期望覆盖 a，最后删 b。
	apply("chained expectations + delete",
		[]materializer.Event{
			{Key: "a", Op: materializer.Put, Value: "1", Expect: nil},
			{Key: "b", Op: materializer.Put, Value: "2", Expect: nil},
			{Key: "a", Op: materializer.Put, Value: "3", Expect: ptr("1")},
			{Key: "b", Op: materializer.Delete, Expect: ptr("2")},
		})

	// 2. 全或无：第 2 条期望失败，第 1 条的暂存写入也必须无痕。
	apply("all-or-nothing: expectation fails mid-batch",
		[]materializer.Event{
			{Key: "tmp", Op: materializer.Put, Value: "x", Expect: nil},
			{Key: "a", Op: materializer.Put, Value: "9", Expect: ptr("no-such-value")},
		})

	// 3. 三类非法输入：空批。
	apply("illegal: empty batch", nil)

	// 4. 三类非法输入：空键（第一条即失败）。
	apply("illegal: empty key",
		[]materializer.Event{
			{Key: "", Op: materializer.Put, Value: "x", Expect: nil},
		})

	fmt.Printf("\nfinal view: %v\n", m.Snapshot())
}
