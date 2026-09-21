package ctxtmpl

import (
	"fmt"
	"strings"
)

func (r *renderer) stepTag(tmpl string, i int) (int, error) {
	c := tmpl[i]
	switch {
	case c == '>':
		r.out.WriteByte(c)
		r.mode = modeText
		return i + 1, nil
	case isSpace(c) || c == '/':
		r.out.WriteByte(c)
		return i + 1, nil
	case c == '=':
		r.out.WriteByte(c)
		r.expectValue = true
		return i + 1, nil
	case c == '"' || c == '\'':
		r.enterAttrValue(c)
		r.out.WriteByte(c)
		return i + 1, nil
	case r.expectValue:
		r.enterAttrValue(0) // unquoted value; do not consume the first byte
		return i, nil
	default: // attribute or tag name
		j := i
		for j < len(tmpl) && isNameChar(tmpl[j]) {
			j++
		}
		if j == i {
			r.out.WriteByte(c)
			return i + 1, nil
		}
		r.lastAttrName = strings.ToLower(tmpl[i:j])
		r.out.WriteString(tmpl[i:j])
		return j, nil
	}
}

func (r *renderer) enterAttrValue(quote byte) {
	r.mode = modeAttrValue
	r.quote = quote
	r.expectValue = false
	r.inURLAttr = isURLAttr(r.lastAttrName)
	r.attrPrefix.Reset()
}

func (r *renderer) stepAttrValue(tmpl string, i int) (int, error) {
	c := tmpl[i]
	if r.quote != 0 && c == r.quote {
		r.out.WriteByte(c)
		r.mode = modeTag
		return i + 1, nil
	}
	if r.quote == 0 && (isSpace(c) || c == '>') {
		r.mode = modeTag
		return i, nil
	}
	if strings.HasPrefix(tmpl[i:], "{{") {
		v, next, err := r.lookup(tmpl, i)
		if err != nil {
			return 0, err
		}
		if r.inURLAttr && hasDangerousProtocol(r.attrPrefix.String()+v) {
			return 0, fmt.Errorf("%w in attribute %q", ErrDangerousProtocol, r.lastAttrName)
		}
		if r.inURLAttr {
			r.attrPrefix.WriteString(v)
		}
		r.out.WriteString(r.escapeAttr(v))
		return next, nil
	}
	if r.inURLAttr {
		r.attrPrefix.WriteByte(c)
	}
	r.out.WriteByte(c)
	return i + 1, nil
}

func (r *renderer) escapeAttr(s string) string {
	switch r.quote {
	case '"':
		return doubleQuotedAttrEscaper.Replace(s)
	case '\'':
		return singleQuotedAttrEscaper.Replace(s)
	default:
		return unquotedAttrEscaper.Replace(s)
	}
}

func (r *renderer) stepComment(tmpl string, i int) (int, error) {
	if strings.HasPrefix(tmpl[i:], "-->") {
		r.out.WriteString("-->")
		r.mode = modeText
		return i + 3, nil
	}
	if strings.HasPrefix(tmpl[i:], "{{") {
		v, next, err := r.lookup(tmpl, i)
		if err != nil {
			return 0, err
		}
		r.appendComment(v)
		return next, nil
	}
	r.appendComment(tmpl[i : i+1])
	return i + 1, nil
}

// appendComment emits comment content, escaping "-" as "&#45;" when it would
// directly follow a previously emitted "-". The check looks at the assembled
// comment content, not a single interpolation: a lone "-" from one
// interpolation plus "->" from the next would otherwise form "-->" and
// terminate the comment early.
func (r *renderer) appendComment(s string) {
	for k := 0; k < len(s); k++ {
		if s[k] == '-' && r.prevCommentB == '-' {
			r.out.WriteString("&#45;")
		} else {
			r.out.WriteByte(s[k])
		}
		r.prevCommentB = s[k]
	}
}
