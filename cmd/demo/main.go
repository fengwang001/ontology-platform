package main

import (
	"fmt"
	"os"
	"reflect"

	"ontology/dag"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
		fmt.Println("FAIL", name)
	} else {
		fmt.Println("OK  ", name)
	}
}

// checkDirtySets 核验第三节八行表：每步 SetBase 后的脏视图集合。
func checkDirtySets() {
	g := dag.New()
	for _, b := range []string{"b1", "b2", "b3"} {
		g.AddBase(b)
	}
	g.AddView("v1", []string{"b1"})
	g.AddView("v2", []string{"b2"})
	g.AddView("v3", []string{"v1", "v2"})
	g.AddView("v4", []string{"v3", "b3"})
	seq := []string{"b1", "b3", "b2", "b1", "b2", "b3", "b1", "b2"}
	want := []map[string]bool{
		{"v1": true, "v3": true, "v4": true},
		{"v1": true, "v3": true, "v4": true},
		{"v1": true, "v2": true, "v3": true, "v4": true},
		{"v1": true, "v2": true, "v3": true, "v4": true},
		{"v1": true, "v2": true, "v3": true, "v4": true},
		{"v1": true, "v2": true, "v3": true, "v4": true},
		{"v1": true, "v2": true, "v3": true, "v4": true},
		{"v1": true, "v2": true, "v3": true, "v4": true},
	}
	var changed []string
	ok := true
	for i, b := range seq {
		changed = append(changed, b)
		if !reflect.DeepEqual(g.Dirty(changed), want[i]) {
			ok = false
		}
	}
	check("dag: 八步脏集与推导表一致", ok)
}

func main() {
	checkDirtySets()
	if failed {
		os.Exit(1)
	}
}
