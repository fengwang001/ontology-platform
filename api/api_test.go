package api

import (
	"errors"
	"testing"
)

func buildOntology(t *testing.T) *Ontology {
	t.Helper()
	o := New()
	types := []struct {
		name    string
		parents []string
		props   map[string]string
	}{
		{"Living", nil, nil},
		{"Animal", []string{"Living"}, map[string]string{"friend": "Animal"}},
		{"Dog", []string{"Animal"}, map[string]string{"friend": "Dog"}},
	}
	for _, d := range types {
		if err := o.AddType(d.name, d.parents, d.props); err != nil {
			t.Fatal(err)
		}
	}
	if err := o.AddInterface("Friendly", map[string]string{"friend": "Animal"}); err != nil {
		t.Fatal(err)
	}
	return o
}

// TestValidation 表驱动遍历参数校验：空名、未知类型、未知接口。
func TestValidation(t *testing.T) {
	o := buildOntology(t)
	cases := []struct {
		name   string
		call   func() error
		want   error // 非 nil 时用 errors.Is 判定
		anyErr bool  // true 表示只要求报错即可
	}{
		{"AddType-empty", func() error { return o.AddType("", nil, nil) }, nil, true},
		{"AddIface-empty", func() error { return o.AddInterface("", nil) }, nil, true},
		{"Resolve-empty", func() error { _, err := o.Resolve(""); return err }, nil, true},
		{"Resolve-unknown", func() error { _, err := o.Resolve("Ghost"); return err }, ErrUnknownType, false},
		{"Check-empty", func() error { return o.Check("", "") }, nil, true},
		{"Check-unknown-type", func() error { return o.Check("Ghost", "Friendly") }, ErrUnknownType, false},
		{"Check-unknown-iface", func() error { return o.Check("Dog", "Ghost") }, ErrUnknownInterface, false},
		{"IsSubtype-empty", func() error {
			if o.IsSubtype("", "Animal") || o.IsSubtype("Dog", "") {
				return errors.New("empty name must be false")
			}
			return nil
		}, nil, false},
	}
	for _, c := range cases {
		err := c.call()
		switch {
		case c.anyErr:
			if err == nil {
				t.Errorf("%s: got nil, want error", c.name)
			}
		case c.want == nil:
			if err != nil {
				t.Errorf("%s: got %v, want nil", c.name, err)
			}
		default:
			if !errors.Is(err, c.want) {
				t.Errorf("%s: got %v, want errors.Is %v", c.name, err, c.want)
			}
		}
	}
}

// TestEndToEnd 通过唯一入口遍历核心语义：覆盖方向、契约协变、子类型判定。
func TestEndToEnd(t *testing.T) {
	o := buildOntology(t)
	cases := []struct {
		name string
		call func() error
		want error
	}{
		{"widen-override", func() error {
			return o.AddType("Bad", []string{"Animal"}, map[string]string{"friend": "Living"})
		}, ErrOverride},
		{"self-cycle", func() error {
			return o.AddType("Loop", []string{"Loop"}, nil)
		}, ErrCycle},
		{"covariant-ok", func() error { return o.Check("Dog", "Friendly") }, nil},
		{"contravariant-rejected", func() error {
			if err := o.AddInterface("Strict", map[string]string{"friend": "Dog"}); err != nil {
				return err
			}
			return o.Check("Animal", "Strict")
		}, ErrIncompatibleType},
		{"missing-property", func() error {
			if err := o.AddInterface("Coded", map[string]string{"code": "string"}); err != nil {
				return err
			}
			return o.Check("Dog", "Coded")
		}, ErrMissingProperty},
	}
	for _, c := range cases {
		err := c.call()
		if c.want == nil {
			if err != nil {
				t.Errorf("%s: got %v, want nil", c.name, err)
			}
			continue
		}
		if !errors.Is(err, c.want) {
			t.Errorf("%s: got %v, want errors.Is %v", c.name, err, c.want)
		}
	}
	// IsSubtype 传递闭包
	for _, d := range []struct {
		sub, super string
		want       bool
	}{
		{"Dog", "Living", true}, {"Dog", "Dog", true}, {"Living", "Dog", false},
	} {
		if got := o.IsSubtype(d.sub, d.super); got != d.want {
			t.Errorf("IsSubtype(%s,%s) = %v, want %v", d.sub, d.super, got, d.want)
		}
	}
	// Resolve 排序确定性：属性名升序
	props, err := o.Resolve("Dog")
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < len(props); i++ {
		if props[i-1].Name >= props[i].Name {
			t.Errorf("Resolve not sorted: %v", props)
		}
	}
}
