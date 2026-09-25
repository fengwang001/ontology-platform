// Command demo 对旋转排序数组二分查找做少量判定并打印结果。
package main

import (
	"errors"
	"fmt"
	"os"

	"ontology/arr"
	"ontology/rot"
)

func main() {
	fails := 0
	check := func(name string, ok bool) {
		status := "OK"
		if !ok {
			status, fails = "FAIL", fails+1
		}
		fmt.Printf("%s %s\n", status, name)
	}
	nums := []int{4, 5, 6, 7, 0, 1, 2}
	check("hit-rotated", rot.Search(nums, 0) == 4)
	check("miss-rotated", rot.Search(nums, 3) == -1)
	check("empty", rot.Search(nil, 1) == -1)
	check("single", rot.Search([]int{7}, 7) == 0 && rot.Search([]int{7}, 1) == -1)
	check("no-rotation", rot.Search([]int{1, 2, 3, 4}, 3) == 2)
	check("validate-ok", arr.Validate([]int{4, 5, 0, 1}) == nil)
	check("validate-sentinel", errors.Is(arr.Validate([]int{1, 1}), arr.ErrNotStrict))
	status := "OK"
	if fails > 0 {
		status = "FAIL"
	}
	fmt.Printf("%s total=%d fail=%d\n", status, 7, fails)
	if fails > 0 {
		os.Exit(1)
	}
}
