package main

import (
	"fmt"
	"strings"

	"ontology/logical"
)

type check struct {
	name string
	ok   bool
}

func assemble(in string) []string {
	lines, err := logical.Split(strings.NewReader(in))
	if err != nil {
		panic(err)
	}
	var got []string
	for _, l := range lines {
		if l.Kind == logical.Entry {
			got = append(got, l.Text)
		}
	}
	return got
}

func main() {
	var checks []check

	got := assemble("k=v\\\n   w\nk=v\\\\\nx=y\n# c\n\nk=a\\\n#b\n")
	checks = append(checks, check{"logical continuation/comment",
		fmt.Sprint(got) == "[k=vw k=v\\\\ x=y k=a#b]"})

	pass := 0
	for _, c := range checks {
		mark := "FAIL"
		if c.ok {
			mark = "OK"
			pass++
		}
		fmt.Printf("%s  %s\n", mark, c.name)
	}
	fmt.Printf("TOTAL %d/%d\n", pass, len(checks))
}
