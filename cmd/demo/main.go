package main

import "fmt"

type result struct {
	name string
	ok   bool
}

func main() {
	results := []result{
		{"trailing whitespace", false},
		{"atomic escapes", false},
		{"line limit", false},
		{"error kinds and offsets", false},
		{"all split points", false},
		{"round trip", false},
		{"minimal escapes", false},
		{"byte check counter", false},
	}

	ok := true
	for _, item := range results {
		status := "OK"
		if !item.ok {
			status = "FAIL"
			ok = false
		}
		fmt.Printf("%s: %s\n", item.name, status)
	}

	if ok {
		fmt.Println("total: 8/8 OK")
	} else {
		fmt.Println("total: 0/8 OK")
	}
}
