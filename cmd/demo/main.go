package main

import (
	"errors"
	"fmt"
	"net/url"
	"time"

	"ontology/attr"
	"ontology/jar"
	"ontology/match"
)

var passed, failed int

func U(s string) *url.URL { u, _ := url.Parse(s); return u }

func check(name string, ok bool) {
	if ok {
		passed++
		fmt.Println("OK  " + name)
	} else {
		failed++
		fmt.Println("FAIL " + name)
	}
}

func main() {
	bad := []string{"noequal", "=v", "a=b\x01c", "a=b; Max-Age=xyz", "a=b; Expires=nope"}
	seen := map[error]bool{}
	ok := true
	for _, s := range bad {
		if _, err := attr.Parse(s); err == nil || seen[err] {
			ok = false
		} else {
			seen[err] = true
		}
	}
	check("parse errors distinguishable", ok && len(seen) == len(bad))
	c, err := attr.Parse("a=b; X-Unknown=1; Secure")
	check("unknown attribute ignored", err == nil && c.Secure)
	check("path /a not prefix-match /ab", match.Path("/a", "/a") &&
		match.Path("/a/", "/a") && match.Path("/a/b", "/a") && !match.Path("/ab", "/a"))

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := base
	newJar := func() *jar.Jar { return jar.New(func() time.Time { return now }) }

	j1 := newJar()
	ok = j1.Set(U("https://a.example.com/"), "m=1; Max-Age=100; Expires=Wed, 01 Jan 2020 00:00:00 GMT") == nil
	check("Max-Age overrides Expires", ok && j1.Len() == 1)

	j2 := newJar()
	ok = j2.Set(U("https://a.example.com/"), "m=1; Max-Age=100") == nil &&
		j2.Set(U("https://a.example.com/"), "m=1; Max-Age=-1") == nil
	check("negative Max-Age deletes", ok && j2.Len() == 0)

	j3 := newJar()
	ok = j3.Set(U("https://h.com/a/b/c"), "p1=1") == nil &&
		j3.Set(U("https://h.com/a"), "p2=1") == nil && j3.Set(U("https://h.com/"), "p3=1") == nil
	check("default path /a/b / /", ok && len(j3.Cookies(U("https://h.com/a/b"))) == 3 &&
		len(j3.Cookies(U("https://h.com/other"))) == 2 && len(j3.Cookies(U("https://h.com/a/bx"))) == 2)

	j4 := newJar()
	ok = j4.Set(U("https://sub.example.com/"), "ho=1") == nil &&
		j4.Set(U("https://sub.example.com/"), "dm=1; Domain=.example.com") == nil
	check("host-only vs Domain subdomain", ok &&
		len(j4.Cookies(U("https://sub.example.com/"))) == 2 &&
		len(j4.Cookies(U("https://other.example.com/"))) == 1 &&
		len(j4.Cookies(U("https://example.com/"))) == 1)

	j5 := newJar()
	e1 := j5.Set(U("https://b.example.com/"), "x=1; Domain=example.org")
	e2 := j5.Set(U("https://b.example.com/"), "y=1; Domain=com")
	check("foreign & public-suffix Domain rejected", errors.Is(e1, jar.ErrDomainNotAllowed) &&
		errors.Is(e2, jar.ErrPublicSuffix) && !errors.Is(e1, jar.ErrPublicSuffix) && j5.Len() == 0)

	j6 := newJar()
	r1 := j6.Set(U("http://h.com/"), "__Host-a=1; Path=/")
	r2 := j6.Set(U("https://h.com/"), "__Host-b=1; Path=/; Domain=h.com")
	r3 := j6.Set(U("https://h.com/"), "__Host-c=1; Path=/x")
	r4 := j6.Set(U("http://h.com/"), "__Secure-d=1")
	ok = errors.Is(r1, jar.ErrHostNeedSecure) && errors.Is(r2, jar.ErrHostHasDomain) &&
		errors.Is(r3, jar.ErrHostBadPath) && errors.Is(r4, jar.ErrSecureNeedSecure) &&
		r1 != r2 && r2 != r3 && r1 != r3 && j6.Len() == 0
	ok = ok && j6.Set(U("https://h.com/"), "__Host-ok=1; Path=/; Secure") == nil && j6.Len() == 1
	check("__Host- x3 & __Secure- rejections distinct", ok)

	j7 := newJar()
	ok = j7.Set(U("https://h.com/p"), "b=1") == nil
	now = base.Add(time.Second)
	ok = ok && j7.Set(U("https://h.com/q"), "a=1") == nil
	now = base.Add(2 * time.Second)
	ok = ok && j7.Set(U("https://h.com/p"), "b=2") == nil
	cs := j7.Cookies(U("https://h.com/"))
	check("overwrite keeps earliest created", ok && len(cs) == 2 &&
		cs[0].Name == "b" && cs[0].Value == "2" && cs[1].Name == "a")

	now = base
	j8 := newJar()
	ok = j8.Set(U("https://h.com/"), "t=1; Max-Age=5") == nil
	before := j8.Len()
	now = base.Add(6 * time.Second)
	ok = ok && len(j8.Cookies(U("https://h.com/"))) == 0
	check("lazy expiry shrinks jar, query idempotent", ok && before == 1 && j8.Len() == 0 &&
		len(j8.Cookies(U("https://h.com/"))) == 0)

	fmt.Printf("total: %d passed, %d failed\n", passed, failed)
	if failed > 0 {
		panic("demo failed")
	}
}
