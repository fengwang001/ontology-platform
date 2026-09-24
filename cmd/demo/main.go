package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"

	"ontology/api"
	"ontology/fk"
	"ontology/sch"
)

var failed bool

func check(name string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failed = true
	}
	fmt.Printf("%s %s\n", status, name)
}

func main() {
	// sch：引用计数随子行增删变化
	st := sch.New()
	st.AddParent("p1")
	st.AddChild("k1", "p1")
	st.AddChild("k2", "p1")
	st.DelChild("k1")
	check("sch refcount", st.RefCount("p1") == 1 && st.HasParent("p1") && !st.HasChild("k1"))

	// fk：四类哨兵错误互不相同，拒绝后状态不变
	s2 := sch.New()
	_ = fk.PIns(s2, "p1")
	_ = fk.CIns(s2, "k1", "p1")
	errs := []error{fk.CIns(s2, "k9", "nope"), fk.PDel(s2, "p1"), fk.PDel(s2, "nope"), fk.CDel(s2, "nope")}
	want := []error{fk.ErrOrphan, fk.ErrParentInUse, fk.ErrNoParent, fk.ErrNoChild}
	distinct := map[error]bool{}
	ok := true
	for i, e := range errs {
		ok = ok && errors.Is(e, want[i])
		distinct[e] = true
	}
	ok = ok && len(distinct) == 4 && fk.Check(s2) == nil
	ok = ok && s2.HasParent("p1") && s2.HasChild("k1") && s2.RefCount("p1") == 1
	check("fk sentinel errors", ok)

	// api：十三步序列逐步判定与双视图（含父晚到重投、幂等、失败不留痕）
	e := api.New()
	steps := []struct {
		run func() error
		rej bool
		vp  []string
		vc  map[string]string
	}{
		{func() error { return e.CIns("k1", "p1") }, true, []string{}, map[string]string{}},
		{func() error { return e.PIns("p1") }, false, []string{"p1"}, map[string]string{}},
		{func() error { return e.CIns("k1", "p1") }, false, []string{"p1"}, map[string]string{"k1": "p1"}},
		{func() error { return e.CIns("k2", "p2") }, true, []string{"p1"}, map[string]string{"k1": "p1"}},
		{func() error { return e.PDel("p1") }, true, []string{"p1"}, map[string]string{"k1": "p1"}},
		{func() error { return e.CIns("k3", "p1") }, false, []string{"p1"}, map[string]string{"k1": "p1", "k3": "p1"}},
		{func() error { return e.CDel("k1") }, false, []string{"p1"}, map[string]string{"k3": "p1"}},
		{func() error { return e.CDel("k2") }, true, []string{"p1"}, map[string]string{"k3": "p1"}},
		{func() error { return e.CDel("k3") }, false, []string{"p1"}, map[string]string{}},
		{func() error { return e.PDel("p1") }, false, []string{}, map[string]string{}},
		{func() error { return e.CIns("k3", "p1") }, true, []string{}, map[string]string{}},
		{func() error { return e.PIns("p1") }, false, []string{"p1"}, map[string]string{}},
		{func() error { return e.CIns("k3", "p1") }, false, []string{"p1"}, map[string]string{"k3": "p1"}},
	}
	ok = true
	for i, stp := range steps {
		err := stp.run()
		if (err != nil) != stp.rej || !reflect.DeepEqual(e.ViewP(), stp.vp) ||
			!reflect.DeepEqual(e.ViewC(), stp.vc) {
			fmt.Printf("  step %d got err=%v ViewP=%v ViewC=%v\n", i+1, err, e.ViewP(), e.ViewC())
			ok = false
		}
	}
	check("api 13-step derivation", ok)
	check("api SelfCheck", e.SelfCheck() == nil)

	if failed {
		os.Exit(1)
	}
}
