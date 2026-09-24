package main

import (
	"errors"
	"fmt"
	"time"

	"ontology/attr"
	"ontology/jar"
	"ontology/match"
)

var now = time.Unix(1_700_000_000, 0)

func clock() time.Time { return now }

func newJar() *jar.Jar { return jar.New(clock) }

var checks int
var fails int

func report(name string, ok bool) {
	checks++
	mark := "OK"
	if !ok {
		mark = "FAIL"
		fails++
	}
	fmt.Printf("%s %s\n", mark, name)
}

func main() {
	c, err := attr.Parse("a=1; X-Unknown=zzz; Secure")
	report("未知属性被忽略", err == nil && c.Secure && c.Name == "a")

	e1, e2, e3, e4, e5 := errs()
	ok := errors.Is(e1, attr.ErrNoEquals) && errors.Is(e2, attr.ErrEmptyName) &&
		errors.Is(e3, attr.ErrControl) && errors.Is(e4, attr.ErrBadMaxAge) &&
		errors.Is(e5, attr.ErrBadExpires) &&
		!errors.Is(e1, e2) && !errors.Is(e2, e3) && !errors.Is(e4, e5)
	report("解析错误可区分", ok)

	d1 := match.DefaultPath("/a/b/c") == "/a/b"
	d2 := match.DefaultPath("/a") == "/"
	d3 := match.DefaultPath("/") == "/"
	report("默认路径三例", d1 && d2 && d3)

	pm := match.PathMatch("/a", "/a") && match.PathMatch("/a", "/a/") &&
		match.PathMatch("/a", "/a/b") && !match.PathMatch("/a", "/ab")
	report("/a 与 /ab 不匹配", pm)

	jarChecks()
	fmt.Printf("total %d/%d\n", checks-fails, checks)
}

func jarChecks() {
	j := newJar()
	_ = j.SetCookie("m=1; Max-Age=3600; Expires=Mon, 02 Jan 2006 15:04:05 GMT", "https://h.com/")
	report("Max-Age 压过 Expires", len(j.Cookies("https://h.com/")) == 1)

	_ = j.SetCookie("m=2; Max-Age=-5", "https://h.com/")
	report("负 Max-Age 即删", j.Len() == 0)

	j = newJar()
	_ = j.SetCookie("ho=1", "https://a.example.com/")
	_ = j.SetCookie("dm=1; Domain=example.com", "https://a.example.com/")
	h1 := len(j.Cookies("https://a.example.com/")) == 2
	h2 := len(j.Cookies("https://b.a.example.com/")) == 1
	report("host-only 与子域匹配", h1 && h2)

	e1 := j.SetCookie("x=1; Domain=example.org", "https://b.example.com/")
	e2 := j.SetCookie("x=1; Domain=com", "https://b.example.com/")
	report("Domain 越权两类被拒", errors.Is(e1, jar.ErrDomainNotAllowed) &&
		errors.Is(e2, jar.ErrDomainNotAllowed) && j.Len() == 2)

	r1 := j.SetCookie("__Host-a=1; Path=/", "http://h.com/")
	r2 := j.SetCookie("__Host-b=1; Path=/; Domain=h.com", "https://h.com/")
	r3 := j.SetCookie("__Host-c=1; Path=/x", "https://h.com/")
	ok := errors.Is(r1, jar.ErrHostNeedSecure) && errors.Is(r2, jar.ErrHostHasDomain) &&
		errors.Is(r3, jar.ErrHostBadPath) && !errors.Is(r1, r2) && !errors.Is(r2, r3)
	report("__Host- 三种拒绝可区分", ok)

	j = newJar()
	_ = j.SetCookie("a=1; Path=/", "https://h.com/")
	now = now.Add(time.Hour)
	_ = j.SetCookie("b=1; Path=/", "https://h.com/")
	now = now.Add(time.Hour)
	_ = j.SetCookie("a=2; Path=/", "https://h.com/")
	cs := j.Cookies("https://h.com/")
	report("覆盖保留最早时间", len(cs) == 2 && cs[0].Name == "a" && cs[0].Value == "2")

	j = newJar()
	_ = j.SetCookie("t=1; Max-Age=10", "https://h.com/")
	before := j.Len()
	now = now.Add(time.Hour)
	n := len(j.Cookies("https://h.com/"))
	report("惰性过期条数变化", before == 1 && n == 0 && j.Len() == 0)
}

func errs() (e1, e2, e3, e4, e5 error) {
	_, e1 = attr.Parse("noequals")
	_, e2 = attr.Parse("=v")
	_, e3 = attr.Parse("a=\x01b")
	_, e4 = attr.Parse("a=1; Max-Age=xyz")
	_, e5 = attr.Parse("a=1; Expires=not-a-date")
	return
}
