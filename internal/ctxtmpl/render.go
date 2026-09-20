package ctxtmpl

import "strings"

// Render interpolates values from data into tmpl, escaping each value
// according to the HTML context of its interpolation point.
func Render(tmpl string, data map[string]string) (string, error) {
	var s scanner
	var out strings.Builder
	out.Grow(len(tmpl))

	for {
		idx := strings.Index(tmpl, openDelim)
		if idx < 0 {
			s.feedLiteral(tmpl, false)
			out.WriteString(tmpl)
			break
		}
		s.feedLiteral(tmpl[:idx], true)
		out.WriteString(tmpl[:idx])

		name, rest, ok := parseInterpolation(tmpl[idx:])
		if !ok {
			return "", &SyntaxError{
				Offset: out.Len(),
				Err:    ErrInvalidInterpolation,
			}
		}

		value, present := data[name]
		ctx, valid := s.currentContext()
		// Parse validity and structural validity are checked before key lookup
		// so malformed templates always report the template error.
		if !valid {
			return "", &SyntaxError{
				Offset: out.Len(),
				Err:    ErrInterpolationInTagName,
			}
		}
		if !present {
			return "", &UnknownKeyError{Key: name}
		}
		if ctx.urlAttr && ctx.urlStart {
			if err := checkDangerousURL(value); err != nil {
				return "", err
			}
		}
		out.WriteString(escapeValue(value, ctx))
		s.feedInterpolation(value)
		tmpl = rest
	}

	if err := s.finish(); err != nil {
		return "", err
	}
	return out.String(), nil
}

const (
	openDelim  = "{{"
	closeDelim = "}}"
)

// parseInterpolation parses a string starting with "{{". It returns the name
// and the remainder after "}}" on success.
func parseInterpolation(seg string) (name, rest string, ok bool) {
	if !strings.HasPrefix(seg, openDelim) {
		return "", "", false
	}
	i := len(openDelim)
	if i >= len(seg) || !isNameStart(seg[i]) {
		return "", "", false
	}
	start := i
	for i < len(seg) && isNamePart(seg[i]) {
		i++
	}
	name = seg[start:i]
	if !strings.HasPrefix(seg[i:], closeDelim) {
		return "", "", false
	}
	return name, seg[i+len(closeDelim):], true
}

func isNameStart(ch byte) bool {
	return isASCIIAlpha(ch) || ch == '_'
}

func isNamePart(ch byte) bool {
	return isNameStart(ch) || ch >= '0' && ch <= '9'
}
