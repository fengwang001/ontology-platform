package ontology

import "testing"

func newChecker(norm Normalizer) *Checker {
	return NewChecker(norm, Constraint{Name: "uniq_name", Props: []string{"name"}})
}

func mustAdd(t *testing.T, c *Checker, pk, name string) {
	t.Helper()
	if err := c.Add(Record{PK: pk, Values: map[string]*string{"name": Str(name)}}); err != nil {
		t.Fatalf("Add(%q, %q) unexpected error: %v", pk, name, err)
	}
}

func addErr(c *Checker, pk, name string) error {
	return c.Add(Record{PK: pk, Values: map[string]*string{"name": Str(name)}})
}

// 两个开关的四种组合：同一对输入 "  Alice" / "alice"。
func TestNormalizeSwitchCombinations(t *testing.T) {
	cases := []struct {
		name     string
		norm     Normalizer
		conflict bool
	}{
		{"both_off", Normalizer{TrimSpace: false, CaseFold: false}, false},
		{"trim_only", Normalizer{TrimSpace: true, CaseFold: false}, false},
		{"fold_only", Normalizer{TrimSpace: false, CaseFold: true}, false},
		{"both_on", Normalizer{TrimSpace: true, CaseFold: true}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newChecker(tc.norm)
			mustAdd(t, c, "pk1", "  Alice")
			err := addErr(c, "pk2", "alice")
			if tc.conflict && err == nil {
				t.Fatalf("expected conflict, got nil")
			}
			if !tc.conflict && err != nil {
				t.Fatalf("expected no conflict, got %v", err)
			}
		})
	}
}

// 单独验证每个开关各自生效。
func TestNormalizeEachSwitchAlone(t *testing.T) {
	trimOnly := newChecker(Normalizer{TrimSpace: true})
	mustAdd(t, trimOnly, "pk1", "Alice ")
	if err := addErr(trimOnly, "pk2", "  Alice"); err == nil {
		t.Fatal("trim_only: expected conflict for trailing/leading spaces")
	}
	if err := addErr(trimOnly, "pk3", "ALICE"); err != nil {
		t.Fatalf("trim_only: case difference must not conflict, got %v", err)
	}

	foldOnly := newChecker(Normalizer{CaseFold: true})
	mustAdd(t, foldOnly, "pk1", "ALICE")
	if err := addErr(foldOnly, "pk2", "alice"); err == nil {
		t.Fatal("fold_only: expected conflict for case difference")
	}
	if err := addErr(foldOnly, "pk3", " alice"); err != nil {
		t.Fatalf("fold_only: leading space must not be trimmed, got %v", err)
	}
}

// 非 ASCII 大小写折叠：证明不是简单的 ASCII 转换。
func TestCaseFoldNonASCII(t *testing.T) {
	c := newChecker(Normalizer{CaseFold: true})

	// 德语 ß 全折叠为 ss。
	mustAdd(t, c, "pk1", "Straße")
	if err := addErr(c, "pk2", "STRASSE"); err == nil {
		t.Fatal("ß must fold to ss: Straße vs STRASSE should conflict")
	}

	// 希腊语词尾小写 σ/ς 折叠一致。
	mustAdd(t, c, "pk3", "ΟΔΟΣ")
	if err := addErr(c, "pk4", "οδος"); err == nil {
		t.Fatal("final sigma ς must fold like σ: ΟΔΟΣ vs οδος should conflict")
	}

	// 土耳其语 İ 折叠为 i + 组合上点，与普通的 i 不同。
	mustAdd(t, c, "pk5", "İ")
	if err := addErr(c, "pk6", "i"); err != nil {
		t.Fatalf("İ must not fold to plain i, got %v", err)
	}
	if err := addErr(c, "pk7", "i̇"); err == nil {
		t.Fatal("İ must fold to i + U+0307")
	}
}

// 规范化只用于比较：读回的值必须与写入值逐字节相同。
func TestStoredValueUnchanged(t *testing.T) {
	c := newChecker(Normalizer{TrimSpace: true, CaseFold: true})
	raw := "  Àlìçé ß  "
	mustAdd(t, c, "pk1", raw)
	rec, ok := c.Get("pk1")
	if !ok {
		t.Fatal("record not found")
	}
	got := rec.Values["name"]
	if got == nil || *got != raw {
		t.Fatalf("stored value changed: got %v, want %q", got, raw)
	}
}
