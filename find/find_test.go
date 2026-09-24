package find_test

import (
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"unsafe"

	"ontology/find"
	"ontology/scan"
	"ontology/table"
)

func naiveFind(text, pattern string) []int {
	got := []int{}
	for i := 0; i+len(pattern) <= len(text); i++ {
		if text[i:i+len(pattern)] == pattern {
			got = append(got, i)
		}
	}
	return got
}

func counter(s *scan.Scanner, name string) int {
	f := reflect.ValueOf(s).Elem().FieldByName(name)
	return int(reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem().Int())
}
func TestFindAllTable(t *testing.T) {
	for _, tc := range []struct{ text, pattern string }{
		{"abababaca", "ababaca"}, {"aaaa", "aa"}, {"mississippi", "issi"}, {"abc", "abc"}, {"x", "y"},
	}
	for _, tc := range cases {
		m, _ := find.Compile(tc.pattern, len(tc.pattern))
		got, err := m.FindAll(tc.text)
		want := naiveFind(tc.text, tc.pattern)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("%q in %q: got %v, %v; want %v", tc.pattern, tc.text, got, err, want)
		}
	}
}
func TestOverlap(t *testing.T) {
	for _, tc := range []struct {
		text, pattern string
		want          []int
	}{{"aaaa", "aa", []int{0, 1, 2}}} {
		m, _ := find.Compile(tc.pattern, len(tc.pattern))
		if got, _ := m.FindAll(tc.text); !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("got %v want %v", got, tc.want)
		}
	}
}
func TestCountersLinear(t *testing.T) {
	for _, tc := range []struct{ n, m int }{{1000, 10}, {100000, 100}} {
		tab, _ := table.New(strings.Repeat("a", tc.m-1) + "b")
		s := scan.New(tab)
		s.Scan(strings.Repeat("a", tc.n))
		if got := counter(&s, "textAdvances"); got != tc.n {
			t.Fatalf("advances=%d want %d", got, tc.n)
		}
		if got := counter(&s, "comparisons"); got > 2*tc.n {
			t.Fatalf("comparisons=%d > %d", got, 2*tc.n)
		}
	}
}
func TestSelfCheck(t *testing.T) {
	for _, tc := range []struct{ text, pattern string }{
		{"abababaca", "ababaca"}, {"aaaa", "aa"}, {"abc", "abc"},
	} {
		m, _ := find.Compile(tc.pattern, len(tc.pattern))
		if err := m.SelfCheck(tc.text); err != nil {
			t.Fatalf("SelfCheck(%q,%q): %v", tc.text, tc.pattern, err)
		}
	}
}
func TestRejectionLeavesNoTrace(t *testing.T) {
	m, _ := find.Compile("aa", 2)
	before, _ := m.FindAll("aaaa")
	cases := []struct {
		name string
		err  error
		run  func() error
	}{
		{"empty", find.ErrEmptyPattern, func() error { _, e := find.Compile("", 2); return e }},
		{"limit", find.ErrPatternTooLong, func() error { _, e := find.Compile("abc", 2); return e }},
		{"length", find.ErrTextShorterThanText, func() error { _, e := m.FindAll("a"); return e }},
	} {
		if got := tc.run(); !errors.Is(got, tc.err) {
			t.Fatalf("%s: got %v want %v", tc.name, got, tc.err)
		}
	}
	after, _ := m.FindAll("aaaa")
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("before=%v after=%v", before, after)
	}
}
func TestConcurrentFindAll(t *testing.T) {
	m, _ := find.Compile("aa", 2)
	const workers = 32
	var wg sync.WaitGroup
	results := make([][]int, workers)
	for i := range results {
		wg.Add(1)
		go func(i int) { defer wg.Done(); results[i], _ = m.FindAll("aaaaaaaaaa") }(i)
	}
	wg.Wait()
	for i := 1; i < workers; i++ {
		if !reflect.DeepEqual(results[0], results[i]) {
			t.Fatalf("worker %d got %v want %v", i, results[i], results[0])
		}
	}
}
