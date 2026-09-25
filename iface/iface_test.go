package iface

import (
	"errors"
	"strings"
	"testing"

	"ontology/typetree"
)

func buildTree(t *testing.T) *typetree.Tree {
	t.Helper()
	tr := typetree.New()
	decls := []struct {
		name    string
		parents []string
		props   map[string]string
	}{
		{"Living", nil, nil},
		{"Animal", []string{"Living"}, map[string]string{"friend": "Animal", "name": "string"}},
		{"Dog", []string{"Animal"}, map[string]string{"friend": "Dog"}},
	}
	for _, d := range decls {
		if err := tr.AddType(d.name, d.parents, d.props); err != nil {
			t.Fatal(err)
		}
	}
	return tr
}

// TestCheck 表驱动遍历契约判定：协变满足、逆变被拒、缺属性、未知接口。
func TestCheck(t *testing.T) {
	tr := buildTree(t)
	r := New()
	ifaces := map[string]map[string]string{
		"Friendly":     {"friend": "Animal"},
		"StrictFriend": {"friend": "Dog"},
		"Coded":        {"code": "string"},
		"Named":        {"name": "string"},
	}
	for name, req := range ifaces {
		if err := r.AddInterface(name, req); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		typeName  string
		ifaceName string
		wantErr   error  // nil 表示应满足契约
		wantProp  string // 缺属性时报错应包含的属性名
	}{
		{"Dog", "Friendly", nil, ""},                        // 协变：Dog < Animal
		{"Dog", "StrictFriend", nil, ""},                    // 恰好相等
		{"Animal", "Friendly", nil, ""},                     // 恰好相等
		{"Animal", "StrictFriend", ErrIncompatibleType, ""}, // 逆变被拒
		{"Dog", "Coded", ErrMissingProperty, "code"},        // 缺属性
		{"Dog", "Named", nil, ""},                           // 继承来的属性也算
		{"Dog", "Ghost", nil, ""},                           // 未知接口：非哨兵错误
	}
	for _, c := range cases {
		err := r.Check(tr, c.typeName, c.ifaceName)
		switch {
		case c.ifaceName == "Ghost":
			if err == nil || errors.Is(err, ErrMissingProperty) || errors.Is(err, ErrIncompatibleType) {
				t.Errorf("Check(%s,%s) = %v, want plain unknown-interface error", c.typeName, c.ifaceName, err)
			}
		case c.wantErr == nil:
			if err != nil {
				t.Errorf("Check(%s,%s) = %v, want nil", c.typeName, c.ifaceName, err)
			}
		default:
			if !errors.Is(err, c.wantErr) {
				t.Errorf("Check(%s,%s) = %v, want errors.Is %v", c.typeName, c.ifaceName, err, c.wantErr)
			}
			other := ErrMissingProperty
			if c.wantErr == ErrMissingProperty {
				other = ErrIncompatibleType
			}
			if errors.Is(err, other) {
				t.Errorf("Check(%s,%s) = %v, must not match %v", c.typeName, c.ifaceName, err, other)
			}
			if c.wantProp != "" && !strings.Contains(err.Error(), c.wantProp) {
				t.Errorf("Check(%s,%s) = %v, want mention of %q", c.typeName, c.ifaceName, err, c.wantProp)
			}
		}
	}
	if r.missing != 1 {
		t.Errorf("missing counter = %d, want 1", r.missing)
	}
	if r.incompatible != 1 {
		t.Errorf("incompatible counter = %d, want 1", r.incompatible)
	}
}

// TestAddInterface 校验注册期参数检查。
func TestAddInterface(t *testing.T) {
	r := New()
	cases := []struct {
		name string
		req  map[string]string
		ok   bool
	}{
		{"", nil, false},
		{"I1", map[string]string{"": "string"}, false},
		{"I1", map[string]string{"p": "string"}, true},
		{"I1", nil, false},
	}
	for _, c := range cases {
		err := r.AddInterface(c.name, c.req)
		if got := err == nil; got != c.ok {
			t.Errorf("AddInterface(%q) ok=%v, want %v (err=%v)", c.name, got, c.ok, err)
		}
	}
}
