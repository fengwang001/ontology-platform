package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"ontology/filename"
	"ontology/paramjoin"
	"ontology/paramlex"
)

var failed bool

func check(ok bool, msg string) {
	mark := "ok"
	if !ok {
		mark = "FAIL"
		failed = true
	}
	fmt.Printf("%s: %s\n", mark, msg)
}

func join(h string) ([]paramjoin.Param, error) {
	_, items, err := paramlex.Parse(h)
	if err != nil {
		return nil, err
	}
	return paramjoin.Join(items)
}

func decide(h string) string {
	_, items, err := paramlex.Parse(h)
	if err != nil {
		return "ERR: " + err.Error()
	}
	ps, err := paramjoin.Join(items)
	if err != nil {
		return "ERR: " + err.Error()
	}
	return filename.Decide("attachment", ps)
}

func main() {
	// 1. 引号内的分号与空格不得切断参数值
	typ, items, err := paramlex.Parse(`attachment; filename="a b;c.txt"`)
	check(err == nil && typ == "attachment" && len(items) == 1 &&
		items[0].Name == "filename" && items[0].Raw == "a b;c.txt",
		"1 quoted value keeps semicolon and space")

	// 2. 扩展标记值按 utf-8 百分号解码出中文
	ps, err := join(`attachment; filename*=utf-8''%E4%B8%AD%E6%96%87.txt`)
	check(err == nil && len(ps) == 1 && ps[0].Value == "中文.txt",
		"2 utf-8 ext value decodes to Chinese")

	// 3. 三段续行按序号升序直接相接，仅第 0 段带字符集标注
	ps, err = join(`attachment; name*0*="utf-8''a"; name*1=b; name*2=c`)
	check(err == nil && len(ps) == 1 && ps[0].Value == "abc",
		"3 three-segment continuation joins to abc")

	// 4. 缺 1 号段报续行不完整，且能取出缺的是 1
	_, err = join(`attachment; name*0=x; name*2=z`)
	var inc *paramjoin.IncompleteError
	check(errors.Is(err, paramjoin.ErrIncomplete) && errors.As(err, &inc) && inc.Missing == 1,
		"4 missing segment reported as 1")

	// 5. 不支持的字符集能取出字符集名 gbk
	_, err = join(`attachment; filename*=gbk''%41`)
	var ce *paramjoin.CharsetError
	check(errors.Is(err, paramjoin.ErrCharset) && errors.As(err, &ce) && ce.Charset == "gbk",
		"5 unsupported charset reported as gbk")

	// 6. filename 与 filename* 同时存在且不同：扩展标记胜出
	ps, err = join(`attachment; filename="plain.txt"; filename*=utf-8''fancy.txt`)
	name := ""
	used := "filename"
	if err == nil {
		name = filename.Decide("attachment", ps)
		for _, p := range ps {
			if p.Name == "filename" && p.Ext && p.Value == name {
				used = "filename*"
			}
		}
	}
	check(err == nil && name == "fancy.txt", "6 ext wins, decided by "+used)

	// 7. 路径穿越被安全化：不含分隔符也不含 ..
	name = decide(`attachment; filename="../../etc/passwd"`)
	check(!strings.ContainsAny(name, `/\`) && !strings.Contains(name, ".."),
		"7 traversal sanitized to "+name)

	// 8. ".." 与其百分号编码形式都被剔除到兜底名
	a, b := decide(`attachment; filename="%2e%2e"`), decide(`attachment; filename=".."`)
	check(a == filename.Fallback && b == filename.Fallback,
		"8 dot-only names fall back to "+filename.Fallback)

	if failed {
		os.Exit(1)
	}
	fmt.Println("ALL OK")
}
