// Command demo 逐条演示 cookie 罐的核心语义判定，每步一行 OK/FAIL。
package main

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"time"

	"ontology/attr"
	"ontology/jar"
	"ontology/match"
)

var total, fails int

func check(name string, ok bool) {
	total++
	mark := "OK  "
	if !ok {
		mark = "FAIL"
		fails++
	}
	fmt.Println(mark, name)
}

func main() {
	// attr：未知属性被忽略而不是报错
	c, err := attr.Parse("n=v; X-Unknown=1; Secure")
	check("未知属性被忽略", err == nil && c.Secure && c.Name == "n")

	// attr：五类解析错误彼此可区分
	wantErrs := []struct {
		in   string
		want error
	}{
		{"noequals", attr.ErrNoEqual},
		{"=v", attr.ErrEmptyName},
		{"a\x01b=v", attr.ErrControlChar},
		{"a=b; Max-Age=xyz", attr.ErrBadMaxAge},
		{"a=b; Expires=not-a-date", attr.ErrBadExpires},
	}
	ok := true
	for _, w := range wantErrs {
		_, err := attr.Parse(w.in)
		ok = ok && errors.Is(err, w.want)
	}
	sentinels := []error{attr.ErrNoEqual, attr.ErrEmptyName, attr.ErrControlChar, attr.ErrBadMaxAge, attr.ErrBadExpires}
	for i, a := range sentinels {
		for _, b := range sentinels[i+1:] {
			ok = ok && !errors.Is(a, b)
		}
	}
	check("解析错误五类可区分", ok)

	// match：默认路径三例推导
	check("默认路径 /a/b/c→/a/b, /a→/, /→/",
		match.DefaultPath("/a/b/c") == "/a/b" &&
			match.DefaultPath("/a") == "/" &&
			match.DefaultPath("/") == "/")

	// match：路径匹配不是前缀匹配
	check("/a 匹配 /a、/a/、/a/b 但不匹配 /ab",
		match.PathMatch("/a", "/a") && match.PathMatch("/a", "/a/") &&
			match.PathMatch("/a", "/a/b") && !match.PathMatch("/a", "/ab"))

	// jar：Max-Age 压过 Expires；负 Max-Age 即删
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	j := jar.New(func() time.Time { return now })
	src := u("https://b.example.com/a/b/c")
	j.Set("ma=1; Max-Age=100; Expires=Wed, 01 Jan 2020 00:00:00 GMT", src)
	check("Max-Age 压过 Expires", len(j.Cookies(src)) == 1)
	j.Set("ma=1; Max-Age=-1", src)
	check("负 Max-Age 即删", j.Count() == 0)

	// jar：host-only 与 Domain 子域匹配
	j.Set("h=1", src)
	j.Set("d=1; Domain=example.com", src)
	check("host-only 不匹配子域, Domain 匹配子域",
		len(j.Cookies(u("https://x.b.example.com/a/b/c"))) == 1 && len(j.Cookies(src)) == 2)

	// jar：Domain 越权两类被拒
	e1 := j.Set("e=1; Domain=example.org", src)
	e2 := j.Set("e=1; Domain=com", src)
	check("Domain 越权两类被拒",
		errors.Is(e1, jar.ErrDomainNotAllowed) && errors.Is(e2, jar.ErrDomainNotAllowed))

	// jar：__Host- 三种拒绝彼此可区分
	r1 := j.Set("__Host-a=1; Path=/", u("http://b.example.com/"))
	r2 := j.Set("__Host-a=1; Path=/; Domain=example.com", src)
	r3 := j.Set("__Host-a=1; Path=/x", src)
	ok = errors.Is(r1, jar.ErrInsecureOrigin) && errors.Is(r2, jar.ErrHostPrefixDomain) &&
		errors.Is(r3, jar.ErrHostPrefixPath) && !errors.Is(r1, r2) && !errors.Is(r2, r3)
	check("__Host- 三种拒绝可区分", ok && j.Set("__Host-ok=1; Path=/", src) == nil)

	// jar：同身份覆盖保留最早创建时间
	t0 := now
	j.Set("o=1; Path=/ov", src)
	now = now.Add(time.Hour)
	j.Set("o=2; Path=/ov", src)
	got := j.Cookies(u("https://b.example.com/ov"))
	check("覆盖保留最早创建时间", len(got) == 2 && got[0].Name == "o" && got[0].Created.Equal(t0))

	// jar：惰性过期前后条数变化，同时刻连查一致
	j.Set("x=1; Max-Age=10", src)
	before := j.Count()
	now = now.Add(11 * time.Second)
	n1 := len(j.Cookies(src))
	check("惰性过期条数变化且连查一致", before == j.Count()+1 && n1 == len(j.Cookies(src)))

	fmt.Printf("TOTAL %d/%d OK\n", total-fails, total)
	if fails > 0 {
		os.Exit(1)
	}
}

func u(s string) *url.URL { p, _ := url.Parse(s); return p }
