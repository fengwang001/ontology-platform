package attr

import (
	"errors"
	"strconv"
	"strings"
	"time"
)

var (
	ErrNoEquals       = errors.New("attr: missing '='")
	ErrEmptyName      = errors.New("attr: empty cookie name")
	ErrControlName    = errors.New("attr: control character in cookie name")
	ErrControlValue   = errors.New("attr: control character in cookie value")
	ErrControlKey     = errors.New("attr: control character in attribute name")
	ErrControlData    = errors.New("attr: control character in attribute value")
	ErrInvalidMaxAge  = errors.New("attr: Max-Age is not an integer")
	ErrInvalidExpires = errors.New("attr: Expires is not a valid time")
	ErrInvalidPath    = errors.New("attr: Path contains a control character")
)

type Cookie struct {
	Name     string
	Value    string
	Path     string
	Domain   string
	Expires  time.Time
	MaxAge   int
	HasPath  bool
	HasDomain bool
	HasMaxAge bool
	HasExpires bool
	Secure   bool
}

func hasCTL(s string) bool { return strings.ContainsAny(s, "\x00\x01\x02\x03\x04\x05\x06\x07\x08\x0b\x0c\x0e\x0f\x10\x11\x12\x13\x14\x15\x16\x17\x18\x19\x1a\x1b\x1c\x1d\x1e\x1f\x7f") }

func Parse(line string) (*Cookie, error) {
	parts := strings.Split(line, ";")
	first := strings.TrimSpace(parts[0])
	if !strings.Contains(first, "=") {
		return nil, ErrNoEquals
	}
	name, value, _ := strings.Cut(first, "=")
	name = strings.TrimSpace(name)
	value = strings.TrimSpace(value)
	if name == "" {
		return nil, ErrEmptyName
	}
	if hasCTL(name) {
		return nil, ErrControlName
	}
	if hasCTL(value) {
		return nil, ErrControlValue
	}
	c := &Cookie{Name: name, Value: value}
	for _, part := range parts[1:] {
		key, val, ok := strings.Cut(strings.TrimSpace(part), "=")
		key = strings.TrimSpace(key)
		if ok {
			val = strings.TrimSpace(val)
		}
		if key == "" {
			continue
		}
		if hasCTL(key) {
			return nil, ErrControlKey
		}
		switch strings.ToLower(key) {
		case "path":
			if hasCTL(val) {
				return nil, ErrInvalidPath
			}
			c.Path, c.HasPath = val, true
		case "domain":
			if hasCTL(val) {
				return nil, ErrControlData
			}
			c.Domain, c.HasDomain = val, true
		case "max-age":
			if hasCTL(val) {
				return nil, ErrControlData
			}
			n, err := strconv.Atoi(val)
			if err != nil {
				return nil, ErrInvalidMaxAge
			}
			c.MaxAge, c.HasMaxAge = n, true
		case "expires":
			if hasCTL(val) {
				return nil, ErrControlData
			}
			t, err := time.Parse(time.RFC1123, val)
			if err != nil {
				return nil, ErrInvalidExpires
			}
			c.Expires, c.HasExpires = t, true
		case "secure":
			c.Secure = true
		}
	}
	return c, nil
}
