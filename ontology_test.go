package ontology_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"ontology/api"
	"ontology/iface"
	"ontology/props"
	"ontology/typetree"
)

// 表驱动：所有「方向/三态/协变正反/循环形态」均由循环驱动，不展开成多个测试函数。

type addTypeCase struct {
	name    string
	parents []string
	own     []typetree.Prop
	wantErr error // nil 表示期望成功
}

func TestAddTypeValidation(t *testing.T) {
	tests := []struct {
		label string
		seed  func(*typetree.Tree)
		cases []addTypeCase
	}{
		{
			label: "父不存在/重名/空名",
			cases: []addTypeCase{
				{"A", nil, nil, nil},
				{"A", nil, nil, typetree.ErrDuplicateType},
				{"", nil, nil, typetree.ErrInvalidName},
				{"Orphan", []string{"Ghost"}, nil, typetree.ErrParentNotFound},
			},
		},
		{
			label: "覆盖方向：相同或更窄合法，放宽拒绝",
			seed: func(tr *typetree.Tree) {
				must(tr.AddType("Text", nil, nil))
				must(tr.AddType("RichText", []string{"Text"}, nil))
				must(tr.AddType("Base", nil, []typetree.Prop{{Name: "p", Type: "RichText"}}))
			},
			cases: []addTypeCase{
				{"Same", []string{"Base"}, []typetree.Prop{{Name: "p", Type: "RichText"}}, nil},
				{"Narrow", []string{"Base"}, []typetree.Prop{{Name: "p", Type: "RichText"}}, nil},
				{"Widen", []string{"Base"}, []typetree.Prop{{Name: "p", Type: "Text"}}, typetree.ErrBadOverride},
			},
		},
		{
			label: "菱形三态：同类型合并 / 兼容取窄 / 不兼容报错",
			seed: func(tr *typetree.Tree) {
				must(tr.AddType("Text", nil, nil))
				must(tr.AddType("RichText", []string{"Text"}, nil))
				must(tr.AddType("X", nil, nil))
				must(tr.AddType("Y", nil, nil))
				must(tr.AddType("EqL", nil, []typetree.Prop{{Name: "eq", Type: "Text"}}))
				must(tr.AddType("EqR", nil, []typetree.Prop{{Name: "eq", Type: "Text"}}))
				must(tr.AddType("NarrowL", nil, []typetree.Prop{{Name: "nr", Type: "Text"}}))
				must(tr.AddType("NarrowR", []string{"RichText"}, []typetree.Prop{{Name: "nr", Type: "RichText"}}))
				must(tr.AddType("BadL", nil, []typetree.Prop{{Name: "cf", Type: "X"}}))
				must(tr.AddType("BadR", nil, []typetree.Prop{{Name: "cf", Type: "Y"}}))
			},
			cases: []addTypeCase{
				{"EqJoin", []string{"EqL", "EqR"}, nil, nil},
				{"NrJoin", []string{"NarrowL", "NarrowR"}, nil, nil},
				{"BadJoin", []string{"BadL", "BadR"}, nil, typetree.ErrPropConflict},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.label, func(t *testing.T) {
			tr := typetree.New()
			if tc.seed != nil {
				tc.seed(tr)
			}
			for _, c := range tc.cases {
				err := tr.AddType(c.name, c.parents, c.own)
				if !errIs(err, c.wantErr) {
					t.Fatalf("AddType(%s) err=%v, want %v", c.name, err, c.wantErr)
				}
			}
		})
	}

	// 循环继承：父必须先注册，故公开 API 可构造的环只有自环
	// （直接把自己列为父）；环错误必须指出环上类型序列。
	cycleCases := []struct {
		label   string
		name    string
		parents []string
		setup   func(*typetree.Tree)
		wantOK  bool
		wantSeq string
	}{
		{"直接自环", "Self", []string{"Self"}, nil, false, "Self -> Self"},
		{"普通已存在父不成环", "Child", []string{"P"}, func(tr *typetree.Tree) {
			must(tr.AddType("P", nil, nil))
		}, true, ""},
	}
	t.Run("循环继承形态", func(t *testing.T) {
		for _, cc := range cycleCases {
			tr := typetree.New()
			if cc.setup != nil {
				cc.setup(tr)
			}
			err := tr.AddType(cc.name, cc.parents, nil)
			if cc.wantOK {
				if err != nil {
					t.Fatalf("%s: unexpected err %v", cc.label, err)
				}
				continue
			}
			if !errors.Is(err, typetree.ErrCycle) {
				t.Fatalf("%s: err=%v want ErrCycle", cc.label, err)
			}
			if !strings.Contains(err.Error(), cc.wantSeq) {
				t.Fatalf("%s: cycle err=%q does not contain %q", cc.label, err.Error(), cc.wantSeq)
			}
		}
	})
}

func TestResolveAndDeterminism(t *testing.T) {
	want := []typetree.Prop{
		{Name: "age", Type: "Text"},
		{Name: "id", Type: "RichText"},
		{Name: "shared", Type: "RichText"},
	}
	build := func(swap bool) []typetree.Prop {
		tr := typetree.New()
		must(tr.AddType("Text", nil, nil))
		must(tr.AddType("RichText", []string{"Text"}, nil))
		pa := []string{"A", "B"}
		if swap {
			pa = []string{"B", "A"}
		}
		must(tr.AddType("A", nil, []typetree.Prop{{Name: "id", Type: "Text"}, {Name: "shared", Type: "Text"}}))
		must(tr.AddType("B", []string{"RichText"}, []typetree.Prop{{Name: "shared", Type: "RichText"}, {Name: "age", Type: "Text"}}))
		must(tr.AddType("C", pa, []typetree.Prop{{Name: "id", Type: "RichText"}}))
		got, err := props.NewResolver(tr).Resolve("C")
		must(err)
		return got
	}
	for _, swap := range []bool{false, true} {
		got := build(swap)
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("swap=%v got=%v want=%v", swap, got, want)
		}
	}
	if _, err := props.NewResolver(typetree.New()).Resolve("Missing"); !errors.Is(err, typetree.ErrTypeNotFound) {
		t.Fatalf("Resolve missing type err=%v", err)
	}
}

