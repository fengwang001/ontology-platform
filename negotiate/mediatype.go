package negotiate

import "strings"

// mediaType is a parsed MIME type with its parameters.
// typ and sub are lower-cased; "*" marks a wildcard.
type mediaType struct {
	typ    string
	sub    string
	params map[string]string
}

// parseMediaType parses an offer such as "text/html;level=1".
// Every ';'-separated parameter must have the form name=value.
func parseMediaType(s string) (mediaType, error) {
	var mt mediaType
	parts := strings.Split(s, ";")
	typ, sub, err := parseTypeSub(parts[0])
	if err != nil {
		return mt, err
	}
	mt.typ, mt.sub = typ, sub
	for _, p := range parts[1:] {
		name, value, err := parseParam(p)
		if err != nil {
			return mediaType{}, err
		}
		if mt.params == nil {
			mt.params = make(map[string]string)
		}
		mt.params[name] = value
	}
	return mt, nil
}

// parseTypeSub parses a "type/subtype" token, allowing "*" wildcards.
func parseTypeSub(s string) (string, string, error) {
	s = strings.TrimSpace(s)
	slash := strings.IndexByte(s, '/')
	if slash < 0 {
		return "", "", ErrMalformed
	}
	typ := strings.ToLower(strings.TrimSpace(s[:slash]))
	sub := strings.ToLower(strings.TrimSpace(s[slash+1:]))
	if typ == "" || sub == "" {
		return "", "", ErrMalformed
	}
	if strings.Contains(sub, "/") {
		return "", "", ErrMalformed
	}
	if strings.Contains(typ, "*") && typ != "*" {
		return "", "", ErrMalformed
	}
	if strings.Contains(sub, "*") && sub != "*" {
		return "", "", ErrMalformed
	}
	if typ == "*" && sub != "*" {
		return "", "", ErrMalformed
	}
	return typ, sub, nil
}

// parseParam parses a single "name=value" parameter segment.
func parseParam(p string) (string, string, error) {
	eq := strings.IndexByte(p, '=')
	if eq < 0 {
		return "", "", ErrMalformed
	}
	name := strings.ToLower(strings.TrimSpace(p[:eq]))
	if name == "" {
		return "", "", ErrMalformed
	}
	return name, strings.TrimSpace(p[eq+1:]), nil
}

// parseQ parses a q value and returns it scaled by 1000 (0..1000).
// Only "0", "1" and up to three decimal places are accepted.
func parseQ(v string) (int, error) {
	v = strings.TrimSpace(v)
	if len(v) == 0 || len(v) > 5 {
		return 0, ErrMalformed
	}
	if v[0] != '0' && v[0] != '1' {
		return 0, ErrMalformed
	}
	milli := 0
	if v[0] == '1' {
		milli = 1000
	}
	if len(v) == 1 {
		return milli, nil
	}
	if v[1] != '.' || len(v) == 2 {
		return 0, ErrMalformed
	}
	scale := 100
	for _, c := range v[2:] {
		if c < '0' || c > '9' {
			return 0, ErrMalformed
		}
		milli += int(c-'0') * scale
		scale /= 10
	}
	if milli > 1000 {
		return 0, ErrMalformed
	}
	return milli, nil
}
