package jar_test

import (
	"errors"
	"testing"
	"time"

	"ontology/attr"
	"ontology/jar"
	"ontology/match"
)

var now = time.Unix(1_700_000_000, 0)

func clock() time.Time { return now }
func eq[T comparable](t *testing.T, got, want T) {
	if got != want {
		t.Errorf("got %v, want %v", got, want)
	}
}
func set(t *testing.T, j *jar.Jar, line, url string) {
	if err := j.SetCookie(line, url); err != nil {
		t.Fatalf("%s: %v", line, err)
	}
}
func TestParse(t *testing.T) {
	errs := []error{attr.ErrNoEquals, attr.ErrEmptyName, attr.ErrControl, attr.ErrBadMaxAge, attr.ErrBadExpires}
	lines := []string{"noeq", "=v", "a=\x01b", "a=1; Max-Age=x", "a=1; Expires=nope"}
	for i, l := range lines {
		_, err := attr.Parse(l)
		for j, e := range errs {
			eq(t, errors.Is(err, e), i == j)
		}
	}
	c, err := attr.Parse("s=1; UNKNOWN=x; HTTPONLY; max-age=5")
	d, _ := attr.Parse("s=1; Domain=.Example.COM")
	eq(t, err == nil && c.HTTPOnly && c.HasMaxAge && c.MaxAge == 5 && d.Domain == "example.com", true)
}

func TestMatch(t *testing.T) {
	for in, want := range map[string]string{"/a/b/c": "/a/b", "/a": "/", "/": "/", "x": "/"} {
		eq(t, match.DefaultPath(in), want)
	}
	pc := []string{"/a", "/a", "/a", "/a", "/a/", "/a/"}
	pr := []string{"/a", "/a/", "/a/b", "/ab", "/a/b", "/a"}
	pw := []bool{true, true, true, false, true, false}
	for i := range pc {
		eq(t, match.PathMatch(pc[i], pr[i]), pw[i])
	}
	eq(t, match.DomainMatch("a.example.com", "a.example.com", true), true)
	eq(t, match.DomainMatch("a.example.com", "b.a.example.com", true), false)
	eq(t, match.DomainMatch("example.com", "b.example.com", false), true)
	dd := []string{"example.com", "b.example.com", "example.org", "com"}
	dw := []bool{true, true, false, false}
	for i := range dd {
		eq(t, match.DomainAllowed("b.example.com", dd[i]), dw[i])
	}
}

func TestJar(t *testing.T) {
	j := jar.New(clock)
	set(t, j, "m=1; Max-Age=3600; Expires=Mon, 02 Jan 2006 15:04:05 GMT", "https://h.com/")
	eq(t, len(j.Cookies("https://h.com/")), 1) // Max-Age 压过已过去的 Expires
	for _, ma := range []string{"0", "-1"} {   // 非正 Max-Age 即删
		set(t, j, "m=2; Max-Age="+ma, "https://h.com/")
		eq(t, j.Len(), 0)
		set(t, j, "m=1; Max-Age=3600", "https://h.com/")
	}
	for _, d := range []string{"example.org", "com"} { // Domain 越权两类
		eq(t, errors.Is(j.SetCookie("x=1; Domain="+d, "https://b.example.com/"), jar.ErrDomainNotAllowed), true)
	}
	j = jar.New(clock)
	set(t, j, "ho=1", "https://a.example.com/")
	set(t, j, "dm=1; Domain=.example.com", "https://a.example.com/")
	eq(t, len(j.Cookies("https://a.example.com/")), 2)
	eq(t, len(j.Cookies("https://b.a.example.com/")), 1) // host-only 不匹配子域
	eq(t, j.LenFor("b.a.example.com"), 1)
	eq(t, j.LenFor("other.org"), 0)
	set(t, j, "t=1; Max-Age=10", "https://h.com/")
	now = now.Add(time.Hour)
	eq(t, j.Len(), 3)                          // 只读查询不剔除
	eq(t, len(j.Cookies("https://h.com/")), 0) // 适用性查询剔除过期项
	eq(t, j.Len(), 2)                          // 真正从罐里移除
	eq(t, len(j.Cookies("https://h.com/")), 0) // 同时刻连查一致
}

func TestPrefixes(t *testing.T) {
	j := jar.New(clock)
	lines := []string{"__Host-a=1; Path=/", "__Host-b=1; Path=/; Domain=h.com", "__Host-c=1; Path=/x", "__Secure-d=1"}
	urls := []string{"http://h.com/", "https://h.com/", "https://h.com/", "http://h.com/"}
	want := []error{jar.ErrHostNeedSecure, jar.ErrHostHasDomain, jar.ErrHostBadPath, jar.ErrSecureNeedSecure}
	var got []error
	for i := range lines {
		got = append(got, j.SetCookie(lines[i], urls[i]))
		eq(t, errors.Is(got[i], want[i]), true)
	}
	eq(t, errors.Is(got[0], got[1]) || errors.Is(got[0], got[2]) || errors.Is(got[1], got[2]), false)
	set(t, j, "__Host-ok=1; Secure; Path=/", "https://h.com/")
	set(t, j, "__Secure-ok=1; Secure", "https://h.com/")
	eq(t, j.Len(), 2)
}

func TestOrder(t *testing.T) {
	j := jar.New(clock)
	set(t, j, "a=1; Path=/", "https://h.com/")
	now = now.Add(time.Hour)
	set(t, j, "b=1; Path=/", "https://h.com/")
	now = now.Add(time.Hour)
	set(t, j, "a=2; Path=/", "https://h.com/") // 覆盖：不新增且保留最早创建时间
	eq(t, j.Len(), 2)
	cs := j.Cookies("https://h.com/")
	eq(t, cs[0].Name+cs[1].Name+cs[0].Value, "ab2")
	set(t, j, "c=1; Path=/xy", "https://h.com/")
	now = now.Add(time.Hour)
	set(t, j, "d=1; Path=/xy", "https://h.com/")
	now = now.Add(time.Hour)
	set(t, j, "c=2; Path=/xy", "https://h.com/")
	cs = j.Cookies("https://h.com/xy")
	eq(t, cs[0].Name+cs[1].Name+cs[2].Name+cs[3].Name, "cdab")
}
