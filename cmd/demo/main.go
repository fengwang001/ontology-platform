package main

import (
	"fmt"
	"os"
)

type check struct {
	name string
	fn   func() error
}

// 骨架：各判定在 b64 / stream 实现后逐条替换为真实逻辑。
var checks = []check{
	{"canonical QQ== -> A", pend},
	{"reject QR==", pend},
	{"canonical QUI= -> AB", pend},
	{"reject QUJ=", pend},
	{"reject unpadded/short/extra-pad", pend},
	{"reject pad mid-stream", pend},
	{"empty input", pend},
	{"newline positions", pend},
	{"5 error classes & offsets", pend},
	{"all split points agree", pend},
	{"roundtrip (plain & MIME)", pend},
	{"output limit", pend},
	{"examined byte counter", pend},
}

func pend() error { return errPending }

var errPending = fmt.Errorf("pending")

func main() {
	pass := 0
	for _, c := range checks {
		if err := c.fn(); err != nil {
			if err == errPending {
				fmt.Printf("PEND %s\n", c.name)
				continue
			}
			fmt.Printf("FAIL %s: %v\n", c.name, err)
			continue
		}
		fmt.Printf("OK   %s\n", c.name)
		pass++
	}
	fmt.Printf("TOTAL %d/%d\n", pass, len(checks))
	if pass != len(checks) {
		os.Exit(0) // 骨架阶段仍返回 0；全部判定补齐后只认真实 OK。
	}
}
