package main

import (
	"errors"
	"fmt"
	"strings"

	"ontology/api"
	"ontology/iface"
	"ontology/props"
	"ontology/typetree"
)

type caseResult struct {
	name string
	ok   bool
	info string
}

func main() {
	cases := []caseResult{
		{"skeleton 骨架可运行", true, ""},
	}
	cases = append(cases, typetreeCases()...)
	cases = append(cases, propsCases()...)
	cases = append(cases, ifaceCases()...)
	cases = append(cases, apiCases()...)

	fail := 0
	for _, c := range cases {
		mark := "OK"
		if !c.ok {
			mark = "FAIL"
			fail++
		}
		if c.info != "" {
			fmt.Printf("%s  %s  %s\n", mark, c.name, c.info)
		} else {
			fmt.Printf("%s  %s\n", mark, c.name)
		}
	}
	if fail > 0 {
		fmt.Printf("FAIL count=%d\n", fail)
		return
	}
	fmt.Println("ALL OK")
}

func apiCases() []caseResult {
	var out []caseResult

	a := api.New()
	matchErr(&out, "api 参数校验-空类型名", a.AddType("", nil, nil), typetree.ErrInvalidName)
	matchErr(&out, "api 参数校验-空属性类型",
		a.AddType("T", nil, []typetree.Prop{{Name: "p"}}), typetree.ErrInvalidName)

	_ = a.AddType("Text", nil, nil)
	_ = a.AddType("RichText", []string{"Text"}, nil)
	_ = a.AddType("Doc", nil, []typetree.Prop{{Name: "id", Type: "RichText"}})
	_ = a.AddInterface("HasID", []typetree.Prop{{Name: "id", Type: "Text"}})

	_, errResolve := a.Resolve("Nope")
	matchErr(&out, "api Resolve 未知类型", errResolve, typetree.ErrTypeNotFound)
	matchErr(&out, "api Check 未知接口", a.Check("Doc", "Nope"), iface.ErrInterfaceNotFound)
	matchErr(&out, "api Check 未知类型", a.Check("Nope", "HasID"), typetree.ErrTypeNotFound)
	matchErr(&out, "api Check 协变满足", a.Check("Doc", "HasID"), nil)

	sub, err := a.IsSubtype("RichText", "Text")
	out = append(out, caseResult{"api IsSubtype 子类型", err == nil && sub, ""})
	sub, err = a.IsSubtype("Text", "RichText")
	out = append(out, caseResult{"api IsSubtype 反向为假", err == nil && !sub, ""})

	return out
}

func ifaceCases() []caseResult {
	var out []caseResult

	tr := typetree.New()
	_ = tr.AddType("Text", nil, nil)
	_ = tr.AddType("RichText", []string{"Text"}, nil)
	_ = tr.AddType("Unrelated", nil, nil)
	// 实现者提供 id: RichText（接口要求 Text 的子类型 => 协变满足）。
	_ = tr.AddType("Impl", nil, []typetree.Prop{{Name: "id", Type: "RichText"}, {Name: "name", Type: "Text"}})
	// 反例：提供比要求更抽象的类型（逆变误判口径下才会通过）。
	_ = tr.AddType("BadImpl", nil, []typetree.Prop{{Name: "id", Type: "Text"}})

	reg := iface.NewRegistry(tr)
	err := reg.AddInterface("Named", []typetree.Prop{{Name: "id", Type: "Text"}, {Name: "name", Type: "Text"}})
	mustOK(&out, nil, "接口注册", err)
	err = reg.AddInterface("Strict", []typetree.Prop{{Name: "id", Type: "RichText"}})
	mustOK(&out, nil, "接口注册-严格", err)

	matchErr(&out, "协变契约满足", reg.Check("Impl", "Named"), nil)

	err = reg.Check("BadImpl", "Strict")
	matchErr(&out, "逆变误判被拒", err, iface.ErrPropTypeMismatch)

	// 缺属性：与类型不兼容必须可用 errors.Is 区分，且指出具体属性名。
	_ = tr.AddType("Partial", nil, []typetree.Prop{{Name: "id", Type: "RichText"}})
	err = reg.Check("Partial", "Named")
	if matchErr(&out, "缺属性可区分", err, iface.ErrMissingProp) {
		out = append(out, caseResult{"缺属性指出属性名",
			strings.Contains(err.Error(), "name"), err.Error()})
	}
	out = append(out, caseResult{"缺属性不被误判为类型错配",
		!errors.Is(err, iface.ErrPropTypeMismatch), ""})

	// 不相关类型既不是子类型也不是父类型 => 不兼容。
	_ = tr.AddType("Odd", nil, []typetree.Prop{{Name: "id", Type: "Unrelated"}, {Name: "name", Type: "Text"}})
	matchErr(&out, "无关类型契约不兼容", reg.Check("Odd", "Named"), iface.ErrPropTypeMismatch)

	return out
}

