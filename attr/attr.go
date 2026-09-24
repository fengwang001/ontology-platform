// Package attr 解析单条 Set-Cookie 头部文本，不依赖其他包。
package attr

import (
	"errors"
	"strconv"
	"strings"
	"time"
)

// 可彼此区分的解析错误。
var (
	ErrNoEqual     = errors.New("attr: name-value pair missing '='")
	ErrEmptyName   = errors.New("attr: empty cookie name")
	ErrControlChar = errors.New("attr: control character in header")
	ErrBadMaxAge   = errors.New("attr: Max-Age is not an integer")
	ErrBadExpires  = errors.New("attr: Expires is not a parseable date")
)

// Cookie 是一条 Set-Cookie 的解析结果。
type Cookie struct {
	Name, Value      string
	Domain, Path     string
	Secure, HTTPOnly bool
	MaxAge           int64 // 秒
	HasMaxAge        bool
	Expires          time.Time
	HasExpires       bool
}

var expiresLayouts = []string{
	"Mon, 02 Jan 2006 15:04:05 GMT",
	"Mon, 02-Jan-2006 15:04:05 GMT",
	"Mon Jan  2 15:04:05 2006",
	time.RFC1123,
}

func hasControl(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] == 0x7f {
			return true
		}
	}
	return false
}

// Parse 解析一条 Set-Cookie 文本；未知属性名被忽略。
func Parse(s string) (*Cookie, error) {
	if hasControl(s) {
		return nil, ErrControlChar
	}
	segs := strings.Split(s, ";")
	pair := strings.TrimSpace(segs[0])
	eq := strings.IndexByte(pair, '=')
	if eq < 0 {
		return nil, ErrNoEqual
	}
	name := strings.TrimSpace(pair[:eq])
	if name == "" {
		return nil, ErrEmptyName
	}
	c := &Cookie{Name: name, Value: strings.TrimSpace(pair[eq+1:])}
	for _, seg := range segs[1:] {
		seg = strings.TrimSpace(seg)
		an, av := seg, ""
		if i := strings.IndexByte(seg, '='); i >= 0 {
			an, av = seg[:i], strings.TrimSpace(seg[i+1:])
		}
		switch strings.ToLower(strings.TrimSpace(an)) {
		case "domain":
			c.Domain = strings.ToLower(strings.TrimPrefix(av, "."))
		case "path":
			c.Path = av
		case "max-age":
			n, err := strconv.ParseInt(av, 10, 64)
			if err != nil {
				return nil, ErrBadMaxAge
			}
			c.MaxAge, c.HasMaxAge = n, true
		case "expires":
			t, err := parseDate(av)
			if err != nil {
				return nil, ErrBadExpires
			}
			c.Expires, c.HasExpires = t, true
		case "secure":
			c.Secure = true
		case "httponly":
			c.HTTPOnly = true
		}
	}
	return c, nil
}

func parseDate(s string) (time.Time, error) {
	for _, l := range expiresLayouts {
		if t, err := time.Parse(l, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, ErrBadExpires
}
