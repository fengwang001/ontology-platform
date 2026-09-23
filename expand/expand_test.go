package expand

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"ontology/merge"
	"ontology/source"
)

func TestExpand(t *testing.T) {
	cases := []struct {
		name    string
		values  map[string]string
		key     string
		want    string
		wantErr error
		wantMsg string
	}{
		{"simple", map[string]string{"a": "x", "b": "${a}"}, "b", "x", nil, ""},
		{"two refs", map[string]string{"a": "1", "b": "2", "c": "${a}${b}"}, "c", "12", nil, ""},
		{"escaped", map[string]string{"a": "x", "v": "$${a}"}, "v", "${a}", nil, ""},
		{"unclosed", map[string]string{"v": "ab ${a"}, "v", "", ErrUnclosedRef, "byte 3"},
		{"undefined", map[string]string{"v": "${nope}"}, "v", "", ErrUndefinedRef, "nope"},
		{"self cycle", map[string]string{"a": "${a}"}, "a", "", ErrCycle, "a -> a"},
		{"two cycle", map[string]string{"a": "${b}", "b": "${a}"}, "a", "", ErrCycle, "a -> b -> a"},
		{"no refs", map[string]string{"a": "plain"}, "a", "plain", nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := New(tc.values).Expand(tc.key)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("want %v, got %v", tc.wantErr, err)
				}
				if !strings.Contains(err.Error(), tc.wantMsg) {
					t.Fatalf("error %q lacks %q", err, tc.wantMsg)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("got %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

// TestMergeThenExpand proves the required order: merge raw values first,
// then expand, so env's name=env wins in the file's greeting reference.
func TestMergeThenExpand(t *testing.T) {
	file, err := source.ParseFile(source.File, []byte("greeting = hello ${name}\nname = file\n.\n"))
	if err != nil {
		t.Fatal(err)
	}
	env, err := source.FromEnv(source.Env, []string{"NAME=env"}, "")
	if err != nil {
		t.Fatal(err)
	}
	res := merge.Merge(file, env)
	vals := map[string]string{}
	for k, m := range res.Entries {
		vals[k] = m.Value
	}
	got, err := New(vals).Expand("greeting")
	if err != nil || got != "hello env" {
		t.Fatalf("got %q, %v; want %q", got, err, "hello env")
	}
}

func TestExpandMemoBound(t *testing.T) {
	vals := map[string]string{"k0": "x"}
	totalRefs := 0
	for i := 1; i <= 20; i++ {
		vals[fmt.Sprintf("k%d", i)] = fmt.Sprintf("${k%d}${k%d}", i-1, i-1)
		totalRefs += 2
	}
	ex := New(vals)
	out, err := ex.ExpandAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(out["k20"]) != 1<<20 {
		t.Fatalf("k20 length = %d, want %d", len(out["k20"]), 1<<20)
	}
	bound := 4 * len(vals) * (totalRefs / len(vals))
	if bound < 4*totalRefs {
		bound = 4 * totalRefs // 4*keys*avgRefs, rounded up
	}
	if ex.Replacements() > bound {
		t.Fatalf("replacements %d exceed bound %d", ex.Replacements(), bound)
	}
	if ex.Replacements() != totalRefs {
		t.Fatalf("replacements = %d, want exactly %d (one per reference edge)",
			ex.Replacements(), totalRefs)
	}
}
