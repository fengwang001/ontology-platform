// Package jar 是进程内 cookie 罐：解析、存储、覆盖、惰性过期与适用性判定。
package jar

import (
	"cmp"
	"errors"
	"net/url"
	"ontology/attr"
	"ontology/match"
	"slices"
	"strings"
	"time"
)

var (
	ErrDomainNotAllowed = errors.New("jar: Domain not allowed for origin host")
	ErrHostNeedSecure   = errors.New("jar: __Host- requires secure origin")
	ErrHostHasDomain    = errors.New("jar: __Host- forbids Domain attribute")
	ErrHostBadPath      = errors.New("jar: __Host- requires Path=/")
	ErrSecureNeedSecure = errors.New("jar: __Secure- requires secure origin")
)

type entry struct {
	c                 attr.Cookie
	hostOnly, expires bool
	created, deadline time.Time
}

type Jar struct {
	now func() time.Time
	m   map[string]*entry
}

func New(now func() time.Time) *Jar { return &Jar{now: now, m: map[string]*entry{}} }

func (j *Jar) SetCookie(line, rawurl string) error {
	c, err1 := attr.Parse(line)
	u, err2 := url.Parse(rawurl)
	if err := cmp.Or(err1, err2); err != nil {
		return err
	}
	host, secure := strings.ToLower(u.Hostname()), u.Scheme == "https"
	domain, hostOnly := c.Domain, false
	if domain == "" {
		domain, hostOnly = host, true
	} else if !match.DomainAllowed(host, domain) {
		return ErrDomainNotAllowed
	}
	path := c.Path
	if !strings.HasPrefix(path, "/") {
		path = match.DefaultPath(u.EscapedPath())
	}
	isHost := strings.HasPrefix(c.Name, "__Host-")
	switch {
	case isHost && !secure:
		return ErrHostNeedSecure
	case isHost && !hostOnly:
		return ErrHostHasDomain
	case isHost && path != "/":
		return ErrHostBadPath
	case strings.HasPrefix(c.Name, "__Secure-") && !secure:
		return ErrSecureNeedSecure
	}
	k, now := c.Name+"\x00"+domain+"\x00"+path, j.now()
	e := &entry{c: *c, hostOnly: hostOnly, created: now}
	e.c.Domain, e.c.Path = domain, path
	if c.HasMaxAge {
		e.expires, e.deadline = true, now.Add(time.Duration(c.MaxAge)*time.Second)
	} else if c.HasExpires {
		e.expires, e.deadline = true, c.Expires
	}
	if e.expires && !e.deadline.After(now) {
		delete(j.m, k)
		return nil
	}
	if old, ok := j.m[k]; ok {
		e.created = old.created
	}
	j.m[k] = e
	return nil
}

// Cookies 返回对 rawurl 适用的 cookie，路径长的在前，等长则创建早的在前。
func (j *Jar) Cookies(rawurl string) []attr.Cookie {
	u, err := url.Parse(rawurl)
	if err != nil {
		return nil
	}
	host, path := strings.ToLower(u.Hostname()), cmp.Or(u.EscapedPath(), "/")
	now, es := j.now(), []*entry{}
	for k, e := range j.m {
		if e.expires && !e.deadline.After(now) {
			delete(j.m, k)
			continue
		}
		if match.DomainMatch(e.c.Domain, host, e.hostOnly) && match.PathMatch(e.c.Path, path) {
			es = append(es, e)
		}
	}
	slices.SortFunc(es, func(x, y *entry) int {
		return cmp.Or(cmp.Compare(len(y.c.Path), len(x.c.Path)),
			x.created.Compare(y.created), cmp.Compare(x.c.Name, y.c.Name))
	})
	out := make([]attr.Cookie, len(es))
	for i, e := range es {
		out[i] = e.c
	}
	return out
}

func (j *Jar) Len() int { return len(j.m) }

func (j *Jar) LenFor(host string) (n int) {
	for _, e := range j.m {
		if match.DomainMatch(e.c.Domain, host, e.hostOnly) {
			n++
		}
	}
	return
}
