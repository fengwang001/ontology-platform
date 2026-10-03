package route

import (
	"fmt"
	"strings"
	"testing"
)

func TestNormalizeAndLookup(t *testing.T) {
	normalizeCases := []struct {
		name string
		path string
		want string
		ok   bool
	}{
		{"root", "/", "/", true},
		{"drop empty", "/a//b/", "/a/b", true},
		{"drop dot", "/a/./b", "/a/b", true},
		{"pop segment", "/a/b/../c", "/a/c", true},
		{"pop to root", "/a/..", "/", true},
		{"escape root", "/../a", "", false},
		{"missing leading slash", "a", "", false},
		{"empty", "", "", false},
		{"percent rejected", "/a%2fb", "", false},
		{"query rejected", "/a?b", "", false},
		{"fragment rejected", "/a#b", "", false},
		{"space allowed", "/a b", "/a b", true},
		{"tab rejected", "/a\tb", "", false},
		{"del rejected", "/a\x7fb", "", false},
	}
	for _, tc := range normalizeCases {
		t.Run("normalize/"+tc.name, func(t *testing.T) {
			got, err := Normalize(tc.path)
			if tc.ok != (err == nil) || got != tc.want {
				t.Fatalf("Normalize(%q) = (%q,%v), want (%q,ok=%v)", tc.path, got, err, tc.want, tc.ok)
			}
		})
	}

	table, err := NewTable([]Route{
		{Prefix: "/", Scope: "root", Backend: "root"},
		{Prefix: "/pub", Scope: "", Backend: "pub"},
		{Prefix: "/api", Scope: "api", Backend: "api"},
		{Prefix: "/api/v1", Scope: "v1", Backend: "v1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	lookupCases := []struct {
		path    string
		prefix  string
		rest    string
		backend string
	}{
		{"/", "/", "", "root"},
		{"/pub", "/pub", "", "pub"},
		{"/pub/x", "/pub", "/x", "pub"},
		{"/pubx", "/", "/pubx", "root"},
		{"/api/v1/x", "/api/v1", "/x", "v1"},
		{"/api/v2", "/api", "/v2", "api"},
	}
	for _, tc := range lookupCases {
		t.Run("lookup/"+tc.path, func(t *testing.T) {
			match, examined, ok := table.lookupWithExamined(tc.path)
			if !ok || match.Route.Prefix != tc.prefix || match.Rest != tc.rest || match.Route.Backend != tc.backend {
				t.Fatalf("Lookup(%q) = %+v ok=%v, want prefix=%q rest=%q backend=%q", tc.path, match, ok, tc.prefix, tc.rest, tc.backend)
			}
			segments := len(splitSegments(tc.path))
			if examined > segments+1 {
				t.Fatalf("examined %d > segments+1 %d", examined, segments+1)
			}
		})
	}
}

func TestNewTableValidation(t *testing.T) {
	cases := []struct {
		name   string
		routes []Route
		err    error
	}{
		{"unnormalized", []Route{{Prefix: "/a/", Backend: "x"}}, ErrInvalidPrefix},
		{"empty backend first", []Route{{Prefix: "/a", Backend: ""}}, ErrEmptyBackend},
		{"duplicate", []Route{{Prefix: "/a", Backend: "x"}, {Prefix: "/a", Backend: "y"}}, ErrDuplicateRoute},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewTable(tc.routes); err != tc.err {
				t.Fatalf("err=%v want %v", err, tc.err)
			}
		})
	}
}

func TestLookupExaminedBoundAtTwoSizes(t *testing.T) {
	for _, n := range []int{100, 10000} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			routes := make([]Route, 0, n)
			for i := 0; i < n; i++ {
				routes = append(routes, Route{Prefix: "/p" + strings.ReplaceAll(fmt.Sprintf("/%08d", i), "/", "/d"), Backend: "b"})
			}
			table, err := NewTable(routes)
			if err != nil {
				t.Fatal(err)
			}
			path := routes[n-1].Prefix + "/leaf"
			match, examined, ok := table.lookupWithExamined(path)
			if !ok || match.Rest != "/leaf" {
				t.Fatalf("lookup failed: %+v %v", match, ok)
			}
			if examined > len(splitSegments(path))+1 || examined > 11 {
				t.Fatalf("n=%d examined=%d path=%s", n, examined, path)
			}
		})
	}
}