func TestInterfaceCovariance(t *testing.T) {
	tr := typetree.New()
	must(tr.AddType("Text", nil, nil))
	must(tr.AddType("RichText", []string{"Text"}, nil))
	must(tr.AddType("Other", nil, nil))
	must(tr.AddType("Good", nil, []typetree.Prop{{Name: "p", Type: "RichText"}}))
	must(tr.AddType("Wider", nil, []typetree.Prop{{Name: "p", Type: "Text"}}))
	must(tr.AddType("Odd", nil, []typetree.Prop{{Name: "p", Type: "Other"}}))
	must(tr.AddType("None", nil, nil))

	reg := iface.NewRegistry(tr)
	must(reg.AddInterface("ReqText", []typetree.Prop{{Name: "p", Type: "Text"}}))

	cases := []struct {
		typ     string
		wantErr error
	}{
		{"Good", nil},                      // U=RichText ≼ Text=T：协变满足
		{"Wider", nil},                     // U==Text 相同：满足
		{"Odd", iface.ErrPropTypeMismatch}, // 互不兼容
		{"None", iface.ErrMissingProp},     // 缺属性
	}
	for _, c := range cases {
		err := reg.Check(c.typ, "ReqText")
		if !errIs(err, c.wantErr) {
			t.Fatalf("Check(%s) err=%v want %v", c.typ, err, c.wantErr)
		}
	}

	// 严格接口要求 RichText：提供 Text（更抽象/逆变口径）必须拒绝。
	must(reg.AddInterface("ReqRich", []typetree.Prop{{Name: "p", Type: "RichText"}}))
	if err := reg.Check("Wider", "ReqRich"); !errors.Is(err, iface.ErrPropTypeMismatch) {
		t.Fatalf("contravariant wrong-direction err=%v", err)
	}

	// 缺属性错误必须点名属性，且与类型错配互不混淆。
	err := reg.Check("None", "ReqText")
	if !errors.Is(err, iface.ErrMissingProp) || !strings.Contains(err.Error(), "p") {
		t.Fatalf("missing prop err=%v", err)
	}
	if errors.Is(err, iface.ErrPropTypeMismatch) {
		t.Fatal("ErrMissingProp must not also match ErrPropTypeMismatch")
	}
	if err := reg.Check("Good", "NoSuch"); !errors.Is(err, iface.ErrInterfaceNotFound) {
		t.Fatalf("unknown interface err=%v", err)
	}
}

func TestAPIEntry(t *testing.T) {
	a := api.New()
	if err := a.AddInterface("I", nil); err != nil {
		t.Fatal(err)
	}
	if err := a.AddType("", nil, nil); !errors.Is(err, typetree.ErrInvalidName) {
		t.Fatalf("empty name err=%v", err)
	}
	must(a.AddType("Text", nil, nil))
	must(a.AddType("RichText", []string{"Text"}, nil))
	must(a.AddType("Doc", nil, []typetree.Prop{{Name: "body", Type: "RichText"}}))
	must(a.AddInterface("HasBody", []typetree.Prop{{Name: "body", Type: "Text"}}))

	if err := a.Check("Doc", "HasBody"); err != nil {
		t.Fatalf("covariant satisfy err=%v", err)
	}
	if ok, err := a.IsSubtype("RichText", "Text"); err != nil || !ok {
		t.Fatalf("IsSubtype true-branch ok=%v err=%v", ok, err)
	}
	if ok, _ := a.IsSubtype("Text", "RichText"); ok {
		t.Fatal("Text must not be subtype of RichText")
	}
	got, err := a.Resolve("Doc")
	must(err)
	if len(got) != 1 || got[0] != (typetree.Prop{Name: "body", Type: "RichText"}) {
		t.Fatalf("Resolve via api got=%v", got)
	}
}

func errIs(err, target error) bool {
	if target == nil {
		return err == nil
	}
	return errors.Is(err, target)
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