func propsCases() []caseResult {
	var out []caseResult

	// 两种注册/父声明顺序构建同一组类型，Resolve 结果必须逐字节相同。
	build := func(revParents bool) string {
		tr := typetree.New()
		_ = tr.AddType("Text", nil, nil)
		_ = tr.AddType("RichText", []string{"Text"}, nil)
		pa := []string{"A", "B"}
		if revParents {
			pa = []string{"B", "A"}
		}
		_ = tr.AddType("A", nil, []typetree.Prop{{Name: "id", Type: "Text"}, {Name: "shared", Type: "Text"}})
		_ = tr.AddType("B", []string{"RichText"}, []typetree.Prop{{Name: "shared", Type: "RichText"}, {Name: "age", Type: "Text"}})
		_ = tr.AddType("C", pa, []typetree.Prop{{Name: "id", Type: "RichText"}})
		got, err := props.NewResolver(tr).Resolve("C")
		if err != nil {
			return "ERR:" + err.Error()
		}
		var b strings.Builder
		for _, p := range got {
			b.WriteString(p.Name)
			b.WriteByte('=')
			b.WriteString(p.Type)
			b.WriteByte(';')
		}
		return b.String()
	}

	s1, s2 := build(false), build(true)
	out = append(out, caseResult{"Resolve 覆盖收窄与菱形取窄",
		s1 == "age=Text;id=RichText;shared=RichText;", s1})
	out = append(out, caseResult{"Resolve 顺序无关确定性",
		s1 == s2, "forward=" + s1 + " reverse=" + s2})

	return out
}

func typetreeCases() []caseResult {
	var out []caseResult

	// 合法收窄覆盖 + 菱形同类型合并 + 菱形兼容取更窄。
	good := typetree.New()
	mustOK(&out, good, "基础类型 Text", good.AddType("Text", nil, nil))
	mustOK(&out, good, "合法收窄覆盖",
		good.AddType("Animal", nil, []typetree.Prop{{Name: "name", Type: "Text"}}))
	mustOK(&out, good, "菱形同类型合并",
		good.AddType("NamedA", []string{"Animal"}, []typetree.Prop{{Name: "id", Type: "Text"}}))
	mustOK(&out, good, "菱形同类型合并-另一祖先",
		good.AddType("NamedB", []string{"Animal"}, []typetree.Prop{{Name: "id", Type: "Text"}}))
	mustOK(&out, good, "菱形兼容取更窄",
		good.AddType("RichText", []string{"Text"}, nil))
	mustOK(&out, good, "菱形兼容取更窄-宽祖先",
		good.AddType("WideA", nil, []typetree.Prop{{Name: "tag", Type: "Text"}}))
	mustOK(&out, good, "菱形兼容取更窄-窄祖先",
		good.AddType("WideB", []string{"RichText"}, []typetree.Prop{{Name: "tag", Type: "RichText"}}))
	mustOK(&out, good, "菱形兼容取更窄-合并子类",
		good.AddType("WideC", []string{"WideA", "WideB"}, nil))
	mustOK(&out, good, "菱形同类型合并-合并子类",
		good.AddType("NamedC", []string{"NamedA", "NamedB"}, nil))

	// 非法放宽覆盖必须被拒。
	bad := typetree.New()
	_ = bad.AddType("Text", nil, nil)
	_ = bad.AddType("RichText", []string{"Text"}, nil)
	_ = bad.AddType("Base", nil, []typetree.Prop{{Name: "p", Type: "RichText"}})
	err := bad.AddType("Wide", []string{"Base"}, []typetree.Prop{{Name: "p", Type: "Text"}})
	matchErr(&out, "非法放宽覆盖被拒", err, typetree.ErrBadOverride)

	// 菱形不兼容必须报错并指出两边类型。
	conf := typetree.New()
	_ = conf.AddType("X", nil, nil)
	_ = conf.AddType("Y", nil, nil)
	_ = conf.AddType("Left", nil, []typetree.Prop{{Name: "p", Type: "X"}})
	_ = conf.AddType("Right", nil, []typetree.Prop{{Name: "p", Type: "Y"}})
	err = conf.AddType("Join", []string{"Left", "Right"}, nil)
	matchErr(&out, "菱形冲突报错", err, typetree.ErrPropConflict)

	// 循环继承必须被拒且指出环上类型序列。
	cyc := typetree.New()
	err = cyc.AddType("Self", []string{"Self"}, nil)
	if matchErr(&out, "循环继承被拒", err, typetree.ErrCycle) {
		out = append(out, caseResult{"循环继承指出环序列",
			err != nil && strings.Contains(err.Error(), "Self -> Self"), err.Error()})
	}

	return out
}

func mustOK(out *[]caseResult, t *typetree.Tree, name string, err error) {
	_ = t
	*out = append(*out, caseResult{name, err == nil, errstr(err)})
}

func matchErr(out *[]caseResult, name string, err error, target error) bool {
	ok := errors.Is(err, target)
	*out = append(*out, caseResult{name, ok, errstr(err)})
	return ok
}

func errstr(err error) string {
	if err != nil {
		return err.Error()
	}
	return ""
}
