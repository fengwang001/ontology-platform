package ctxtmpl

import (
	"fmt"
	"strings"
)

// Render expands every {{name}} action in tmpl with data[name],
// escaping each value for the HTML context in which the action
// appears. It returns an error (matching one of the package's
// sentinel errors) for malformed actions, missing keys, dangerous
// URLs, interpolations in attribute-name position, and templates
// that end with an unclosed quote, tag or comment.
func Render(tmpl string, data map[string]string) (string, error) {
	var out strings.Builder
	out.Grow(len(tmpl))
	sc := &scanner{state: stText}
	for i := 0; i < len(tmpl); {
		if tmpl[i] == '{' && i+1 < len(tmpl) && tmpl[i+1] == '{' {
			name, next, err := parseAction(tmpl, i)
			if err != nil {
				return "", err
			}
			val, ok := data[name]
			if !ok {
				return "", fmt.Errorf("%w: %q", ErrMissingKey, name)
			}
			if err := sc.interpolate(&out, val); err != nil {
				return "", err
			}
			i = next
			continue
		}
		i = sc.step(&out, tmpl, i)
	}
	if err := sc.finish(); err != nil {
		return "", err
	}
	return out.String(), nil
}

// interpolate writes val, escaped for the current context, to out.
func (sc *scanner) interpolate(out *strings.Builder, val string) error {
	switch sc.state {
	case stText:
		out.WriteString(escapeText(val))
	case stAttrDouble:
		if err := sc.checkURL(val); err != nil {
			return err
		}
		out.WriteString(escapeDoubleQuoted(val))
	case stAttrSingle:
		if err := sc.checkURL(val); err != nil {
			return err
		}
		out.WriteString(escapeSingleQuoted(val))
	case stAttrUnquoted:
		if err := sc.checkURL(val); err != nil {
			return err
		}
		out.WriteString(escapeUnquoted(val))
	case stComment:
		out.WriteString(escapeComment(val))
	case stTag:
		return ErrAttrNamePosition
	}
	return nil
}

// checkURL applies the dangerous-scheme rule when the interpolation
// sits at the very start of a URL attribute value, and tracks whether
// the value is still at its start afterwards.
func (sc *scanner) checkURL(val string) error {
	if sc.valueStart && isURLAttr(sc.valueAttr) && isDangerousURL(val) {
		return fmt.Errorf("%w: %q", ErrDangerousURL, val)
	}
	sc.valueStart = sc.valueStart && isAllSpace(val)
	return nil
}

// parseAction parses the {{name}} action starting at offset i
// (tmpl[i:i+2] == "{{") and returns the name and the offset just
// past the closing "}}".
func parseAction(tmpl string, i int) (string, int, error) {
	j := i + 2
	if j >= len(tmpl) || !isNameStart(tmpl[j]) {
		return "", 0, fmt.Errorf("%w at offset %d", ErrBadAction, i)
	}
	start := j
	for j < len(tmpl) && isNameChar(tmpl[j]) {
		j++
	}
	name := tmpl[start:j]
	if j+2 > len(tmpl) || tmpl[j] != '}' || tmpl[j+1] != '}' {
		return "", 0, fmt.Errorf("%w at offset %d", ErrBadAction, i)
	}
	return name, j + 2, nil
}

func isNameStart(c byte) bool {
	return isLetter(c) || c == '_'
}

func isNameChar(c byte) bool {
	return isNameStart(c) || c >= '0' && c <= '9'
}
