package ontology_test

import (
	"errors"
	"strings"
	"testing"

	"ontology/api"
	"ontology/typetree"
)

// TestTableDriven 以表 + 循环覆盖：覆盖方向、菱形三态、协变/逆变契约、
// 缺属性与类型不符区分、循环继承形态、参数校验、注册顺序确定性。
func TestTableDriven(t *testing.T) {
	seed := func() *api.API {
		s := api.New()
		add := func(n string, ps []string, pr ...typetree.Prop) {
			if err := s.AddType(n, ps, pr); err != nil {
				t.Fatalf("seed %s: %v", n, err)
			}
		}
		add("Any", nil)
		add("Str", []string{"Any"})
		add("WStr", []string{"Str"})
		add("Num", []string{"Any"})
		add("Base", nil, typetree.Prop{Name: "p", Type: "Str"})
		add("Left", nil, typetree.Prop{Name: "q", Type: "Str"}, typetree.Prop{Name: "l", Type: "Num"})
		add("Right", nil, typetree.Prop{Name: "q", Type: "Str"}, typetree.Prop{Name: "r", Type: "Num"})
		add("LeftN", nil, typetree.Prop{Name: "p", Type: "Str"})
		add("RightN", nil, typetree.Prop{Name: "p", Type: "WStr"})
		return s
	}

	t.Run("覆盖方向", func(t *testing.T) {
		for _, c := range []struct {
			child, typ string
			want       error
		}{
			{"Same", "Str", nil}, {"Narrow", "WStr", nil},
			{"Widen", "Any", api.ErrInvalidOverride},
			{"Unrel", "Num", api.ErrInvalidOverride},
		} {
			s := seed()
			err := s.AddType(c.child, []string{"Base"},
				[]typetree.Prop{{Name: "p", Type: c.typ}})
			if !match(err, c.want) {
				t.Errorf("%s p:%s err=%v want %v", c.child, c.typ, err, c.want)
			}
		}
	})

	t.Run("菱形三态", func(t *testing.T) {
		for _, c := range []struct {
			name       string
			parents    []string
			wantPType  string
			wantNProps int
		}{
			{"JoinSame", []string{"Left", "Right"}, "", 3},
			{"JoinNarrow", []string{"LeftN", "RightN"}, "WStr", 1},
		} {
			s := seed()
			if err := s.AddType(c.name, c.parents, nil); err != nil {
				t.Fatalf("%s: %v", c.name, err)
			}
			got, err := s.Resolve(c.name)
			if err != nil || len(got) != c.wantNProps {
				t.Fatalf("%s props=%v err=%v", c.name, got, err)
			}
			if c.wantPType != "" && got[0].Type != c.wantPType {
				t.Errorf("%s p=%s want %s", c.name, got[0].Type, c.wantPType)
			}
		}
		s := seed()
		_ = s.AddType("BadL", nil, []typetree.Prop{{Name: "p", Type: "Str"}})
		_ = s.AddType("BadR", nil, []typetree.Prop{{Name: "p", Type: "Num"}})
		err := s.AddType("BadJoin", []string{"BadL", "BadR"}, nil)
		if !errors.Is(err, api.ErrPropConflict) ||
			!strings.Contains(err.Error(), "BadL.p:Str") ||
			!strings.Contains(err.Error(), "BadR.p:Num") {
			t.Errorf("conflict err=%v", err)
		}
	})

	t.Run("契约协变", func(t *testing.T) {
		for _, c := range []struct {
			typ, iface string
			want       error
		}{
			{"NSame", "ReqStr", nil}, {"NSub", "ReqStr", nil},
			{"Base", "ReqWStr", api.ErrPropTypeMismatch},
			{"Num", "ReqStr", api.ErrMissingProp},
		} {
			s := seed()
			_ = s.AddType("NSame", []string{"Base"}, []typetree.Prop{{Name: "p", Type: "Str"}})
			_ = s.AddType("NSub", []string{"Base"}, []typetree.Prop{{Name: "p", Type: "WStr"}})
			_ = s.AddInterface("ReqStr", []typetree.Prop{{Name: "p", Type: "Str"}})
			_ = s.AddInterface("ReqWStr", []typetree.Prop{{Name: "p", Type: "WStr"}})
			if err := s.Check(c.typ, c.iface); !match(err, c.want) {
				t.Errorf("Check(%s,%s)=%v want %v", c.typ, c.iface, err, c.want)
			}
		}
	})

	t.Run("缺属性与类型不符可区分", func(t *testing.T) {
		s := seed()
		_ = s.AddInterface("I", []typetree.Prop{{Name: "p", Type: "WStr"}, {Name: "z", Type: "Str"}})
		err := s.Check("Num", "I")
		if !errors.Is(err, api.ErrMissingProp) ||
			errors.Is(err, api.ErrPropTypeMismatch) ||
			!strings.Contains(err.Error(), "p") {
			t.Errorf("missing err=%v", err)
		}
		err = s.Check("Base", "I")
		if !errors.Is(err, api.ErrPropTypeMismatch) || errors.Is(err, api.ErrMissingProp) {
			t.Errorf("mismatch err=%v", err)
		}
	})

	t.Run("循环继承与父缺失", func(t *testing.T) {
		for _, self := range []string{"X", "Y"} {
			s := seed()
			err := s.AddType(self, []string{self}, nil)
			if !errors.Is(err, api.ErrCycle) ||
				!strings.Contains(err.Error(), self+" -> "+self) {
				t.Errorf("self-cycle err=%v", err)
			}
		}
		s := seed()
		if err := s.AddType("Orphan", []string{"Ghost"}, nil); !errors.Is(err, typetree.ErrParentNotFound) {
			t.Errorf("parent missing err=%v", err)
		}
	})

	t.Run("参数校验", func(t *testing.T) {
		s := seed()
		if err := s.AddType("", nil, nil); !errors.Is(err, typetree.ErrEmptyName) {
			t.Errorf("empty type err=%v", err)
		}
		if err := s.AddType("Str", nil, nil); !errors.Is(err, typetree.ErrTypeExists) {
			t.Errorf("dup err=%v", err)
		}
		if err := s.AddInterface("", nil); err == nil {
			t.Error("empty interface accepted")
		}
		if _, err := s.Resolve("Ghost"); !errors.Is(err, api.ErrTypeNotFound) {
			t.Errorf("resolve ghost err=%v", err)
		}
		if s.IsSubtype("Str", "") {
			t.Error("empty super accepted")
		}
	})

	t.Run("注册顺序确定性", func(t *testing.T) {
		build := func(ord []string) []typetree.Prop {
			s := api.New()
			for _, n := range ord {
				switch n {
				case "Left":
					_ = s.AddType("Left", nil, []typetree.Prop{{Name: "q", Type: "Str"}, {Name: "l", Type: "Num"}})
				case "Right":
					_ = s.AddType("Right", nil, []typetree.Prop{{Name: "q", Type: "Str"}, {Name: "r", Type: "Num"}})
				case "Join":
					if err := s.AddType("Join", []string{"Left", "Right"}, nil); err != nil {
						t.Fatal(err)
					}
				}
			}
			got, err := s.Resolve("Join")
			if err != nil {
				t.Fatal(err)
			}
			return got
		}
		a := build([]string{"Left", "Right", "Join"})
		b := build([]string{"Right", "Left", "Join"})
		if len(a) != len(b) {
			t.Fatalf("len differ: %d %d", len(a), len(b))
		}
		for i := range a {
			if a[i] != b[i] {
				t.Errorf("order-dependent: %v vs %v", a, b)
			}
		}
	})
}

func match(err, want error) bool {
	if want == nil {
		return err == nil
	}
	return errors.Is(err, want)
}
