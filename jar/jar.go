// Package jar 是进程内 cookie 罐：解析、存储、覆盖、惰性过期与适用性判定。
package jar

import (
	"errors"
	"net/url"
	"sort"
	"strings"
	"time"

	"ontology/attr"
	"ontology/match"
)

var ErrDomainNotAllowed, ErrPublicSuffix = errors.New("jar: Domain not allowed for origin host"), errors.New("jar: Domain is a single-label public suffix")
var ErrHostNeedSecure, ErrHostHasDomain = errors.New("jar: __Host- cookie requires secure origin"), errors.New("jar: __Host- cookie must not have Domain")
var ErrHostBadPath, ErrSecureNeedSecure = errors.New("jar: __Host- cookie requires Path=/"), errors.New("jar: __Secure- cookie requires secure origin")

type entry struct {
	c                *attr.Cookie
	hostOnly         bool
	created, expires time.Time
}

type Jar struct {
	now func() time.Time
	m   map[string]*entry
}

func New(now func() time.Time) *Jar { return &Jar{now: now, m: map[string]*entry{}} }

func (j *Jar) Set(u *url.URL, header string) error {
	c, err := attr.Parse(header)
	if err != nil {
		return err
	}
	host, secure := strings.ToLower(u.Hostname()), u.Scheme == "https"
	isHost := strings.HasPrefix(c.Name, "__Host-")
	switch {
	case strings.HasPrefix(c.Name, "__Secure-") && !secure:
		return ErrSecureNeedSecure
	case isHost && !secure:
		return ErrHostNeedSecure
	case isHost && c.Domain != "":
		return ErrHostHasDomain
	case isHost && c.Path != "/":
		return ErrHostBadPath
	}
	hostOnly, domain := c.Domain == "", c.Domain
	if hostOnly {
		domain = host
	} else if !strings.Contains(domain, ".") {
		return ErrPublicSuffix
	} else if !match.Domain(host, domain) {
		return ErrDomainNotAllowed
	}
	path := match.DefaultPath(c.Path, u.Path)
	k := c.Name + "\x00" + domain + "\x00" + path
	if c.HasMaxAge && c.MaxAge <= 0 { // 立即过期：删除而非存过期项
		delete(j.m, k)
		return nil
	}
	e := &entry{c: c, hostOnly: hostOnly, created: j.now()}
	if c.HasMaxAge {
		e.expires = e.created.Add(time.Duration(c.MaxAge) * time.Second)
	} else if c.HasExpires {
		e.expires = c.Expires
	}
	if old, ok := j.m[k]; ok { // 覆盖：保留最早创建时间
		e.created = old.created
	}
	e.c.Domain, e.c.Path = domain, path
	j.m[k] = e
	return nil
}

func (j *Jar) reap() {
	for k, e := range j.m {
		if !e.expires.IsZero() && !j.now().Before(e.expires) {
			delete(j.m, k)
		}
	}
}

// Cookies 返回对 u 适用的 cookie：先惰性剔除过期项，再按路径更长、创建更早排序。
func (j *Jar) Cookies(u *url.URL) []*attr.Cookie {
	j.reap()
	host := strings.ToLower(u.Hostname())
	var es []*entry
	for _, e := range j.m {
		domOK := e.hostOnly && host == e.c.Domain || !e.hostOnly && match.Domain(host, e.c.Domain)
		if e.c.Secure && u.Scheme != "https" || !domOK || !match.Path(u.Path, e.c.Path) {
			continue
		}
		es = append(es, e)
	}
	sort.SliceStable(es, func(a, b int) bool {
		if la, lb := len(es[a].c.Path), len(es[b].c.Path); la != lb {
			return la > lb
		}
		return es[a].created.Before(es[b].created)
	})
	out := make([]*attr.Cookie, len(es))
	for i, e := range es {
		out[i] = e.c
	}
	return out
}

func (j *Jar) Len() int { j.reap(); return len(j.m) }

func (j *Jar) CountByDomain(domain string) (n int) {
	j.reap()
	for _, e := range j.m {
		if e.c.Domain == strings.ToLower(strings.TrimPrefix(domain, ".")) {
			n++
		}
	}
	return
}
