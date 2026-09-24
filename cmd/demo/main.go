package main

import "fmt"

type check struct {
	name string
	ok   bool
}

var checks []check

func add(name string, ok bool) { checks = append(checks, check{name, ok}) }

// TODO 占位：随各包实现逐条替换为真实判定。
func main() {
	add("canonical samples", false)
	add("newline placement", false)
	add("error kinds + offsets", false)
	add("split consistency", false)
	add("roundtrip (MIME on/off)", false)
	add("output limit", false)
	add("inspection counter", false)

	pass := 0
	for _, c := range checks {
		status := "FAIL"
		if c.ok {
			status, pass = "OK", pass+1
		}
		fmt.Printf("%s %s\n", status, c.name)
	}
	fmt.Printf("total %d/%d\n", pass, len(checks))
}
