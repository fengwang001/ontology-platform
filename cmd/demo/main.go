package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"ontology/filename"
	"ontology/paramjoin"
)

func main() {
	ok := true
	check := func(no int, pass bool, got string) {
		fmt.Printf("%d: %v got=%q\n", no, pass, got)
		ok = ok && pass
	}
	find := func(params []paramjoin.Param, name string, ext bool) string {
		for _, p := range params {
			if p.Name == name && p.Extended == ext {
				return p.Value
			}
		}
		return ""
	}

	_, params, err := paramjoin.Decode(`attachment; filename="a b;c.txt"`)
	check(1, err == nil && find(params, "filename", false) == "a b;c.txt", find(params, "filename", false))

	_, params, err = paramjoin.Decode(`attachment; filename*=utf-8''%E4%B8%AD%E6%96%87.txt`)
	check(2, err == nil && find(params, "filename", true) == "中文.txt", errText(err, find(params, "filename", true)))

	continued := `attachment; name*0*=utf-8''a; name*1=b; name*2=c`
	_, params, err = paramjoin.Decode(continued)
	check(3, err == nil && find(params, "name", true) == "abc", errText(err, find(params, "name", true)))

	_, _, err = paramjoin.Decode(`attachment; name*0=a; name*2=c`)
	var missing paramjoin.IncompleteError
	check(4, errors.As(err, &missing) && missing.Missing == 1, errText(err, ""))

	_, _, err = paramjoin.Decode(`attachment; filename*=gbk''%41`)
	var unsupported paramjoin.CharsetError
	check(5, errors.As(err, &unsupported) && unsupported.Charset == "gbk", errText(err, ""))

	choice, err := filename.Decode(`attachment; filename="plain.txt"; filename*=utf-8''star.txt`)
	source := "filename*"
	check(6, err == nil && choice.Name == "star.txt" && choice.Source == filename.Extended, source+":"+choice.Name)

	choice, err = filename.Decode(`attachment; filename="../../etc/passwd"`)
	safe := choice.Name
	check(7, err == nil && safe != "" &&
		!strings.ContainsAny(safe, `/\`) && !strings.Contains(safe, ".."), safe)

	one, err1 := filename.Decode(`attachment; filename="%2e%2e"`)
	two, err2 := filename.Decode(`attachment; filename=".."`)
	check(8, err1 == nil && err2 == nil && one.Name == filename.Fallback &&
		two.Name == filename.Fallback, one.Name+","+two.Name)

	if !ok {
		os.Exit(1)
	}
	fmt.Println("ALL OK")
}

func errText(err error, value string) string {
	if err != nil {
		return err.Error()
	}
	return value
}
