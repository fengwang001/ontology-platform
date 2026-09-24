package jar_test

import (
	"errors"
	"net/url"
	"testing"
	"time"

	"ontology/attr"
	"ontology/jar"
	"ontology/match"
)

var base = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func u(s string) *url.URL { v, _ := url.Parse(s); return v }

func mustSet(t *testing.T, j *jar.Jar, raw, h string) {
	if err := j.Set(u(raw), h); err != nil {
		t.Fatalf("Set(%q): %v", h, err)
	}
}

func TestParseMatch(t *testing.T) { // 解析错误、未知属性、路径/域匹配、默认路径
	bads := map[string]error{ // want 互不相同，errors.Is 各自命中即证明错误可区分
		"noequal": attr.ErrNoEqual, "=v": attr.ErrEmptyName, "a=b\x01": attr.ErrControlChar,
		"a=b; Max-Age=x": attr.ErrBadMaxAge, "a=b; Expires=x": attr.ErrBadExpires,
	}
	for in, want := range bads {
		if _, err := attr.Parse(in); !errors.Is(err, want) {
			t.Errorf("Parse(%q) err=%v", in, err)
		}
	}
	if c, err := attr.Parse("a=b; What=1; Secure"); err != nil || !c.Secure {
		t.Errorf("unknown attribute not ignored: %v", err)
	}
	for _, c := range []struct {
		got  bool
		name string
	}{
		{match.Path("/a", "/a"), "path self"}, {match.Path("/a/", "/a"), "path slash"},
		{match.Path("/a/b", "/a"), "path sub"}, {!match.Path("/ab", "/a"), "path /ab rejected"},
		{match.Domain("example.com", "example.com"), "domain self"},
		{match.Domain("sub.example.com", "example.com"), "domain sub"},
		{!match.Domain("example.com", "sub.example.com"), "domain parent rejected"},
		{!match.Domain("badexample.com", "example.com"), "domain suffix rejected"},
		{match.DefaultPath("", "/a/b/c") == "/a/b", "defpath /a/b/c"},
		{match.DefaultPath("", "/a") == "/", "defpath /a"},
		{match.DefaultPath("", "/") == "/", "defpath /"},
		{match.DefaultPath("/x", "/a/b") == "/x", "defpath explicit"},
	} {
		if !c.got {
			t.Error(c.name)
		}
	}
}

func TestRejections(t *testing.T) { // 越权 Domain 与前缀约束，理由彼此可区分
	j := jar.New(func() time.Time { return base })
	cases := []struct {
		url, header string
		want        error
	}{ // want 互不相同，errors.Is 各自命中即证明理由可区分
		{"https://b.example.com/", "x=1; Domain=example.org", jar.ErrDomainNotAllowed},
		{"https://b.example.com/", "x=1; Domain=com", jar.ErrPublicSuffix},
		{"http://h.com/", "__Host-a=1; Path=/", jar.ErrHostNeedSecure},
		{"https://h.com/", "__Host-a=1; Path=/; Domain=h.com", jar.ErrHostHasDomain},
		{"https://h.com/", "__Host-a=1; Path=/x", jar.ErrHostBadPath},
		{"http://h.com/", "__Secure-a=1", jar.ErrSecureNeedSecure},
	}
	for _, c := range cases {
		if err := j.Set(u(c.url), c.header); !errors.Is(err, c.want) {
			t.Errorf("Set(%q) err=%v", c.header, err)
		}
	}
	if err := j.Set(u("https://h.com/"), "__Host-ok=1; Path=/; Secure"); err != nil || j.Len() != 1 {
		t.Errorf("valid __Host- rejected or bad ones stored: %v, len=%d", err, j.Len())
	}
}

func TestJarBehavior(t *testing.T) { // 过期优先级、host-only、排序、惰性过期
	now := base
	j := jar.New(func() time.Time { return now })
	mustSet(t, j, "https://e.com/", "m=1; Max-Age=100; Expires=Wed, 01 Jan 2020 00:00:00 GMT")
	mustSet(t, j, "https://e.com/", "n=1; Max-Age=5")
	mustSet(t, j, "https://e.com/", "n=2; Max-Age=0")  // Max-Age<=0 即删
	if j.Len() != 1 || j.CountByDomain("e.com") != 1 { // m 存活：Max-Age 压过过去的 Expires
		t.Fatalf("Max-Age precedence/delete: len=%d", j.Len())
	}
	mustSet(t, j, "https://sub.example.com/", "ho=1")
	mustSet(t, j, "https://sub.example.com/", "dm=1; Domain=.example.com")
	if a, b := len(j.Cookies(u("https://other.example.com/"))), len(j.Cookies(u("https://sub.example.com/"))); a != 1 || b != 2 {
		t.Fatalf("host-only/subdomain: %d, %d", a, b)
	}
	now = base // 排序组1：路径长者优先；等长创建早者优先；覆盖保留最早创建时间
	mustSet(t, j, "https://h.com/p", "b=1")
	now = base.Add(time.Second)
	mustSet(t, j, "https://h.com/q", "a=1")
	mustSet(t, j, "https://h.com/x/y", "deep=1; Path=/x")
	now = base.Add(2 * time.Second)
	mustSet(t, j, "https://h.com/p", "b=2") // 覆盖 b，创建时间仍最早
	if cs := j.Cookies(u("https://h.com/x")); len(cs) != 3 || cs[0].Name != "deep" || cs[1].Name != "b" || cs[2].Name != "a" {
		t.Fatalf("order after overwrite: %v", cs)
	}
	mustSet(t, j, "https://h.com/x", "c1=1; Path=/p") // 排序组2：等长路径、创建时间不同
	now = base.Add(3 * time.Second)
	mustSet(t, j, "https://h.com/x", "c2=1; Path=/p")
	if cs := j.Cookies(u("https://h.com/p")); len(cs) != 4 || cs[0].Name != "c1" || cs[1].Name != "c2" {
		t.Fatalf("equal-length path order: %v", cs)
	}
	now = base // 惰性过期：查询剔除并真正移除，连查两次一致
	mustSet(t, j, "https://h.com/", "t=1; Max-Age=5")
	before := j.Len()
	now = base.Add(6 * time.Second)
	// t 已过期被剔除，剩 b、a；两次查询结果一致
	if a, b := len(j.Cookies(u("https://h.com/"))), len(j.Cookies(u("https://h.com/"))); j.Len() != before-1 || a != 2 || a != b {
		t.Fatalf("lazy expiry: before=%d len=%d", before, j.Len())
	}
}
