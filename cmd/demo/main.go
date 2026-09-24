// Command demo exercises the cookie jar end to end.
package main

import (
	"errors"
	"fmt"
	"os"
	"time"

	"ontology/attr"
	"ontology/jar"
	"ontology/match"
)

var (
	fails int
	now   = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
)

func clock() time.Time { return now }

func ok(name string, pass bool) {
	mark := "OK  "
	if pass {
		mark = "OK  "
	} else {
		mark = "FAIL"
		fails++
	}
	fmt.Printf("%s %s\n", mark, name)
}

func main() {
	if _, err := attr.Parse("a=1; Unknown=x; Another"); err != nil {
		ok("unknown attribute ignored", false)
	} else {
		ok("unknown attribute ignored", true)
	}
	cases := []struct {
		line string
		want error
	}{
		{"noequalsign", attr.ErrNoEqual},
		{"=v", attr.ErrEmptyName},
		{"a=b\x01", attr.ErrControlChar},
		{"a=b; Max-Age=xyz", attr.ErrBadMaxAge},
		{"a=b; Expires=not-a-date", attr.ErrBadExpires},
	}
	seen := map[error]bool{}
	distinct := true
	for _, c := range cases {
		_, err := attr.Parse(c.line)
		if !errors.Is(err, c.want) || seen[c.want] {
			distinct = false
		}
		seen[c.want] = true
	}
	ok("parse errors distinguishable", distinct && len(seen) == len(cases))
	ok("default path /a/b/c -> /a/b", match.DefaultPath("/a/b/c") == "/a/b")
	ok("default path /a -> /", match.DefaultPath("/a") == "/")
	ok("default path / -> /", match.DefaultPath("/") == "/")
	ok("path /a not prefix-matched to /ab",
		match.PathMatch("/a", "/a") && match.PathMatch("/a", "/a/") &&
			match.PathMatch("/a", "/a/b") && !match.PathMatch("/a", "/ab"))
	ok("domain scope rule",
		match.DomainAllowed("b.example.com", "example.com") &&
			!match.DomainAllowed("b.example.com", "example.org") &&
			!match.DomainAllowed("b.example.com", "com"))
	j := jar.New(clock)
	if err := j.Set("https://example.com/", "a=1; Max-Age=3600; Expires=Wed, 01 Jan 2020 00:00:00 GMT"); err != nil {
		ok("Max-Age overrides Expires", false)
	} else {
		ok("Max-Age overrides Expires", j.Count() == 1)
	}
	j.Set("https://example.com/", "b=1")
	j.Set("https://example.com/", "b=2; Max-Age=-5")
	ok("negative Max-Age deletes", j.CountDomain("example.com") == 1)
	h := jar.New(clock)
	h.Set("https://example.com/", "h=1")
	h.Set("https://b.example.com/", "d=1; Domain=.example.com")
	ok("host-only vs subdomain",
		len(h.Applicable("https://sub.example.com/")) == 1 &&
			len(h.Applicable("https://example.com/")) == 2)
	err1 := h.Set("https://b.example.com/", "x=1; Domain=example.org")
	err2 := h.Set("https://b.example.com/", "y=1; Domain=com")
	ok("out-of-scope Domain rejected",
		errors.Is(err1, jar.ErrDomainNotAllowed) && errors.Is(err2, jar.ErrDomainNotAllowed))
	p := jar.New(clock)
	e1 := p.Set("http://example.com/", "__Host-a=1; Path=/")
	e2 := p.Set("https://example.com/", "__Host-b=1; Path=/; Domain=example.com")
	e3 := p.Set("https://example.com/", "__Host-c=1; Path=/x")
	e4 := p.Set("http://example.com/", "__Secure-d=1")
	ok("__Host-/__Secure- rejections distinct",
		errors.Is(e1, jar.ErrHostPrefix) && errors.Is(e2, jar.ErrHostPrefix) &&
			errors.Is(e3, jar.ErrHostPrefix) && errors.Is(e4, jar.ErrSecurePrefix) &&
			jar.ErrHostPrefix != jar.ErrSecurePrefix && p.Count() == 0)
	o := jar.New(clock)
	o.Set("https://example.com/", "o=1")
	first := o.Applicable("https://example.com/")[0].Created
	now = now.Add(time.Hour)
	o.Set("https://example.com/", "o=2")
	got := o.Applicable("https://example.com/")[0]
	ok("overwrite keeps earliest Created",
		o.Count() == 1 && got.Value == "2" && got.Created.Equal(first))
	x := jar.New(clock)
	x.Set("https://example.com/", "s=1; Max-Age=10")
	x.Set("https://example.com/", "t=1")
	before := x.Count()
	now = now.Add(11 * time.Second)
	r1 := x.Applicable("https://example.com/")
	r2 := x.Applicable("https://example.com/")
	ok("lazy expiry shrinks jar",
		before == 2 && x.Count() == 1 && len(r1) == 1 && len(r2) == 1 && r1[0].Name == r2[0].Name)
	fmt.Printf("total: %d failed\n", fails)
	if fails > 0 {
		os.Exit(1)
	}
}
