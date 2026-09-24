// Package attr 解析单条 Set-Cookie 头部文本：name=value 与分号分隔的属性。
package attr

import (
	"errors"
	"strconv"
	"strings"
	"time"
)

// 五类彼此可区分的解析错误；未知属性名一律忽略，不产生错误。
var (
	ErrNoEqual     = errors.New("attr: name-value pair missing =")
	ErrEmptyName   = errors.New("attr: empty cookie name")
	ErrControlChar = errors.New("attr: control character in name or value")
	ErrBadMaxAge   = errors.New("attr: Max-Age is not an integer")
	ErrBadExpires  = errors.New("attr: Expires is not a parseable date")
)

// Cookie 是一条 Set-Cookie 的解析结果。
type Cookie struct {
	Name, Value      string
	Path, Domain     string
	Secure, HttpOnly bool
	HasMaxAge        bool
	MaxAge           int64 // 秒
	HasExpires       bool
	Expires          time.Time
}

// Parse 解析一条 Set-Cookie 文本，属性名大小写不敏感。
func Parse(s string) (*Cookie, error) {
	parts := strings.Split(s, ";")
	kv := strings.SplitN(strings.TrimSpace(parts[0]), "=", 2)
	if len(kv) != 2 {
		return nil, ErrNoEqual
	}
	c := &Cookie{Name: strings.TrimSpace(kv[0]), Value: strings.TrimSpace(kv[1])}
	if c.Name == "" {
		return nil, ErrEmptyName
	}
	if hasControl(c.Name) || hasControl(c.Value) {
		return nil, ErrControlChar
	}
	for _, p := range parts[1:] {
		kv := strings.SplitN(strings.TrimSpace(p), "=", 2)
		val := ""
		if len(kv) == 2 {
			val = strings.TrimSpace(kv[1])
		}
		switch strings.ToLower(strings.TrimSpace(kv[0])) {
		case "max-age":
			n, err := strconv.ParseInt(val, 10, 64)
			if err != nil {
				return nil, ErrBadMaxAge
			}
			c.HasMaxAge, c.MaxAge = true, n
		case "expires":
			t, err := parseDate(val)
			if err != nil {
				return nil, err
			}
			c.HasExpires, c.Expires = true, t
		case "path":
			c.Path = val
		case "domain":
			c.Domain = val
		case "secure":
			c.Secure = true
		case "httponly":
			c.HttpOnly = true
		}
	}
	return c, nil
}

func hasControl(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] == 0x7f {
			return true
		}
	}
	return false
}

var dateFormats = []string{
	"Mon, 02 Jan 2006 15:04:05 GMT", // RFC 1123 GMT，Set-Cookie 标准形式
	"Mon, 02-Jan-2006 15:04:05 GMT", // RFC 850 变体
	time.RFC1123,
}

func parseDate(s string) (time.Time, error) {
	for _, f := range dateFormats {
		if t, err := time.Parse(f, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, ErrBadExpires
}
