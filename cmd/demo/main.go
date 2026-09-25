package main

import (
	"errors"
	"fmt"

	"ontology/classify"
)

func report(name string, ok bool, detail string) bool {
	status := "OK"
	if !ok {
		status = "FAIL"
	}
	fmt.Printf("%s %s %s\n", status, name, detail)
	return ok
}

func main() {
	pass := true
	cases := []struct {
		err error
		cls classify.Class
	}{
		{nil, classify.OK},
		{classify.MarkPermanent(errors.New("bad arg")), classify.Permanent},
		{classify.ErrTimeout, classify.TimeoutC},
		{errors.New("boom"), classify.Retryable},
	}
	okCls := true
	for _, c := range cases {
		if classify.Classify(c.err) != c.cls {
			okCls = false
		}
	}
	pass = report("错误分类四类齐全(ok/permanent/timeout/retryable)", okCls, "") && pass
	if !pass {
		fmt.Println("FAIL 总计: 存在失败项")
		return
	}
	fmt.Println("OK 总计: 全部通过")
}
