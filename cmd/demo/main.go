// Command demo verifies the escaped RLE codec end to end.
package main

import (
	"fmt"
	"os"
)

func main() {
	failed := 0
	check := func(name string, ok bool) {
		if ok {
			fmt.Println("OK   " + name)
			return
		}
		fmt.Println("FAIL " + name)
		failed++
	}

	check("format samples", false)
	check("roundtrip", false)
	check("reject table", false)
	check("big count", false)
	check("combining marks", false)
	check("all split points", false)
	check("inspection counter", false)

	if failed == 0 {
		fmt.Println("ALL OK 7/7")
		return
	}
	fmt.Printf("%d/7 failed\n", failed)
	os.Exit(1)
}
