package main

import (
	"fmt"
	"os"
	"slices"

	"ontology/linescan"
	"ontology/field"
)

var failed, total int

func check(name string, ok bool) {
	status := "OK  "
	if !ok {
		status = "FAIL"
		failed++
	}
	total++
	fmt.Printf("%s %s\n", status, name)
}

func splitEvery(data string, n int) []string {
	sc := linescan.New(0)
	var out []string
	for i := 0; i < len(data); i += n {
		lines, err := sc.Feed([]byte(data[i:min(i+n, len(data))]))
		if err != nil {
			return nil
		}
		out = append(out, lines...)
	}
	if line, ok := sc.Flush(); ok {
		out = append(out, line)
	}
	return out
}

func main() {
	want := []string{"l1", "l2", "l3", "l4"}
	check("三种行尾等价", slices.Equal(splitEvery("l1\nl2\r\nl3\rl4", 1), want) &&
		slices.Equal(splitEvery("l1\nl2\r\nl3\rl4", 4), want))
	check("CRLF 不算两个空行", slices.Equal(splitEvery("a\r\nb", 5), []string{"a", "b"}))
	check("块边界落在 CRLF 中间", slices.Equal(splitEvery("a\r\nb", 2), []string{"a", "b"}))
	src := "data: 1\r\ndata:2\rdata: 3\n\n"
	base := splitEvery(src, len(src))
	same := true
	for n := 1; n < len(src); n++ {
		same = same && slices.Equal(splitEvery(src, n), base)
	}
	check("切分点遍历一致", same)
	f1, ok1 := field.Parse("data: x")
	f2, _ := field.Parse("data:x")
	f3, _ := field.Parse("data:  x")
	f4, _ := field.Parse("data:")
	f5, _ := field.Parse("data")
	_, isComment := field.Parse(": ping")
	check("冒号后空格规则", ok1 && f1.Value == "x" && f2.Value == "x" &&
		f3.Value == " x" && f4.Value == "" && f5.Name == "data" && f5.Value == "" && !isComment)
	fmt.Printf("total: %d/%d passed\n", total-failed, total)
	if failed > 0 {
		os.Exit(1)
	}
}
