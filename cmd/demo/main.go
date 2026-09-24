package main

import (
	"errors"
	"fmt"
	"os"

	"ontology/vec"
)

var pass, total int

func judge(name string, ok bool) {
	total++
	if ok {
		pass++
		fmt.Println("OK " + name)
	} else {
		fmt.Println("FAIL " + name)
	}
}

func main() {
	_, err := vec.Dot(vec.Vec{1}, vec.Vec{1, 2})
	var de *vec.DimError
	judge("维度不符可 errors.Is 判定并带期望/实际",
		errors.As(err, &de) && errors.Is(err, vec.ErrDimMismatch) && de.Want == 1 && de.Got == 2)

	fmt.Printf("TOTAL %d/%d\n", pass, total)
	if pass != total {
		os.Exit(1)
	}
}
