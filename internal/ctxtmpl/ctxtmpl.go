// Package ctxtmpl renders templates with context-aware escaping: each
// interpolation is escaped according to the HTML context it appears in
// (text, quoted/unquoted attribute value, URL attribute value, comment),
// and URL attributes are guarded against dangerous protocol schemes.
package ctxtmpl

import (
	"fmt"
	"strings"
)

// Render renders tmpl, substituting "{{key}}" interpolations from data and
// escaping each one for the HTML context in which it appears.
func Render(tmpl string, data map[string]string) (string, error) {
	r := &renderer{data: data}
	if err := r.run(tmpl); err != nil {
		return "", err
	}
	return r.out.String(), nil
}

type mode int

const (
	modeText mode = iota
	modeTag
	modeAttrValue
	modeComment
)

type renderer struct {
	data map[string]string
	out  strings.Builder
	mode mode

	lastAttrName string
	expectValue  bool
	quote        byte // 0 (unquoted), '"' or '\''
	inURLAttr    bool
	// attrPrefix accumulates the raw (unescaped) value of the current URL
	// attribute rendered so far, across both template literals and
	// interpolations. Dangerous-protocol detection runs against this full
	// prefix: checking only the current interpolation let attackers split
	// "javascript:" across two interpolations or across literal+interpolation.
	attrPrefix   strings.Builder
	prevCommentB byte
}

func (r *renderer) run(tmpl string) error {
	for i := 0; i < len(tmpl); {
		next, err := r.step(tmpl, i)
		if err != nil {
			return err
		}
		i = next
	}
	return nil
}

func (r *renderer) step(tmpl string, i int) (int, error) {
	switch r.mode {
	case modeTag:
		return r.stepTag(tmpl, i)
	case modeAttrValue:
		return r.stepAttrValue(tmpl, i)
	case modeComment:
		return r.stepComment(tmpl, i)
	default:
		return r.stepText(tmpl, i)
	}
}

func (r *renderer) stepText(tmpl string, i int) (int, error) {
	switch {
	case strings.HasPrefix(tmpl[i:], "<!--"):
		r.out.WriteString("<!--")
		r.mode = modeComment
		r.prevCommentB = 0
		return i + 4, nil
	case tmpl[i] == '<' && i+1 < len(tmpl) && (isLetter(tmpl[i+1]) || tmpl[i+1] == '/'):
		r.out.WriteByte('<')
		r.mode = modeTag
		r.expectValue = false
		return i + 1, nil
	case strings.HasPrefix(tmpl[i:], "{{"):
		v, next, err := r.lookup(tmpl, i)
		if err != nil {
			return 0, err
		}
		r.out.WriteString(textEscaper.Replace(v))
		return next, nil
	default:
		r.out.WriteByte(tmpl[i])
		return i + 1, nil
	}
}

func (r *renderer) lookup(tmpl string, i int) (string, int, error) {
	end := strings.Index(tmpl[i+2:], "}}")
	if end < 0 {
		return "", 0, ErrUnclosedAction
	}
	key := strings.TrimSpace(tmpl[i+2 : i+2+end])
	v, ok := r.data[key]
	if !ok {
		return "", 0, fmt.Errorf("%w: %q", ErrMissingKey, key)
	}
	return v, i + 2 + end + 2, nil
}

func isLetter(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f'
}

func isNameChar(c byte) bool {
	return isLetter(c) || c >= '0' && c <= '9' || c == '-' || c == '_' || c == ':'
}
