package ontology

import "testing"

func nameConstraint() Constraint {
	return Constraint{Name: "uniq_name", Columns: []string{"name"}}
}

// conflicts 用给定选项判断两个值在同一约束下是否冲突。
func conflicts(t *testing.T, opts NormOptions, a, b string) bool {
	t.Helper()
	c := NewChecker(opts, nameConstraint())
	if err := c.Insert("r1", map[string]Value{"name": String(a)}); err != nil {
		t.Fatalf("insert %q: %v", a, err)
	}
	return c.Insert("r2", map[string]Value{"name": String(b)}) != nil
}

// 两个规范化开关的四种组合：同一对输入 "  Alice" / "alice"。
func TestNormOptionCombinations(t *testing.T) {
	cases := []struct {
		name string
		opts NormOptions
		want bool
	}{
		{"none", NormOptions{}, false},
		{"trim_only", NormOptions{TrimSpace: true}, false},
		{"fold_only", NormOptions{CaseFold: true}, false},
		{"trim_and_fold", NormOptions{TrimSpace: true, CaseFold: true}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := conflicts(t, tc.opts, "  Alice", "alice")
			if got != tc.want {
				t.Fatalf("conflict=%v, want %v", got, tc.want)
			}
		})
	}
}

// 大小写折叠必须是 Unicode 折叠而非 ASCII 转换。
func TestCaseFoldNonASCII(t *testing.T) {
	opts := NormOptions{CaseFold: true}
	pairs := []struct {
		name string
		a, b string
	}{
		{"german_sharp_s", "STRASSE", "straße"},
		{"turkish_dotted_I", "İSTANBUL", "i̇stanbul"},
		{"greek_final_sigma", "ΟΔΟΣ", "οδος"},
	}
	for _, p := range pairs {
		t.Run(p.name, func(t *testing.T) {
			if !conflicts(t, opts, p.a, p.b) {
				t.Fatalf("%q and %q should conflict under case folding", p.a, p.b)
			}
		})
	}
	// 不开折叠时这些都不冲突。
	for _, p := range pairs {
		if conflicts(t, NormOptions{}, p.a, p.b) {
			t.Fatalf("%q and %q must not conflict without folding", p.a, p.b)
		}
	}
}

// 存储与读回的值必须逐字节保持原样。
func TestStoredValueUnchanged(t *testing.T) {
	c := NewChecker(NormOptions{TrimSpace: true, CaseFold: true}, nameConstraint())
	raw := "  Alice München  "
	if err := c.Insert("r1", map[string]Value{"name": String(raw)}); err != nil {
		t.Fatal(err)
	}
	got, ok := c.Get("r1")
	if !ok {
		t.Fatal("record not found")
	}
	if got["name"].Raw() != raw {
		t.Fatalf("stored value changed: got %q, want %q", got["name"].Raw(), raw)
	}
	// 规范化后的等价键仍应冲突，证明比较与存储是分离的。
	if err := c.Insert("r2", map[string]Value{"name": String("alice münchen")}); err == nil {
		t.Fatal("expected conflict with normalized-equal key")
	}
}
