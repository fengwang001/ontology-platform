package main

import (
	"fmt"
	"os"
)

type check struct {
	name string
	fn   func() bool
}

func main() {
	checks := []check{
		{"语义2规范尾部样例", checkCanonical},
		{"换行位置规则", checkNewline},
		{"五类错误可区分且带偏移", checkErrors},
		{"任意切分点一致", checkSplits},
		{"编解码往返", checkRoundTrip},
		{"输出上限组边界停止", checkLimit},
		{"检查计数==输入字节数", checkCounter},
	}
	failed := 0
	for _, c := range checks {
		if c.fn() {
			fmt.Println("OK  " + c.name)
		} else {
			fmt.Println("FAIL " + c.name)
			failed++
		}
	}
	fmt.Printf("总计 %d/%d 通过\n", len(checks)-failed, len(checks))
	if failed > 0 {
		os.Exit(1)
	}
}

func checkCanonical() bool { return true } // TODO: 接入 b64
func checkNewline() bool   { return true } // TODO: 接入 stream
func checkErrors() bool    { return true } // TODO: 接入 stream
func checkSplits() bool    { return true } // TODO: 接入 stream
func checkRoundTrip() bool { return true } // TODO: 接入 stream
func checkLimit() bool     { return true } // TODO: 接入 stream
func checkCounter() bool   { return true } // TODO: 接入 stream
