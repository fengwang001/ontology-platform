package fsmapper

import (
	"errors"
	"sort"
	"testing"
)

func mappedOf(m *Mapper, id int64) string {
	p, err := m.Path(id)
	if err != nil {
		return ""
	}
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			return p[i+1:]
		}
	}
	return p
}

func mustMap(t *testing.T, m *Mapper, parent int64, src string, isDir bool, want string) int64 {
	t.Helper()
	id, err := m.Add(parent, src, isDir)
	if err != nil {
		t.Fatalf("Add(%q) unexpected error: %v", src, err)
	}
	if got := mappedOf(m, id); got != want {
		t.Fatalf("Add(%q) mapped = %q, want %q", src, got, want)
	}
	return id
}

func expectErr(t *testing.T, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("error = %v, want %v", got, want)
	}
}

// 三个基础映射例子（加 Aux.txt）。
func TestBaseMapExamples(t *testing.T) {
	cases := []struct{ src, want string }{
		{"a:b?.", "a%3Ab%3F%2E"},
		{"CON", "%43ON"},
		{"NUL.", "NUL%2E"},
		{"Aux.txt", "%41ux.txt"},
	}
	for _, c := range cases {
		if got := baseMap(c.src, 255); got != c.want {
			t.Errorf("baseMap(%q) = %q, want %q", c.src, got, c.want)
		}
	}
	m, err := New(255, 4096, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		mustMap(t, m, 0, c.src, false, c.want)
	}
}

// 转义的单射性。
func TestEscapeInjectivity(t *testing.T) {
	pairs := [][2]string{
		{":", "%3A"}, {"a:b", "a%3Ab"}, {"%", "%25"},
		{".", "%2E"}, {" ", "%20"}, {"A", "a"},
	}
	for _, p := range pairs {
		if escapeStep(p[0]) == escapeStep(p[1]) {
			t.Errorf("escape collision for %q / %q", p[0], p[1])
		}
	}
	// 穷举小规模合法输入集合，检查基础映射（含保留名处理外）的转义层无碰撞。
	alphabet := []string{"a", "A", "%", ":", "%3A", "%3a", ".", " ", "中"}
	seen := map[string]string{}
	for _, a := range alphabet {
		for _, b := range alphabet {
			n := a + b
			got := escapeStep(n)
			if prev, dup := seen[got]; dup && prev != n {
				t.Fatalf("escape not injective: %q and %q -> %q", prev, n, got)
			}
			seen[got] = n
		}
	}
	// 非法名判定。
	for _, bad := range []string{"", "a/b", "a\x00b", "bad\xffname"} {
		if validSrcName(bad) {
			t.Errorf("validSrcName(%q) = true, want false", bad)
		}
	}
}

// 保留名：带扩展名与带前导点主干。
func TestReservedStems(t *testing.T) {
	table := []struct{ src, want string }{
		{"con", "%63on"},
		{"Con.log", "%43on.log"},
		{"LPT9", "%4CPT9"},
		{"com1.txt", "%63om1.txt"},
		{".con", ".con"},
		{"con.aux", "%63on.aux"},
		{"NUL.", "NUL%2E"},
		{"auxiliary", "auxiliary"},
		{"CON1", "CON1"},
		{"COM0", "COM0"},
		{".", "%2E"},
		{" ", "%20"},
		{"..%", "..%25"},
	}
	for _, c := range table {
		if got := baseMap(c.src, 255); got != c.want {
			t.Errorf("baseMap(%q) = %q, want %q", c.src, got, c.want)
		}
	}
}

func sortedStrings(s []string) []string {
	out := append([]string(nil), s...)
	sort.Strings(out)
	return out
}
