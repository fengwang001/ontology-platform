package omap

import (
	"errors"
	"fmt"
	"testing"

	"ontology/lww"
)

func put(o, i string, v, ts int64, r string) Op {
	return Op{Kind: "put", O: o, I: i, V: v, TS: ts, Rep: r}
}

// TestApplyDispatch 表驱动：put/del 正常分派，未知 Kind 与 lww 校验错误上抛。
func TestApplyDispatch(t *testing.T) {
	cases := []struct {
		name string
		op   Op
		want error
	}{
		{"put ok", put("o", "i", 1, 1, "R"), nil},
		{"del ok", Op{Kind: "del", O: "o", TS: 1}, nil},
		{"unknown kind", Op{Kind: "frob", O: "o"}, ErrUnknownKind},
		{"empty outer", put("", "i", 1, 1, "R"), lww.ErrEmptyOuter},
		{"empty inner", put("o", "", 1, 1, "R"), lww.ErrEmptyInner},
		{"bad ts", put("o", "i", 1, 0, "R"), lww.ErrNonPositiveTS},
		{"del bad ts", Op{Kind: "del", O: "o", TS: -2}, lww.ErrNonPositiveTS},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			x := New()
			err := x.Apply(c.op)
			if !errors.Is(err, c.want) {
				t.Fatalf("Apply = %v, want %v", err, c.want)
			}
		})
	}
}

// TestOuterLookupConstant 钉住复杂度约束：m 个外层键之后对已知键 Put，
// lastChecked 恒为 1（哈希定位，与 m 无关）；被整体拒绝时记 0；
// Merge 不是 Apply，不动计数器。
func TestOuterLookupConstant(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		x := New()
		for j := 0; j < m; j++ {
			if err := x.Apply(put(fmt.Sprintf("o%d", j), "i", int64(j), 1, "R")); err != nil {
				t.Fatalf("seed m=%d j=%d: %v", m, j, err)
			}
		}
		if err := x.Apply(put("known", "i", 9, 1, "R")); err != nil {
			t.Fatalf("target put: %v", err)
		}
		const budget = 2 // 与 m 无关的小常数
		if x.lastChecked > budget || x.lastChecked != 1 {
			t.Fatalf("m=%d: checked %d outer keys, want 1 (no linear scan)", m, x.lastChecked)
		}
	}
	x := New()
	if err := x.Apply(put("", "i", 1, 1, "R")); !errors.Is(err, lww.ErrEmptyOuter) {
		t.Fatalf("reject err = %v", err)
	}
	if x.lastChecked != 0 {
		t.Fatalf("rejected Apply checked %d keys, want 0", x.lastChecked)
	}
	x.Merge(New())
	if x.lastChecked != 0 {
		t.Fatalf("Merge changed counter to %d, want unchanged 0", x.lastChecked)
	}
}
