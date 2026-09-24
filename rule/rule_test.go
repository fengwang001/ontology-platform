package rule

import (
	"errors"
	"fmt"
	"testing"
)

// TestParsePath 一张表覆盖嵌套、字面点、反斜杠消歧。
func TestParsePath(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"a.b.c", []string{"a", "b", "c"}},
		{`a\.b`, []string{"a.b"}},
		{`a\.b.c`, []string{"a.b", "c"}},
		{`a\\b`, []string{`a\b`}},
		{"", []string{}},
		{"plain", []string{"plain"}},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got := ParsePath(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("got %v want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("got %v want %v", got, tc.want)
				}
			}
		})
	}
}

// TestCompileConflicts 一张表覆盖冲突检出与同动作允许、坏模式。
func TestCompileConflicts(t *testing.T) {
	cases := []struct {
		name  string
		specs []Spec
		want  error
	}{
		{"replace vs hash", []Spec{{Path: "a.b", Action: Replace}, {Path: "a.b", Action: Hash}}, ErrConflict},
		{"hash vs replace", []Spec{{Path: "a.b", Action: Hash}, {Path: "a.b", Action: Replace}}, ErrConflict},
		{"same action ok", []Spec{{Path: "a.b", Action: Replace}, {Path: "a.b", Action: Replace}}, nil},
		{"truncate vs replace ok", []Spec{{Path: "a.b", Action: Truncate}, {Path: "a.b", Action: Replace}}, nil},
		{"bad regexp", []Spec{{Pattern: "(", Action: Replace}}, ErrPattern},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Compile(tc.specs)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v want %v", err, tc.want)
			}
		})
	}
}

// TestWalkSemantics 一张表覆盖命中、缺失、中途非映射、字面点、数组不进入。
func TestWalkSemantics(t *testing.T) {
	set, err := Compile([]Spec{
		{Path: "a.b.c", Action: Replace},
		{Path: `lit\.key`, Action: Hash},
	})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
	name   string
		fields map[string]any
		hits   int
	}{
		{"nested hit", map[string]any{"a": map[string]any{"b": map[string]any{"c": "x"}}}, 1},
		{"missing", map[string]any{"a": map[string]any{"b": map[string]any{"d": "x"}}}, 0},
		{"mid non-map", map[string]any{"a": map[string]any{"b": "scalar"}}, 0},
		{"literal dot key", map[string]any{"lit.key": "v"}, 1},
		{"nested not literal", map[string]any{"lit": map[string]any{"key": "v"}}, 0},
		{"array skipped for path", map[string]any{"a": []any{map[string]any{"b": map[string]any{"c": 1}}}}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := len(set.Walk(tc.fields)); got != tc.hits {
				t.Fatalf("hits got %d want %d", got, tc.hits)
			}
		})
	}
}

// TestLookupBound 100 规则、10 万记录：计数上界 = 记录数*(字段数+常数)，与规则数无关。
func TestLookupBound(t *testing.T) {
	const rulesN, recsN, fieldN = 100, 100000, 10
	specs := make([]Spec, rulesN)
	for i := range specs {
		specs[i] = Spec{Path: fmt.Sprintf("k%d", i), Action: Replace}
	}
	set, err := Compile(specs)
	if err != nil {
		t.Fatal(err)
	}
	fields := map[string]any{}
	for i := 0; i < fieldN; i++ {
		fields[fmt.Sprintf("f%d", i)] = i
	}
	set.ResetLookups()
	for i := 0; i < recsN; i++ {
		set.Walk(fields)
	}
	got := set.LookupLookups()
	bound := int64(recsN * (fieldN + 4))
	if got > bound {
		t.Fatalf("lookups %d exceed bound %d (= recs*(fields+const)); rules=%d", got, bound, rulesN)
	}
	if got != int64(recsN*fieldN) {
		t.Fatalf("lookups got %d want %d (exactly fields per record)", got, recsN*fieldN)
	}
}
