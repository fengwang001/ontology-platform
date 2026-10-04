package registry

import (
	"errors"
	"reflect"
	"testing"
)

func TestAdd(t *testing.T) {
	cases := []struct {
		name    string
		adds    []addOp
		wantErr error
	}{
		{"empty name", []addOp{{"", nil}}, ErrEmptyName},
		{"missing parent", []addOp{{"b", []string{"a"}}}, ErrMissingParent},
		{"too many parents", buildChainAdds(10), ErrTooManyParents},
		{"duplicate", []addOp{{"a", nil}, {"a", nil}}, ErrExists},
		{"ok chain", buildChainAdds(3), nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := New()
			var err error
			for _, a := range tc.adds {
				if err = r.Add(a.name, a.parents); err != nil {
					break
				}
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

type addOp struct {
	name    string
	parents []string
}

func buildChainAdds(n int) []addOp {
	ops := []addOp{{"d0", nil}}
	for i := 1; i < n; i++ {
		parents := []string{}
		for j := 0; j < i && j < 9; j++ {
			parents = append(parents, "d"+itoa(j))
		}
		ops = append(ops, addOp{"d" + itoa(i), parents})
	}
	return ops
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

func TestAffectedAndChildren(t *testing.T) {
	// 图：a -> b, a -> c; b -> d; c -> d; 独立 e。
	r := New()
	mustAdd(t, r, "a", nil)
	mustAdd(t, r, "b", []string{"a"})
	mustAdd(t, r, "c", []string{"a"})
	mustAdd(t, r, "d", []string{"b", "c"})
	mustAdd(t, r, "e", nil)

	if got := r.AffectedDownstream("a"); !reflect.DeepEqual(got, []string{"b", "c", "d"}) {
		t.Fatalf("affected(a) = %v", got)
	}
	if got := r.UnretiredChildren("a"); !reflect.DeepEqual(got, []string{"b", "c"}) {
		t.Fatalf("children(a) = %v", got)
	}
	if got := r.AffectedDownstream("e"); len(got) != 0 {
		t.Fatalf("affected(e) = %v", got)
	}

	// d Retired 后从 a 的未 Retired 影响清单与 b/c 的直接下游清单中消失。
	d, _ := r.Get("d")
	d.Phase = Retired
	if got := r.AffectedDownstream("a"); !reflect.DeepEqual(got, []string{"b", "c"}) {
		t.Fatalf("affected after retired = %v", got)
	}
	if got := r.UnretiredChildren("b"); len(got) != 0 {
		t.Fatalf("children(b) = %v", got)
	}
}

func TestByteOrderSorting(t *testing.T) {
	// 字节序而非自然序：Zoo < apple（大写 Z=0x5a < 小写 a=0x61）。
	r := New()
	mustAdd(t, r, "root", nil)
	mustAdd(t, r, "apple", []string{"root"})
	mustAdd(t, r, "Zoo", []string{"root"})
	if got := r.UnretiredChildren("root"); !reflect.DeepEqual(got, []string{"Zoo", "apple"}) {
		t.Fatalf("byte-order children = %v", got)
	}
}

func mustAdd(t *testing.T, r *Registry, name string, parents []string) {
	t.Helper()
	if err := r.Add(name, parents); err != nil {
		t.Fatalf("add %s: %v", name, err)
	}
}
