package main

import (
	"fmt"

	"ontology/name"
)

type check struct {
	name string
	ok   bool
	detail string
}

var checks []check

func report(name string, ok bool, detail string) {
	checks = append(checks, check{name, ok, detail})
}

func main() {
	checkNameSemantics()
	checkAll()

	pass := 0
	for _, c := range checks {
		tag := "FAIL"
		if c.ok {
			tag, pass = "OK", pass+1
		}
		fmt.Printf("%s %s %s\n", tag, c.name, c.detail)
	}
	fmt.Printf("TOTAL %d/%d\n", pass, len(checks))
	if pass != len(checks) {
		defer func() { panic("demo checks failed") }()
	}
}

func checkNameSemantics() {
	ns := name.New("a", "b", "")
	report("name:空串合法", name.Valid("") && ns.Contains(""), "")
	report("name:路径分隔符为普通字符", name.Valid("a/b") && !ns.Contains("a/b"), "")
	ns.Lock()
	ok := !ns.TryLock()
	ns.Unlock()
	report("name:持锁时TryLock被拒绝", ok, "")
}
