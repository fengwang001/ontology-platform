package validate

import (
	"errors"
	"testing"

	"ontology/merge"
	"ontology/source"
)

func mergedWith(kvs ...string) *merge.Result {
	var entries []source.Entry
	for i := 0; i+1 < len(kvs); i += 2 {
		entries = append(entries, source.Entry{
			Key: kvs[i], Value: kvs[i+1], Layer: source.File,
		})
	}
	return merge.Merge(entries)
}

func TestCheck(t *testing.T) {
	cases := []struct {
		name    string
		schema  map[string]Rule
		res     *merge.Result
		wantErr error
	}{
		{"int ok", map[string]Rule{"p": {Type: "int"}}, mergedWith("p", "8080"), nil},
		{"int bad", map[string]Rule{"p": {Type: "int"}}, mergedWith("p", "abc"), ErrTypeMismatch},
		{"bool ok", map[string]Rule{"f": {Type: "bool"}}, mergedWith("f", "true"), nil},
		{"bool bad", map[string]Rule{"f": {Type: "bool"}}, mergedWith("f", "maybe"), ErrTypeMismatch},
		{"string any", map[string]Rule{"s": {Type: "string"}}, mergedWith("s", "abc"), nil},
		{"required hit", map[string]Rule{"a": {Required: true}, "b": {Required: true}},
			mergedWith("a", "1"), ErrRequired},
		{"required ok", map[string]Rule{"a": {Required: true}}, mergedWith("a", ""), nil},
		{"optional absent", map[string]Rule{"a": {Type: "int"}}, mergedWith(), nil},
		{"empty config", map[string]Rule{}, mergedWith(), nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Check(tc.schema, tc.res)
			if tc.wantErr == nil && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("want %v, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestTypeErrorFields(t *testing.T) {
	res := merge.Merge([]source.Entry{{Key: "port", Value: "abc", Layer: source.Env}})
	err := Check(map[string]Rule{"port": {Type: "int"}}, res)
	var te *TypeError
	if !errors.As(err, &te) {
		t.Fatalf("no TypeError in %v", err)
	}
	if te.Key != "port" || te.Want != "int" || te.Got != "abc" || te.Layer != source.Env {
		t.Fatalf("TypeError %+v lacks key/want/got/layer", te)
	}
}

func TestRequiredListsAll(t *testing.T) {
	err := Check(map[string]Rule{
		"m1": {Required: true},
		"m2": {Required: true},
		"ok": {Required: true},
	}, mergedWith("ok", "1"))
	var re *RequiredError
	if !errors.As(err, &re) {
		t.Fatalf("no RequiredError in %v", err)
	}
	if len(re.Keys) != 2 || re.Keys[0] != "m1" || re.Keys[1] != "m2" {
		t.Fatalf("missing keys %v, want [m1 m2]", re.Keys)
	}
}

func TestJoinedFailures(t *testing.T) {
	err := Check(map[string]Rule{
		"p": {Type: "int"},
		"r": {Required: true},
	}, mergedWith("p", "abc"))
	if !errors.Is(err, ErrTypeMismatch) || !errors.Is(err, ErrRequired) {
		t.Fatalf("joined error %v must match both sentinels", err)
	}
}
