package main

import (
	"fmt"
	"os"
)

var fails int

func report(name string, ok bool) {
	if ok {
		fmt.Printf("OK   %s\n", name)
	} else {
		fails++
		fmt.Printf("FAIL %s\n", name)
	}
}

// 判定函数随实现进度逐个补全。
func checkFiveErrors() bool   { return false }
func checkSurrogates() bool   { return false }
func checkInvalidUTF8() bool  { return false }
func checkMinimalEscape() bool { return false }
func checkRoundTrip() bool    { return false }
func checkStreamSplits() bool { return false }
func checkCounter() bool      { return false }

func main() {
	report("five strict errors with offsets", checkFiveErrors())
	report("surrogate-pair cases", checkSurrogates())
	report("invalid UTF-8 both directions", checkInvalidUTF8())
	report("minimal escaping", checkMinimalEscape())
	report("round trip", checkRoundTrip())
	report("all split points agree", checkStreamSplits())
	report("byte counter <= 2N", checkCounter())
	if fails == 0 {
		fmt.Println("ALL OK")
	} else {
		fmt.Printf("%d FAILURES\n", fails)
		os.Exit(1)
	}
}
