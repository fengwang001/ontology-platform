package jar_test

import (
	"errors"
	"net/url"
	"slices"
	"testing"
	"time"

	"ontology/attr"
	"ontology/jar"
	"ontology/match"
)

var t0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func u(s string) *url.URL { p, _ := url.Parse(s); return p }

func eq[T comparable](t *testing.T, got, want T) {
	t.Helper()
	if got != want {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParse(t *testing.T) {
	for in, want := range map[string]error{
		"noequals":          attr.ErrNoEqual,
		"=v":                attr.ErrEmptyName,
		"a\x01b=v":          attr.ErrControlChar,
		"a=b; Max-Age=xyz":  attr.ErrBadMaxAge,
		"a=b; Expires=nope": attr.ErrBadExpires,
	} {
		_, err := attr.Parse(in)
		eq(t, errors.Is(err, want), true)
	}
	errs := []error{attr.ErrNoEqual, attr.ErrEmptyName, attr.ErrControlChar, attr.ErrBadMaxAge, attr.ErrBadExpires,
		jar.ErrInsecureOrigin, jar.ErrHostPrefixDomain, jar.ErrHostPrefixPath, jar.ErrDomainNotAllowed}
	for i, a := range errs { // 九类错误两两可区分
		for _, b := range errs[i+1:] {
			eq(t, errors.Is(a, b), false)
		}
	}
	c, err := attr.Parse("n=v; X-Unknown=1; Secure; HttpOnly; Expires=Wed, 01 Jan 2031 00:00:00 GMT")
	eq(t, err == nil && c.Name == "n" && c.Secure && c.HttpOnly && c.HasExpires, true) // 未知属性被忽略
}

func TestMatch(t *testing.T) {
	for in, want := range map[string]string{"/a/b/c": "/a/b", "/a": "/", "/": "/", "x": "/"} {
		eq(t, match.DefaultPath(in), want)
	}
	for c, want := range map[[2]string]bool{{"/a", "/a"}: true, {"/a", "/a/"}: true, {"/a", "/a/b"}: true, {"/a", "/ab"}: false} {
		eq(t, match.PathMatch(c[0], c[1]), want)
	}
	for c, want := range map[[2]string]bool{
		{"b.example.com", "example.com"}:  true,
		{"b.example.com", ".EXAMPLE.com"}: true,
		{"b.example.com", "example.org"}:  false,
		{"b.example.com", "com"}:          false,
	} {
		eq(t, match.SetDomainOK(c[0], c[1]), want)
	}
}

func TestJar(t *testing.T) {
	now := t0
	j := jar.New(func() time.Time { return now })
	src := u("https://b.example.com/a/b/c")
	for h, want := range map[string]error{
		"e=1; Domain=example.org":                jar.ErrDomainNotAllowed,
		"e=1; Domain=com":                        jar.ErrDomainNotAllowed,
		"__Host-a=1; Path=/; Domain=example.com": jar.ErrHostPrefixDomain,
		"__Host-a=1; Path=/x":                    jar.ErrHostPrefixPath,
	} {
		eq(t, errors.Is(j.Set(h, src), want), true)
	}
	eq(t, errors.Is(j.Set("__Host-a=1; Path=/", u("http://b.example.com/")), jar.ErrInsecureOrigin), true)
	eq(t, errors.Is(j.Set("__Secure-s=1", u("http://b.example.com/")), jar.ErrInsecureOrigin), true)
	eq(t, j.Count(), 0) // 被拒的一律不入罐
	eq(t, j.Set("ma=1; Max-Age=100; Expires=Wed, 01 Jan 2020 00:00:00 GMT", src), nil)
	eq(t, len(j.Cookies(src)), 1) // Max-Age 压过 Expires
	eq(t, j.Set("ma=1; Max-Age=0", src), nil)
	eq(t, j.Set("mb=1", src), nil)
	eq(t, j.Set("mb=1; Max-Age=-5", src), nil)
	eq(t, j.Count(), 0) // Max-Age 为 0 或负：立即删除
	eq(t, j.Set("h=1", src), nil)
	eq(t, j.Set("d=1; Domain=example.com", src), nil)
	eq(t, len(j.Cookies(u("https://x.b.example.com/a/b/c"))), 1) // host-only 不匹配子域
	eq(t, len(j.Cookies(src)), 2)
	eq(t, j.CountDomain("example.com"), 1)
	eq(t, j.CountDomain("b.example.com"), 1)
	eq(t, j.Set("x=1; Max-Age=10", src), nil)
	before := j.Count()
	now = now.Add(11 * time.Second)
	first := j.Cookies(src)
	eq(t, j.Count(), before-1) // 惰性过期须真正移除
	eq(t, slices.Equal(first, j.Cookies(src)), true)
}

func TestOrder(t *testing.T) {
	now := t0
	j := jar.New(func() time.Time { return now })
	src := u("https://h.example/x/y/z")
	for _, h := range []string{"g1a=1; Path=/x", "g2a=1; Path=/x/y", "g1b=1; Path=/x", "g2b=1; Path=/x/y"} {
		eq(t, j.Set(h, src), nil)
		now = now.Add(time.Second)
	}
	eq(t, j.Set("g1a=2; Path=/x", src), nil) // 覆盖：创建时间仍为 t0
	var names []string
	for _, e := range j.Cookies(src) {
		names = append(names, e.Name)
	}
	eq(t, slices.Equal(names, []string{"g2a", "g2b", "g1a", "g1b"}), true) // 路径长优先，等长创建早优先
	eq(t, j.Cookies(src)[2].Created.Equal(t0), true)
}
