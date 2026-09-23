package expand

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"ontology/merge"
	"ontology/source"
)

func TestExpandValues(t *testing.T) {
	cases := []struct {
		name    string
		values  map[string]string
		key     string
		want    string
		wantErr error
	}{
		{"no ref", map[string]string{"a": "x"}, "a", "x", nil},
		{"simple ref", map[string]string{"a": "x", "b": "${a}!"}, "b", "x!", nil},
		{"partial two refs", map[string]string{"a": "1", "b": "2", "c": "${a}${b}"}, "c", "12", nil},
		{"escaped ref", map[string]string{"a": "x", "b": "$${a}"}, "b", "${a}", nil},
		{"lone dollar", map[string]string{"a": "cost $5"}, "a", "cost $5", nil},
		{"empty value", map[string]string{"a": ""}, "a", "", nil},
		{"unclosed", map[string]string{"a": "x${b"}, "a", "", ErrUnclosedRef},
		{"unknown ref", map[string]string{"a": "${nope}"}, "a", "", ErrUnknownRef},
		{"self cycle", map[string]string{"a": "${a}"}, "a", "", ErrCycle},
		{"two key cycle", map[string]string{"a": "${b}", "b": "${a}"}, "a", "", ErrCycle},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var e Expander
			out, err := e.ExpandAll(c.values)
			if !errors.Is(err, c.wantErr) {
				t.Fatalf("err = %v, want %v", err, c.wantErr)
			}
			if c.wantErr == nil && out[c.key] != c.want {
				t.Fatalf("got %q, want %q", out[c.key], c.want)
			}
		})
	}
}

func TestCyclePathClosed(t *testing.T) {
	cases := []struct {
		name   string
		values map[string]string
		path   string
	}{
		{"self", map[string]string{"a": "${a}"}, "a -> a"},
		{"two keys", map[string]string{"a": "${b}", "b": "${a}"}, "a -> b -> a"},
		{"three keys", map[string]string{"a": "${b}", "b": "${c}", "c": "${a}"}, "a -> b -> c -> a"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var e Expander
			_, err := e.ExpandAll(c.values)
			if !errors.Is(err, ErrCycle) {
				t.Fatalf("err = %v, want ErrCycle", err)
			}
			if !strings.Contains(err.Error(), c.path) {
				t.Fatalf("err %v missing closed path %q", err, c.path)
			}
		})
	}
}

func TestUnclosedPosition(t *testing.T) {
	var e Expander
	_, err := e.ExpandAll(map[string]string{"a": "xy${b"})
	if !errors.Is(err, ErrUnclosedRef) {
		t.Fatalf("err = %v, want ErrUnclosedRef", err)
	}
	if !strings.Contains(err.Error(), `key "a" offset 2`) {
		t.Fatalf("err %v missing byte position 2", err)
	}
}

// TestMergeThenExpand 核心顺序：先合并后展开，greeting 必须用最终的 name。
func TestMergeThenExpand(t *testing.T) {
	file, err := source.ParseFile([]byte("greeting = hello ${name}\nname = file\n"))
	if err != nil {
		t.Fatal(err)
	}
	env, err := source.Env("", []string{"NAME=env"})
	if err != nil {
		t.Fatal(err)
	}
	var m merge.Merger
	records := m.Merge(file, env)
	var e Expander
	out, err := e.ExpandAll(merge.Values(records))
	if err != nil {
		t.Fatal(err)
	}
	if out["greeting"] != "hello env" {
		t.Fatalf("greeting = %q, want %q", out["greeting"], "hello env")
	}
}

// TestExpandCacheBound k0=x, k1=${k0}${k0}, …, k20：替换次数线性而非 2^20。
func TestExpandCacheBound(t *testing.T) {
	values := map[string]string{"k0": "x"}
	for i := 1; i <= 20; i++ {
		values[fmt.Sprintf("k%d", i)] = fmt.Sprintf("${k%d}${k%d}", i-1, i-1)
	}
	var e Expander
	out, err := e.ExpandAll(values)
	if err != nil {
		t.Fatal(err)
	}
	if len(out["k20"]) != 1<<20 {
		t.Fatalf("len(k20) = %d, want %d", len(out["k20"]), 1<<20)
	}
	totalRefs := 0
	for _, v := range values {
		totalRefs += strings.Count(v, "${")
	}
	avgRefs := float64(totalRefs) / float64(len(values))
	bound := int(4 * float64(len(values)) * avgRefs)
	if e.Replacements() > bound {
		t.Fatalf("replacements = %d, bound = %d", e.Replacements(), bound)
	}
	if e.Replacements() != totalRefs {
		t.Fatalf("replacements = %d, want exactly %d (one per ref occurrence)", e.Replacements(), totalRefs)
	}
}
