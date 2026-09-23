package validate

import (
	"errors"
	"strings"
	"testing"

	"ontology/source"
)

func TestCheckTypes(t *testing.T) {
	cases := []struct {
		name   string
		rule   Rule
		values map[string]string
		wantOK bool
	}{
		{"int ok", Rule{Key: "n", Type: Int}, map[string]string{"n": "42"}, true},
		{"int bad", Rule{Key: "n", Type: Int}, map[string]string{"n": "abc"}, false},
		{"float ok", Rule{Key: "f", Type: Float}, map[string]string{"f": "1.5"}, true},
		{"float bad", Rule{Key: "f", Type: Float}, map[string]string{"f": "x"}, false},
		{"bool ok", Rule{Key: "b", Type: Bool}, map[string]string{"b": "true"}, true},
		{"bool bad", Rule{Key: "b", Type: Bool}, map[string]string{"b": "yes"}, false},
		{"string any", Rule{Key: "s", Type: String}, map[string]string{"s": "abc"}, true},
		{"optional missing", Rule{Key: "o", Type: Int}, map[string]string{}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := Check([]Rule{c.rule}, c.values, map[string]source.Layer{})
			if got := err == nil; got != c.wantOK {
				t.Fatalf("err = %v, wantOK %v", err, c.wantOK)
			}
			if !c.wantOK && !errors.Is(err, ErrTypeMismatch) {
				t.Fatalf("err = %v, want ErrTypeMismatch", err)
			}
		})
	}
}

// TestTypeErrorDetail 类型错误必须含键名、期望类型、实际值、来源层四要素。
func TestTypeErrorDetail(t *testing.T) {
	err := Check(
		[]Rule{{Key: "port", Type: Int}},
		map[string]string{"port": "abc"},
		map[string]source.Layer{"port": source.LayerEnv},
	)
	if !errors.Is(err, ErrTypeMismatch) {
		t.Fatalf("err = %v, want ErrTypeMismatch", err)
	}
	for _, part := range []string{`"port"`, "int", `"abc"`, "env"} {
		if !strings.Contains(err.Error(), part) {
			t.Fatalf("err %v missing element %q", err, part)
		}
	}
}

// TestMissingRequiredAll 必填缺失必须一次报出全部，而不是只报第一个。
func TestMissingRequiredAll(t *testing.T) {
	err := Check(
		[]Rule{
			{Key: "b.req", Required: true},
			{Key: "a.req", Required: true},
			{Key: "present", Required: true},
		},
		map[string]string{"present": "x"},
		map[string]source.Layer{"present": source.LayerDefault},
	)
	if !errors.Is(err, ErrMissingRequired) {
		t.Fatalf("err = %v, want ErrMissingRequired", err)
	}
	if !strings.Contains(err.Error(), "a.req") || !strings.Contains(err.Error(), "b.req") {
		t.Fatalf("err %v must list all missing keys", err)
	}
	if strings.Contains(err.Error(), "present") {
		t.Fatalf("err %v must not list present keys", err)
	}
}
