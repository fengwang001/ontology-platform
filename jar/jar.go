// Package jar 进程内 cookie 罐：解析、存储、覆盖、惰性过期与适用性判定。
package jar

import (
	"errors"
	"maps"
	"net/url"
	"slices"
	"strings"
	"time"

	"ontology/attr"
	"ontology/match"
)

var (
	ErrInsecureOrigin   = errors.New("jar: __Secure-/__Host- cookie requires secure origin")
	ErrHostPrefixDomain = errors.New("jar: __Host- cookie must not carry Domain")
	ErrHostPrefixPath   = errors.New("jar: __Host- cookie must have Path=/")
	ErrDomainNotAllowed = errors.New("jar: Domain attribute not allowed for origin host")
)

// Entry 是罐内一条 cookie，身份三元组为 (Name, Domain, Path)。
type Entry struct {
	Name, Value, Domain, Path string
	HostOnly, Secure          bool
	Created, expires          time.Time // Created 覆盖时保留最早那次；expires 零值即会话 cookie
}

// Jar 是进程内 cookie 罐，时间只走注入时钟。
type Jar struct {
	now func() time.Time
	m   map[string]*Entry
}

func New(now func() time.Time) *Jar { return &Jar{now: now, m: map[string]*Entry{}} }

func (j *Jar) Set(header string, src *url.URL) error {
	c, err := attr.Parse(header)
	if err != nil {
		return err
	}
	host, secure := strings.ToLower(src.Hostname()), src.Scheme == "https"
	domain, hostOnly := host, true
	if c.Domain != "" {
		if !match.SetDomainOK(host, c.Domain) {
			return ErrDomainNotAllowed
		}
		domain, hostOnly = strings.TrimPrefix(strings.ToLower(c.Domain), "."), false
	}
	path := c.Path
	if !strings.HasPrefix(path, "/") {
		path = match.DefaultPath(src.Path)
	}
	isHost := strings.HasPrefix(c.Name, "__Host-")
	switch {
	case (isHost || strings.HasPrefix(c.Name, "__Secure-")) && !secure:
		return ErrInsecureOrigin
	case isHost && c.Domain != "":
		return ErrHostPrefixDomain
	case isHost && c.Path != "/":
		return ErrHostPrefixPath
	}
	exp := c.Expires
	if c.HasMaxAge { // Max-Age 优先于 Expires
		exp = j.now().Add(time.Duration(c.MaxAge) * time.Second)
	}
	k := c.Name + "\x00" + domain + "\x00" + path
	if !exp.IsZero() && !exp.After(j.now()) { // 立即/已经过期：删除而不是存一个过期的
		delete(j.m, k)
		return nil
	}
	e := &Entry{Name: c.Name, Value: c.Value, Domain: domain, Path: path, HostOnly: hostOnly, Secure: c.Secure, Created: j.now(), expires: exp}
	if old, ok := j.m[k]; ok { // 同身份覆盖：保留最早创建时间
		e.Created = old.Created
	}
	j.m[k] = e
	return nil
}

func (j *Jar) purge() {
	maps.DeleteFunc(j.m, func(_ string, e *Entry) bool {
		return !e.expires.IsZero() && !e.expires.After(j.now())
	})
}

func (j *Jar) Cookies(u *url.URL) []*Entry { // 适用 cookie：路径更长在前，等长则创建更早在前
	j.purge()
	var out []*Entry
	for _, e := range j.m {
		if match.DomainMatch(u.Hostname(), e.Domain) &&
			(!e.HostOnly || strings.EqualFold(u.Hostname(), e.Domain)) &&
			(!e.Secure || u.Scheme == "https") && match.PathMatch(e.Path, u.Path) {
			out = append(out, e)
		}
	}
	slices.SortFunc(out, func(a, b *Entry) int {
		if d := len(b.Path) - len(a.Path); d != 0 {
			return d
		}
		if c := a.Created.Compare(b.Created); c != 0 {
			return c
		}
		return strings.Compare(a.Name+"\x00"+a.Domain, b.Name+"\x00"+b.Domain) // 决胜项保证顺序确定
	})
	return out
}
func (j *Jar) Count() int { j.purge(); return len(j.m) } // 罐内条数（只推进惰性过期）
// CountDomain 返回某域下的条数（只推进惰性过期）。
func (j *Jar) CountDomain(domain string) int {
	j.purge()
	n := 0
	for _, e := range j.m {
		if e.Domain == strings.ToLower(domain) {
			n++
		}
	}
	return n
}
