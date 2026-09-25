package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"ontology/api"
	"ontology/typetree"
)

func report(name string, ok bool) bool {
	if ok {
		fmt.Printf("OK   %s\n", name)
		return true
	}
	fmt.Printf("FAIL %s\n", name)
	return false
}

func main() {
	allOK := true
	sys := api.New()
	mustAdd := func(name string, parents []string, props ...typetree.Prop) {
		if err := sys.AddType(name, parents, props); err != nil {
			panic(err)
		}
	}
	mustAdd("Any", nil)
	mustAdd("Str", []string{"Any"})
	mustAdd("WStr", []string{"Str"})
	mustAdd("Num", []string{"Any"})

	// 合法收窄覆盖：Base.p:Str，Narrow.p:WStr（WStr 是 Str 子类型）。
	mustAdd("Base", nil, typetree.Prop{Name: "p", Type: "Str"})
	errNarrow := sys.AddType("Narrow", []string{"Base"},
		[]typetree.Prop{{Name: "p", Type: "WStr"}})
	allOK = report("合法收窄覆盖", errNarrow == nil) && allOK

	// 非法放宽覆盖：Wide.p:Any（Any 是 Str 的父类型）必须被拒。
	errWide := sys.AddType("Wide", []string{"Base"},
		[]typetree.Prop{{Name: "p", Type: "Any"}})
	allOK = report("非法放宽被拒", errors.Is(errWide, api.ErrInvalidOverride)) && allOK

	// 菱形同类型合并：Left/Right 都有 shared:Str。
	mustAdd("Left", nil,
		typetree.Prop{Name: "shared", Type: "Str"},
		typetree.Prop{Name: "l", Type: "Num"})
	mustAdd("Right", nil,
		typetree.Prop{Name: "shared", Type: "Str"},
		typetree.Prop{Name: "r", Type: "Num"})
	errJoin := sys.AddType("Join", []string{"Left", "Right"}, nil)
	joinProps, _ := sys.Resolve("Join")
	joinMerged := errJoin == nil && len(joinProps) == 3
	allOK = report("菱形同类型合并", joinMerged) && allOK

	// 菱形兼容取更窄：p 一边 Str 一边 WStr，结果取 WStr。
	mustAdd("LeftN", nil, typetree.Prop{Name: "p", Type: "Str"})
	mustAdd("RightN", nil, typetree.Prop{Name: "p", Type: "WStr"})
	errJN := sys.AddType("JoinN", []string{"LeftN", "RightN"}, nil)
	jnProps, _ := sys.Resolve("JoinN")
	jnNarrow := errJN == nil && len(jnProps) == 1 && jnProps[0].Type == "WStr"
	allOK = report("菱形兼容取更窄", jnNarrow) && allOK

	// 菱形冲突：Str 与 Num 互不兼容，报错指出两边类型。
	mustAdd("BadL", nil, typetree.Prop{Name: "p", Type: "Str"})
	mustAdd("BadR", nil, typetree.Prop{Name: "p", Type: "Num"})
	errBad := sys.AddType("BadJoin", []string{"BadL", "BadR"}, nil)
	badOK := errors.Is(errBad, api.ErrPropConflict) &&
		strings.Contains(errBad.Error(), "BadL.p:Str") &&
		strings.Contains(errBad.Error(), "BadR.p:Num")
	allOK = report("菱形冲突报错", badOK) && allOK

	// 接口契约：协变满足、逆变被拒、缺属性与类型不符可区分。
	mustIface := func(n string, pr []typetree.Prop) {
		if e := sys.AddInterface(n, pr); e != nil {
			panic(e)
		}
	}
	mustIface("IStr", []typetree.Prop{{Name: "p", Type: "Str"}})
	mustIface("IWStr", []typetree.Prop{{Name: "p", Type: "WStr"}})
	allOK = report("协变契约满足", sys.Check("Narrow", "IStr") == nil) && allOK
	errContra := sys.Check("Base", "IWStr")
	allOK = report("逆变误判被拒",
		errors.Is(errContra, api.ErrPropTypeMismatch) &&
			!errors.Is(errContra, api.ErrMissingProp)) && allOK
	errMiss := sys.Check("Num", "IStr")
	missOK := errors.Is(errMiss, api.ErrMissingProp) &&
		!errors.Is(errMiss, api.ErrPropTypeMismatch) &&
		strings.Contains(errMiss.Error(), "p")
	allOK = report("缺属性可区分", missOK) && allOK

	// 循环继承被拒且指出环：X 以自身为父。
	errCycle := sys.AddType("X", []string{"X"}, nil)
	cycleOK := errors.Is(errCycle, api.ErrCycle) &&
		strings.Contains(errCycle.Error(), "X -> X")
	allOK = report("循环继承被拒且指出环", cycleOK) && allOK

	// 跨模块不变量：有效属性按名排序，且不同注册/列举顺序结果一致。
	got, errP := sys.Resolve("Join")
	sortedOK := errP == nil && len(got) == 3 &&
		got[0].Name == "l" && got[1].Name == "r" && got[2].Name == "shared"
	allOK = report("有效属性按名排序", sortedOK) && allOK

	sys2 := api.New()
	add2 := func(n string, ps []string, pr []typetree.Prop) {
		if e := sys2.AddType(n, ps, pr); e != nil {
			panic(e)
		}
	}
	add2("Right", nil, []typetree.Prop{{Name: "shared", Type: "Str"}, {Name: "r", Type: "Num"}})
	add2("Left", nil, []typetree.Prop{{Name: "shared", Type: "Str"}, {Name: "l", Type: "Num"}})
	add2("Join", []string{"Right", "Left"}, nil)
	got2, _ := sys2.Resolve("Join")
	detOK := len(got) == len(got2)
	for i := range got {
		detOK = detOK && got[i] == got2[i]
	}
	allOK = report("有效属性确定性", detOK) && allOK
	allOK = report("IsSubtype 判定", sys.IsSubtype("WStr", "Str") &&
		!sys.IsSubtype("Str", "WStr")) && allOK

	if !allOK {
		fmt.Println("demo: 存在失败项")
		os.Exit(1)
	}
}
