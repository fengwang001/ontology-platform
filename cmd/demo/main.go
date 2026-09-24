package main

import (
	"errors"
	"fmt"

	"ontology/classify"
)

func main() {
	checks := []func() (string, bool){
		func() (string, bool) {
			// 四类错误可判定且互不混淆。
			kinds := []struct {
				err error
				k   classify.Kind
			}{
				{classify.ErrRetryable, classify.KindRetryable},
				{classify.ErrNonRetryable, classify.KindNonRetryable},
				{classify.ErrTimeout, classify.KindTimeout},
				{classify.ErrPanic, classify.KindPanic},
			}
			for _, c := range kinds {
				if classify.Of(c.err) != c.k {
					return "四类错误 errors.Is 可区分", false
				}
			}
			return "四类错误 errors.Is 可区分", !errors.Is(classify.ErrTimeout, classify.ErrRetryable)
		},
	}
	pass := 0
	for _, c := range checks {
		name, ok := c()
		if ok {
			pass++
			fmt.Printf("OK   %s\n", name)
		} else {
			fmt.Printf("FAIL %s\n", name)
		}
	}
	fmt.Printf("总计 %d/%d 通过\n", pass, len(checks))
}
