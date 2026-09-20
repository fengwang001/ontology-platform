package ctxtmpl

import "strings"

// Render renders tmpl by substituting {{name}} interpolations with values
// from data. Escaping is chosen from the HTML context inferred from the
// template text preceding each interpolation.
func Render(tmpl string, data map[string]string) (string, error) {
	var out strings.Builder
	sc := newScanner()

	for len(tmpl) > 0 {
		open := strings.Index(tmpl, "{{")
		if open < 0 {
			if err := sc.consumeLiteral(tmpl); err != nil {
				return "", err
			}
			out.WriteString(tmpl)
			break
		}
		lit := tmpl[:open]
		if err := sc.consumeLiteral(lit); err != nil {
			return "", err
		}
		out.WriteString(lit)

		name, rest, err := parseInterpolation(tmpl[open:])
		if err != nil {
			return "", err
		}
		value, ok := data[name]
		if !ok {
			return "", &TemplateError{Err: ErrMissingKey, Pos: open, Key: name}
		}
		escaped, err := sc.applyValue(value)
		if err != nil {
			return "", &TemplateError{Err: err, Pos: open, Key: name}
		}
		out.WriteString(escaped)
		tmpl = rest
	}

	if err := sc.finish(); err != nil {
		return "", err
	}
	return out.String(), nil
}

// parseInterpolation parses a string beginning with "{{". It returns the
// key, the remainder after "}}", or ErrInvalidSyntax.
func parseInterpolation(s string) (string, string, error) {
	const delim = "{{"
	rest := s[len(delim):]
	i := 0
	if i >= len(rest) || !isNameStart(rest[i]) {
		return "", "", ErrInvalidSyntax
	}
	for i < len(rest) && isNameChar(rest[i]) {
		i++
	}
	if i+2 > len(rest) || rest[i] != '}' || rest[i+1] != '}' {
		return "", "", ErrInvalidSyntax
	}
	return rest[:i], rest[i+2:], nil
}

func isNameStart(c byte) bool {
	return isAlpha(c) || c == '_'
}

func isNameChar(c byte) bool {
	return isNameStart(c) || c >= '0' && c <= '9'
}
