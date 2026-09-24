package mask_test

import (
	"bytes"
	"testing"

	"ontology/mask"
	"ontology/record"
	"ontology/rule"
)

func mustSet(t *testing.T, rs []rule.Rule) *rule.Set {
	t.Helper()
	s, err := rule.Compile(rs)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	return s
}

func TestTransformMethods(t *testing.T) {
	cases := []struct {
		name    string
		r       rule.Rule
		in      string
		predict func(t *testing.T, out, in string)
		det     bool
	}{
		{
			name: "replace fixed string length not kept",
			r:    rule.Rule{Method: rule.Replace, Replace: "***"},
			in:   "supersecretvalue",
			predict: func(t *testing.T, out, in string) {
				if out != "***" || len(out) == len(in) {
					t.Fatalf("replace mismatch: %q", out)
				}
			},
		},
		{
			name: "hash stable distinct irreversible",
			r:    rule.Rule{Method: rule.Hash},
			in:   "password1",
			predict: func(t *testing.T, out, in string) {
				again := mask.Transform(in, &rule.Rule{Method: rule.Hash})
				if out != again || out == in || bytes.Contains([]byte(out), []byte(in)) {
					t.Fatalf("hash invariants violated: %q", out)
				}
				other := mask.Transform("password2", &rule.Rule{Method: rule.Hash})
				if other == out {
					t.Fatalf("distinct inputs must hash distinctly")
				}
			},
		},
		{
			name: "truncate keeps prefix and marks",
			r:    rule.Rule{Method: rule.Truncate, Keep: 3},
			in:   "abcdefg",
			predict: func(t *testing.T, out, in string) {
				if out != "abc...<truncated>" || out == in {
					t.Fatalf("truncate mismatch: %q", out)
				}
			},
		},
		{
			name: "empty string value still masked",
			r:    rule.Rule{Method: rule.Replace, Replace: "NONE"},
			in:   "",
			predict: func(t *testing.T, out, in string) {
				if out != "NONE" {
					t.Fatalf("empty value not masked: %q", out)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := mask.Transform(tc.in, &tc.r)
			tc.predict(t, out, tc.in)
		})
	}
}

func TestApplyCoverage(t *testing.T) {
	// 同一敏感值出现在三个不同位置：两个路径 + 数组内嵌套映射；另加值引用。
	fields := map[string]any{
		"pw":    "SECRET",
		"other": map[string]any{"copy": "SECRET"},
		"arr":   []any{map[string]any{"ref": "SECRET"}},
		"keep":  "visible",
		"n":     42,
	}
	orig, err := record.New(record.Info, "t", fields)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	origBytes, _ := record.Encode(orig)

	r2, _ := record.New(record.Info, "t", fields)
	set := mustSet(t, []rule.Rule{{Name: "v", Value: "SECRET",
		Method: rule.Replace, Replace: "REDACTED"}})
	if !mask.Apply(r2, set) {
		t.Fatalf("Apply should report a change")
	}
	outBytes, _ := record.Encode(r2)
	if bytes.Count(outBytes, []byte("REDACTED")) != 3 {
		t.Fatalf("sensitive value must be masked at all 3 sites, body=%s", outBytes)
	}
	if bytes.Contains(outBytes, []byte("SECRET")) {
		t.Fatalf("raw sensitive value leaked: %s", outBytes)
	}
	if !bytes.Contains(outBytes, []byte(`"keep":"visible"`)) ||
		!bytes.Contains(outBytes, []byte(`"n":42`)) {
		t.Fatalf("non-target fields changed: %s", outBytes)
	}

	// 路径规则：仅目标路径改变，其余字节与原始版本逐字节一致（键重排由 json 归一）。
	r3, _ := record.New(record.Info, "t", fields)
	setPath := mustSet(t, []rule.Rule{{Name: "p", Path: "pw",
		Method: rule.Replace, Replace: "X"}})
	mask.Apply(r3, setPath)
	b3, _ := record.Encode(r3)
	base, _ := record.New(record.Info, "t",
		map[string]any{"pw": "X", "other": map[string]any{"copy": "SECRET"},
			"arr":  []any{map[string]any{"ref": "SECRET"}},
			"keep": "visible", "n": 42})
	wantBytes, _ := record.Encode(base)
	if !bytes.Equal(b3, wantBytes) {
		t.Fatalf("non-target bytes differ:\n%s\n%s\norig=%s", b3, wantBytes, origBytes)
	}

	// 源记录不被修改。
	if orig.Fields["pw"] != "SECRET" {
		t.Fatalf("Apply mutated the source record")
	}
}
