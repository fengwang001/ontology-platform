package main

import "fmt"

func report(name string, ok bool) bool {
	if ok {
		fmt.Println("OK  ", name)
	} else {
		fmt.Println("FAIL", name)
	}
	return ok
}

func main() {
	pass := 0
	total := 13
	checks := []struct {
		name string
		ok   bool
	}{
		{"five error kinds + offsets", false},
		{"surrogate pairs", false},
		{"invalid utf-8 decode/encode", false},
		{"minimal escaping", false},
		{"roundtrip", false},
		{"all split points", false},
		{"byte check counter", false},
	}
	for _, c := range checks {
		if report(c.name, c.ok) {
			pass++
		}
	}
	_ = pass
	_ = total
	fmt.Printf("TOTAL %d/%d\n", 0, len(checks))
}
