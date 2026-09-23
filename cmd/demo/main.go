package main

import (
	"bytes"
	"errors"
	"fmt"

	"ontology/delta"
	"ontology/doc"
)

func report(ok bool, msg string) bool {
	if ok {
		fmt.Println("OK " + msg)
	} else {
		fmt.Println("FAIL " + msg)
	}
	return ok
}

func main() {
	fails := 0
	check := func(ok bool, msg string) {
		if !report(ok, msg) {
			fails++
		}
	}

	a := doc.Set{"k": {"x": doc.String("1")}}
	b := doc.Set{"k": {"x": doc.Number(1)}}
	check(!bytes.Equal(doc.EncodeSet(a), doc.EncodeSet(b)), "字符串\"1\"与数值1编码可区分")

	err := delta.Validate([]*delta.Entry{
		{Key: "k", Kind: delta.KindDeleted},
		{Key: "k", Kind: delta.KindModified},
	})
	check(errors.Is(err, delta.ErrContradiction), "同侧删改矛盾被检出")

	if fails > 0 {
		fmt.Printf("总计: %d FAIL\n", fails)
	} else {
		fmt.Println("总计: 0 FAIL")
	}
}
