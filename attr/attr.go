// Package attr parses a single Set-Cookie header into its attributes.
package attr

import (
	"errors"
	"strconv"
	"strings"
	"time"
)

var (
	ErrNoEqual     = errors.New("attr: missing '=' in name-value pair")
	ErrEmptyName   = errors.New("attr: empty cookie name")
	ErrControlChar = errors.New("attr: control character in header")
	ErrBadMaxAge   = errors.New("attr: Max-Age is not an integer")
	ErrBadExpires  = errors.New("attr: Expires is not a parseable date")
)

// Parsed is the result of parsing one Set-Cookie header line.
type Parsed struct {
	Name, Value string
	Path        string
	HasPath     bool
	Domain      string
	HasDomain   bool
	Secure      bool
	HTTPOnly    bool
	MaxAge      *int64 // seconds; takes precedence over Expires
	Expires     time.Time
	HasExpires  bool
}

var expiresFormats = []string{
	"Mon, 02 Jan 2006 15:04:05 GMT",
	time.RFC1123,
	time.RFC850,
	"Mon, 02-Jan-2006 15:04:05 GMT",
}

func hasControl(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] == 0x7f {
			return true
		}
	}
	return false
}

// Parse splits and validates one Set-Cookie header line.
// Unknown attribute names are ignored, never an error.
func Parse(line string) (Parsed, error) {
	var p Parsed
	if hasControl(line) {
		return p, ErrControlChar
	}
	segs := strings.Split(line, ";")
	nv := strings.TrimSpace(segs[0])
	i := strings.IndexByte(nv, '=')
	if i < 0 {
		return p, ErrNoEqual
	}
	p.Name = strings.TrimSpace(nv[:i])
	p.Value = strings.TrimSpace(nv[i+1:])
	if p.Name == "" {
		return p, ErrEmptyName
	}
	for _, seg := range segs[1:] {
		kv := strings.TrimSpace(seg)
		k, v := kv, ""
		if j := strings.IndexByte(kv, '='); j >= 0 {
			k, v = strings.TrimSpace(kv[:j]), strings.TrimSpace(kv[j+1:])
		}
		switch strings.ToLower(k) {
		case "max-age":
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				return p, ErrBadMaxAge
			}
			p.MaxAge = &n
		case "expires":
			t, err := parseDate(v)
			if err != nil {
				return p, ErrBadExpires
			}
			p.Expires, p.HasExpires = t, true
		case "path":
			p.Path, p.HasPath = v, true
		case "domain":
			p.Domain, p.HasDomain = v, true
		case "secure":
			p.Secure = true
		case "httponly":
			p.HTTPOnly = true
		}
	}
	return p, nil
}

func parseDate(s string) (time.Time, error) {
	for _, f := range expiresFormats {
		if t, err := time.Parse(f, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, ErrBadExpires
}
