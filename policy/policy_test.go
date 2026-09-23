package policy

import (
	"errors"
	"testing"
)

func TestCanonical(t *testing.T) {
	cases := []struct {
		name string
		in   string
		p    Policy
		want string
	}{
		{"全小写", "accept", Policy{}, "Accept"},
		{"全大写", "ACCEPT", Policy{}, "Accept"},
		{"连字符分段", "x-custom-header", Policy{}, "X-Custom-Header"},
		{"混合", "cOnTeNt-TyPe", Policy{}, "Content-Type"},
		{"数字段", "h-2x", Policy{}, "H-2x"},
		{"大小写敏感保留原名", "x-Case", Policy{CaseSens: true}, "x-Case"},
		{"显式规范形态", "etag", Policy{CanonicalTo: "ETag"}, "ETag"},
	}
	for _, c := range cases {
		if got := Canonical(c.in, c.p); got != c.want {
			t.Errorf("%s: Canonical(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

func TestSingle(t *testing.T) {
	vals := []string{"a", "b", "c"}
	cases := []struct {
		name string
		p    Policy
		vals []string
		want string
		err  error
	}{
		{"空集", Policy{}, nil, "", nil},
		{"单值", Policy{Dup: DupError}, []string{"x"}, "x", nil},
		{"取首个", Policy{Dup: DupFirst}, vals, "a", nil},
		{"取末个", Policy{Dup: DupLast}, vals, "c", nil},
		{"合并", Policy{Dup: DupMerge}, vals, "a, b, c", nil},
		{"报错", Policy{Dup: DupError}, vals, "", ErrDuplicate},
	}
	for _, c := range cases {
		got, err := Single(c.p, c.vals)
		if !errors.Is(err, c.err) || got != c.want {
			t.Errorf("%s: Single = %q, %v; want %q, %v", c.name, got, err, c.want, c.err)
		}
	}
}

func TestRegistry(t *testing.T) {
	reg := NewRegistry(Policy{Dup: DupFirst})
	reg.Register(Policy{List: true, Dup: DupMerge}, "Accept", "X-Multi")
	cases := []struct {
		name string
		list bool
		dup  Dup
	}{
		{"accept", true, DupMerge},  // 登记大小写不敏感
		{"ACCEPT", true, DupMerge},  //
		{"X-Multi", true, DupMerge}, //
		{"Other", false, DupFirst},  // 未登记走默认
	}
	for _, c := range cases {
		p := reg.For(c.name)
		if p.List != c.list || p.Dup != c.dup {
			t.Errorf("For(%q) = %+v, want list=%v dup=%v", c.name, p, c.list, c.dup)
		}
	}
}

func TestValidateValue(t *testing.T) {
	cases := []struct {
		name string
		p    Policy
		v    string
		bad  bool
	}{
		{"普通值", Policy{}, "ok", false},
		{"注入拒绝", Policy{}, "a\r\nB: 1", true},
		{"列表合法", Policy{List: true}, "a, b", false},
		{"列表空项拒绝", Policy{List: true}, "a,,b", true},
		{"列表未闭合引号拒绝", Policy{List: true}, `"a`, true},
	}
	for _, c := range cases {
		err := ValidateValue(c.p, c.v)
		if (err != nil) != c.bad {
			t.Errorf("%s: ValidateValue(%q) = %v, bad=%v", c.name, c.v, err, c.bad)
		}
	}
}
