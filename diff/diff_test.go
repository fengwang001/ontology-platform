package diff_test

import "testing"
import "ontology/diff"

func TestClassify(t *testing.T) {
	ig := map[string]struct{}{"date": {}}
	cases := []struct {
		name string
		p, s diff.Response
		want diff.Category
	}{
		{"identical", diff.Response{200, map[string]string{"a": "1"}},
			diff.Response{200, map[string]string{"a": "1"}}, diff.Identical},
		{"ignore-only-diff", diff.Response{200, map[string]string{"a": "1", "date": "x"}},
			diff.Response{200, map[string]string{"a": "1", "date": "y"}}, diff.Identical},
		{"extra-shadow-field", diff.Response{200, map[string]string{"a": "1"}},
			diff.Response{200, map[string]string{"a": "1", "b": "2"}}, diff.Compatible},
		{"missing-shadow-field", diff.Response{200, map[string]string{"a": "1"}},
			diff.Response{200, nil}, diff.Breaking},
		{"value-mismatch", diff.Response{200, map[string]string{"a": "1"}},
			diff.Response{200, map[string]string{"a": "2"}}, diff.Breaking},
		{"status-mismatch", diff.Response{200, nil}, diff.Response{500, nil}, diff.Breaking},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := diff.Classify(tc.p, tc.s, ig); got != tc.want {
				t.Fatalf("Classify=%s want %s", got, tc.want)
			}
		})
	}
	// 不修改入参：忽略字段后两侧原始映射仍保留 date。
	p := diff.Response{200, map[string]string{"date": "x"}}
	s := diff.Response{200, map[string]string{"date": "y"}}
	if diff.Classify(p, s, ig) != diff.Identical {
		t.Fatal("date 应被忽略")
	}
	if p.Fields["date"] != "x" || s.Fields["date"] != "y" {
		t.Fatal("Classify 不得修改入参映射")
	}
}
