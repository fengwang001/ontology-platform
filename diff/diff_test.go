package diff

import "testing"

func TestClassify(t *testing.T) {
	cases := []struct {
		name    string
		primary Response
		mirror  Response
		ignore  map[string]bool
		want    Class
	}{
		{
			name:    "完全相同",
			primary: Response{200, map[string]string{"a": "1"}},
			mirror:  Response{200, map[string]string{"a": "1"}},
			want:    Same,
		},
		{
			name:    "状态码不等即破坏",
			primary: Response{200, map[string]string{"a": "1"}},
			mirror:  Response{500, map[string]string{"a": "1"}},
			want:    Breaking,
		},
		{
			name:    "值不同即破坏",
			primary: Response{200, map[string]string{"a": "1"}},
			mirror:  Response{200, map[string]string{"a": "2"}},
			want:    Breaking,
		},
		{
			name:    "主字段缺失即破坏",
			primary: Response{200, map[string]string{"a": "1", "b": "2"}},
			mirror:  Response{200, map[string]string{"a": "1"}},
			want:    Breaking,
		},
		{
			name:    "额外字段为兼容",
			primary: Response{200, map[string]string{"a": "1"}},
			mirror:  Response{200, map[string]string{"a": "1", "b": "2"}},
			want:    Compatible,
		},
		{
			name:    "忽略字段只影响比较-值不同仍相同",
			primary: Response{200, map[string]string{"a": "1", "date": "x"}},
			mirror:  Response{200, map[string]string{"a": "1", "date": "y"}},
			ignore:  map[string]bool{"date": true},
			want:    Same,
		},
		{
			name:    "忽略字段只影响比较-额外字段被忽略",
			primary: Response{200, map[string]string{"a": "1", "date": "x"}},
			mirror:  Response{200, map[string]string{"a": "1", "b": "2", "date": "y"}},
			ignore:  map[string]bool{"date": true},
			want:    Compatible,
		},
		{
			name:    "忽略字段缺失不计破坏",
			primary: Response{200, map[string]string{"a": "1", "date": "x"}},
			mirror:  Response{200, map[string]string{"a": "1"}},
			ignore:  map[string]bool{"date": true},
			want:    Same,
		},
		{
			name:    "规格示例-破坏",
			primary: Response{200, map[string]string{"a": "1", "date": "x"}},
			mirror:  Response{200, map[string]string{"a": "2"}},
			ignore:  map[string]bool{"date": true},
			want:    Breaking,
		},
		{
			name:    "空字段映射相同",
			primary: Response{204, nil},
			mirror:  Response{204, map[string]string{}},
			want:    Same,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Classify(tc.primary, tc.mirror, tc.ignore)
			t.Logf("Classify(primary=%+v, mirror=%+v, ignore=%v) = %s (依据: 状态码/缺失/值不同→破坏, 额外字段→兼容, 否则相同)",
				tc.primary, tc.mirror, tc.ignore, got)
			if got != tc.want {
				t.Fatalf("got %s, 期望 %s", got, tc.want)
			}
		})
	}
}

func TestClassString(t *testing.T) {
	if Same.String() != "same" || Compatible.String() != "compatible" || Breaking.String() != "breaking" {
		t.Fatalf("Class.String 异常: %s %s %s", Same, Compatible, Breaking)
	}
}
