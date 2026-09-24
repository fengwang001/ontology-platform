// Command demo exercises the Range response assembler end to end.
//
// An Assembler does not support concurrent Write/WriteTo calls on the same
// instance; use one assembler per response. Different assemblers are
// independent and may be used concurrently.
package main

import (
	"errors"
	"fmt"
	"io"

	"ontology/coalesce"
	"ontology/rangespec"
	"ontology/source"
)

var fails int

func check(name string, ok bool) {
	if ok {
		fmt.Printf("OK   %s\n", name)
		return
	}
	fails++
	fmt.Printf("FAIL %s\n", name)
}

func main() {
	specs, err := rangespec.Parse("bytes=0-499,500-,-200")
	threeForms := err == nil && len(specs) == 3 &&
		specs[0] == (rangespec.Spec{Kind: rangespec.Closed, A: 0, B: 499}) &&
		specs[1] == (rangespec.Spec{Kind: rangespec.OpenEnd, A: 500}) &&
		specs[2] == (rangespec.Spec{Kind: rangespec.Suffix, B: 200})
	check("三种写法解析 a-b / a- / -n", threeForms)

	_, err = rangespec.Parse("bytes=1--2")
	var se *rangespec.SyntaxError
	check("语法错误带偏移且可判定", errors.As(err, &se) && se.Offset == 8)

	specs2, _ := rangespec.Parse("bytes=-0")
	_, err = coalesce.Normalize(specs2, 100)
	var ue *coalesce.UnsatisfiableError
	check("bytes=-0 不可满足且带总长", errors.As(err, &ue) && ue.Size == 100)
	check("两类错误彼此可判定", errors.As(err, &ue) && !errors.As(err, &se))

	specs3, _ := rangespec.Parse("bytes=0-4,5-9,-5")
	ivs, _ := coalesce.Normalize(specs3, 20)
	adj := len(ivs) == 2 && ivs[0] == (coalesce.Interval{Start: 0, End: 9}) && ivs[1] == (coalesce.Interval{Start: 15, End: 19})
	check("越界裁剪+相邻合并", adj)

	data := make([]byte, 50)
	for i := range data {
		data[i] = byte(i)
	}
	src := source.NewMemory(data)
	src.MaxRead = 7
	buf := make([]byte, 50)
	check("短读循环补齐", source.ReadFull(src, buf) == nil && string(buf) == string(data))

	src2 := source.NewMemory(make([]byte, 1000))
	src2.MaxRead = 2
	src2.ReadErr = io.EOF
	src2.ErrAfter = 1
	err = source.ReadFull(src2, make([]byte, 10))
	check("读到 EOF 但不足可判定", errors.Is(err, io.ErrUnexpectedEOF))

	if fails == 0 {
		fmt.Println("ALL OK")
	} else {
		fmt.Printf("%d FAIL\n", fails)
	}
}
