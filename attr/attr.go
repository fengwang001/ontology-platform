// Package attr attr 解析单条 Set-Cookie 头部文本。
package attr

import (
	"errors"
	"strconv"
	"strings"
	"time"
)

var (
	ErrNoEquals   = errors.New("attr: missing '=' in name-value pair")
	ErrEmptyName  = errors.New("attr: empty cookie name")
	ErrControl    = errors.New("attr: control character in name or value")
	ErrBadMaxAge  = errors.New("attr: Max-Age is not an integer")
	ErrBadExpires = errors.New("attr: Expires is not parseable")
)

// Cookie 是一条 Set-Cookie 的解析结果。
type Cookie struct {
	Name       string
	Value      string
	Domain     string
	Path       string
	HasMaxAge  bool
	MaxAge     int64 // 秒
	HasExpires bool
	Expires    time.Time
	Secure     bool
	HTTPOnly   bool
}

var expiresLayouts = []string{
	time.RFC1123, // Mon, 02 Jan 2006 15:04:05 GMT
	"Mon, 02-Jan-2006 15:04:05 GMT",
	time.RFC1123Z,
	time.RFC3339,
	"2006-01-02 15:04:05",
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
func Parse(line string) (*Cookie, error) {
	parts := strings.Split(line, ";")
	eq := strings.IndexByte(parts[0], '=')
	if eq < 0 {
		return nil, ErrNoEquals
	}
	c := &Cookie{
		Name:  strings.TrimSpace(parts[0][:eq]),
		Value: strings.TrimSpace(parts[0][eq+1:]),
	}
	if c.Name == "" {
		return nil, ErrEmptyName
	}
	if hasControl(c.Name) || hasControl(c.Value) {
		return nil, ErrControl
	}
	for _, p := range parts[1:] {
		k, v, _ := strings.Cut(strings.TrimSpace(p), "=")
		v = strings.TrimSpace(v)
		switch strings.ToLower(strings.TrimSpace(k)) {
		case "max-age":
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				return nil, ErrBadMaxAge
			}
			c.HasMaxAge, c.MaxAge = true, n
		case "expires":
			t, err := parseTime(v)
			if err != nil {
				return nil, ErrBadExpires
			}
			c.HasExpires, c.Expires = true, t
		case "domain":
			c.Domain = strings.ToLower(strings.TrimPrefix(v, "."))
		case "path":
			c.Path = v
		case "secure":
			c.Secure = true
		case "httponly":
			c.HTTPOnly = true
		}
	}
	return c, nil
}

func parseTime(s string) (time.Time, error) {
	var err error
	var t time.Time
	for _, l := range expiresLayouts {
		if t, err = time.Parse(l, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, ErrBadExpires
}
